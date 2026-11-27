package io.github.atqamz.handcompanion

import java.io.ByteArrayInputStream
import java.io.Closeable
import java.io.File
import java.io.FilterInputStream
import java.io.IOException
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.net.URL
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.DelicateCoroutinesApi
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.GlobalScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

private const val FLEET = "f000000000007"
private const val COOKIE = "hand_phone_f000000000007=1.0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

private fun clip(kind: String, size: Long, name: String) =
  Raw(
    200,
    """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":{"path":"reports/$name","name":"$name","kind":"$kind","text":false,"size":$size}}"""
  )

private class ClipBoard(var answer: () -> Raw) : Transport {
  override fun request(fleet: String, post: Boolean, path: String, csrf: String?, body: String?) = answer()
}

private class FakeClip(
  override val duration: Int = 90_000,
  override val width: Int = 1280,
  override val height: Int = 720
) : VideoPlayer {
  override var position = 0
  var released = false
  var running = false
  val shown = mutableListOf<Boolean>()
  val seeks = mutableListOf<Int>()

  override fun start() {
    running = true
  }

  override fun pause() {
    running = false
  }

  override fun seekTo(millis: Int) {
    seeks += millis
    position = millis
  }

  override fun display(holder: android.view.SurfaceHolder?) {
    shown += holder != null
  }

  override fun release() {
    released = true
  }
}

private class FakeVideos : VideoOpener {
  val clips = CopyOnWriteArrayList<FakeClip>()
  val asked = CopyOnWriteArrayList<String>()
  var failure: IOException? = null
  var events: AudioEvents? = null
  var entered: CountDownLatch? = null
  var gate: CountDownLatch? = null

  override fun open(fleet: String, route: String, size: Long, events: AudioEvents): VideoPlayer {
    asked += "$fleet $route $size"
    entered?.countDown()
    gate?.await(5, TimeUnit.SECONDS)
    failure?.let { throw it }
    this.events = events
    return FakeClip().also { clips += it }
  }
}

@OptIn(ExperimentalCoroutinesApi::class, DelicateCoroutinesApi::class)
class DocVideoTest {
  @Before fun main() = Dispatchers.setMain(Dispatchers.Unconfined)

  @After fun reset() = Dispatchers.resetMain()

  private val videos = FakeVideos()
  private val unused = FileSource { _, _ -> throw AssertionError("a video is never fetched whole") }

  private fun model(size: Long = 40_000_000, path: String = "reports/clip one.mp4") =
    DocModel(FLEET, path, ClipBoard { clip("video", size, "clip one.mp4") }, files = unused, videoOpener = videos)

  @Test
  fun aVideoIsOpenedByFleetRouteAndSizeWithoutAWholeFetch() = runBlocking {
    val model = model()
    model.load()
    assertEquals(listOf("$FLEET reports/clip%20one.mp4 40000000"), videos.asked.toList())
    assertEquals(Playback(duration = 90_000, width = 1280, height = 720), model.state.value.audio)
    assertEquals(DocBody.Video, docBody(model.state.value))
  }

  @Test
  fun aVideoIsNeverStartedByLoading() = runBlocking {
    val model = model()
    model.load()
    assertFalse(videos.clips[0].running)
    assertFalse(model.state.value.audio!!.playing)
  }

  @Test
  fun aVideoAboveTheAudioLimitStillOpens() = runBlocking {
    val model = model(MEDIA_LIMIT * 20)
    model.load()
    assertEquals(1, videos.clips.size)
  }

  @Test
  fun aCodecTheDeviceLacksIsANoticeNotAProblem() = runBlocking {
    videos.failure = Unplayable()
    val model = model()
    model.load()
    assertEquals("This video can't be played here.", model.state.value.notice?.title)
    assertNull(model.state.value.audio)
    assertNull(model.state.value.problem)
    assertEquals(DocBody.MediaNotice, docBody(model.state.value))
  }

  @Test
  fun anUnreachableStreamIsAProblemWithRetry() = runBlocking {
    videos.failure = IOException("down")
    val model = model()
    model.load()
    assertEquals("The video could not be loaded.", model.state.value.problem)
    assertEquals(DocBody.Failed, docBody(model.state.value))
    videos.failure = null
    model.load()
    assertNull(model.state.value.problem)
    assertEquals(DocBody.Video, docBody(model.state.value))
  }

  @Test
  fun leavingTheScreenReleasesThePlayerAndPlayingAgainReopensAtThePosition() = runBlocking {
    val model = model()
    model.load()
    model.start()
    videos.clips[0].position = 12_000
    model.release()
    assertTrue(videos.clips[0].released)
    assertEquals(Playback(false, 12_000, 90_000, null, 1280, 720), model.state.value.audio)
    model.start()
    assertEquals(2, videos.clips.size)
    assertEquals(listOf(12_000), videos.clips[1].seeks)
    assertTrue(videos.clips[1].running)
  }

  @Test
  fun clearingTheModelReleasesThePlayerAndNothingReopens() = runBlocking {
    val model = model()
    model.load()
    model.start()
    model.onCleared()
    assertTrue(videos.clips[0].released)
    model.start()
    assertEquals(1, videos.clips.size)
  }

  @Test
  fun loadingAgainReleasesTheEarlierPlayer() = runBlocking {
    val model = model()
    model.load()
    model.load()
    assertTrue(videos.clips[0].released)
    assertFalse(videos.clips[1].released)
  }

  @Test
  fun aPlaybackErrorReleasesThePlayerAndALaterSuccessClearsIt() = runBlocking {
    val model = model()
    model.load()
    model.start()
    videos.events!!.failed()
    assertTrue(videos.clips[0].released)
    assertEquals("The video stopped with an error.", model.state.value.audio!!.error)
    assertFalse(model.state.value.audio!!.playing)
    model.start()
    assertNull(model.state.value.audio!!.error)
    assertTrue(model.state.value.audio!!.playing)
  }

  @Test
  fun aFailedReopenSaysVideoAndALaterReopenClearsIt() = runBlocking {
    val model = model()
    model.load()
    model.release()
    videos.failure = IOException("down")
    model.start()
    assertEquals("The video could not be played.", model.state.value.audio!!.error)
    videos.failure = null
    model.start()
    assertNull(model.state.value.audio!!.error)
    assertTrue(model.state.value.audio!!.playing)
  }

  @Test
  fun aFinishedVideoStartsFromTheBeginning() = runBlocking {
    val model = model()
    model.load()
    model.start()
    videos.events!!.ended()
    assertEquals(90_000, model.state.value.audio!!.position)
    model.start()
    assertEquals(0, model.state.value.audio!!.position)
    assertTrue(model.state.value.audio!!.playing)
  }

  @Test
  fun seekAndTickMoveThePosition() = runBlocking {
    val model = model()
    model.load()
    model.start()
    videos.clips[0].position = 3000
    model.tick()
    assertEquals(3000, model.state.value.audio!!.position)
    model.seek(60_000)
    assertEquals(60_000, model.state.value.audio!!.position)
    assertEquals(listOf(0, 60_000), videos.clips[0].seeks)
  }

  @Test
  fun theSurfaceIsHandedToTheOpenPlayerAndToALaterOne() = runBlocking {
    val model = model()
    val holder = java.lang.reflect.Proxy.newProxyInstance(
      javaClass.classLoader,
      arrayOf(android.view.SurfaceHolder::class.java)
    ) { _, _, _ -> null } as android.view.SurfaceHolder
    model.surface(holder)
    model.load()
    assertEquals(listOf(true), videos.clips[0].shown)
    model.surface(null)
    assertEquals(listOf(true, false), videos.clips[0].shown)
    model.surface(holder)
    model.release()
    model.start()
    assertEquals(listOf(true), videos.clips[1].shown)
  }

  @Test
  fun aLoadThatIsOverTakenReleasesWhatItOpened() {
    val entered = CountDownLatch(1)
    val gate = CountDownLatch(1)
    videos.entered = entered
    videos.gate = gate
    var first = true
    val board = ClipBoard { if (first) clip("video", 10, "a.mp4") else clip("folder", 0, "d") }
    val model = DocModel(FLEET, "reports/a.mp4", board, files = unused, videoOpener = videos)
    val worker = Thread { runBlocking { model.load() } }
    worker.start()
    assertTrue(entered.await(5, TimeUnit.SECONDS))
    first = false
    runBlocking { model.load() }
    gate.countDown()
    worker.join(5000)
    assertEquals(1, videos.clips.size)
    assertTrue(videos.clips[0].released)
    assertEquals("folder", model.state.value.node?.kind)
    assertNull(model.state.value.audio)
  }

  @Test
  fun clearingTheModelDuringALoadReleasesWhatTheLoadOpens() {
    val entered = CountDownLatch(1)
    val gate = CountDownLatch(1)
    videos.entered = entered
    videos.gate = gate
    val model = model()
    val worker = Thread { runBlocking { model.load() } }
    worker.start()
    assertTrue(entered.await(5, TimeUnit.SECONDS))
    model.onCleared()
    gate.countDown()
    worker.join(5000)
    assertEquals(1, videos.clips.size)
    assertTrue(videos.clips[0].released)
  }

  @Test
  fun leavingDuringAReopenDoesNotPlayInTheBackground() {
    val model = model()
    runBlocking {
      model.load()
      model.release()
    }
    val entered = CountDownLatch(1)
    val gate = CountDownLatch(1)
    videos.entered = entered
    videos.gate = gate
    val job = GlobalScope.launch(Dispatchers.Default) { model.start() }
    assertTrue(entered.await(5, TimeUnit.SECONDS))
    model.release()
    gate.countDown()
    runBlocking { job.join() }
    assertEquals(2, videos.clips.size)
    assertFalse(videos.clips[1].running)
    assertTrue(videos.clips[1].released)
    assertFalse(model.state.value.audio!!.playing)
  }

  @Test
  fun aStaleAnswerAfterLeavingForAnotherFileIsDropped() {
    val entered = CountDownLatch(1)
    val gate = CountDownLatch(1)
    val model = model()
    runBlocking { model.load() }
    runBlocking { model.release() }
    videos.entered = entered
    videos.gate = gate
    val job = GlobalScope.launch(Dispatchers.Default) { model.start() }
    assertTrue(entered.await(5, TimeUnit.SECONDS))
    runBlocking { model.load() }
    gate.countDown()
    runBlocking { job.join() }
    assertTrue(videos.clips[1].released)
    assertFalse(videos.clips[2].released)
    assertFalse(model.state.value.audio!!.playing)
  }

  @Test
  fun theProxyAnswerIsAcceptedOnlyForThisFleetOnLoopback() {
    val good = """{"port":"41234","cookie":"$COOKIE"}"""
    val link = proxyLink(good, FLEET)
    assertNotNull(link)
    assertEquals("http://localhost:41234", link!!.base)
    assertEquals(COOKIE, link.cookie)
    assertNull(proxyLink(null, FLEET))
    assertNull(proxyLink("""{"port":"0","cookie":"$COOKIE"}""", FLEET))
    assertNull(proxyLink("""{"port":"70000","cookie":"$COOKIE"}""", FLEET))
    assertNull(proxyLink("""{"port":"x","cookie":"$COOKIE"}""", FLEET))
    assertNull(proxyLink("""{"cookie":"$COOKIE"}""", FLEET))
    assertNull(proxyLink("""{"port":"41234"}""", FLEET))
    assertNull(proxyLink(good, "f000000000008"))
    assertNull(proxyLink("""{"port":"41234","cookie":"$COOKIE; a=b"}""", FLEET))
    assertNull(proxyLink("""{"port":"41234","cookie":"hand_phone_$FLEET=1.zz"}""", FLEET))
  }

  private class Wire(val handler: (Map<String, String>) -> ByteArray) : AutoCloseable {
    val hits = CopyOnWriteArrayList<Map<String, String>>()
    private val socket = ServerSocket(0, 8, InetAddress.getLoopbackAddress())
    val port get() = socket.localPort
    private val thread =
      Thread {
        while (!socket.isClosed) {
          val client = try { socket.accept() } catch (_: IOException) { break }
          answer(client)
        }
      }

    init {
      thread.isDaemon = true
      thread.start()
    }

    private fun answer(client: Socket) {
      client.use {
        client.soTimeout = 5000
        val input = client.getInputStream().bufferedReader(Charsets.ISO_8859_1)
        val head = mutableMapOf<String, String>()
        head["line"] = input.readLine() ?: return
        while (true) {
          val line = input.readLine() ?: break
          if (line.isEmpty()) break
          head[line.substringBefore(':').lowercase()] = line.substringAfter(':').trim()
        }
        hits += head
        client.getOutputStream().apply {
          write(handler(head))
          flush()
        }
      }
    }

    override fun close() = socket.close()
  }

  private fun ranged(body: ByteArray, status: String = "206 Partial Content", header: (Long, Long) -> String? = { a, b -> "bytes $a-$b/${body.size}" }) =
    Wire { head ->
      val match = Regex("bytes=(\\d+)-(\\d+)").matchEntire(head["range"] ?: "")
      if (match == null) {
        "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\nConnection: close\r\n\r\n".toByteArray()
      } else {
        val a = match.groupValues[1].toLong()
        val b = minOf(match.groupValues[2].toLong(), body.size - 1L)
        val slice = body.copyOfRange(a.toInt(), b.toInt() + 1)
        val range = header(a, b)?.let { "Content-Range: $it\r\n" } ?: ""
        ("HTTP/1.1 $status\r\n$range" + "Content-Length: ${slice.size}\r\nConnection: close\r\n\r\n").toByteArray() + slice
      }
    }

  private fun reader(port: Int, size: Int) = RangedReader("http://localhost:$port", FLEET, "reports/a.mp4", COOKIE, size.toLong())

  private fun bytes(count: Int) = ByteArray(count) { (it % 251).toByte() }

  @Test
  fun aRangeIsFetchedWithTheCookieOnTheRouteAndCachedPerBlock() {
    val body = bytes(STREAM_BLOCK * 2 + 100)
    ranged(body).use { rig ->
      val reader = reader(rig.port, body.size)
      val out = ByteArray(1000)
      assertEquals(1000, reader.read(10, out, 0, 1000))
      assertEquals(body.copyOfRange(10, 1010).toList(), out.toList())
      assertEquals(1000, reader.read(2000, out, 0, 1000))
      assertEquals(1, rig.hits.size)
      assertEquals("GET /$FLEET/api/v1/doc/reports/a.mp4?raw HTTP/1.1", rig.hits[0]["line"])
      assertEquals(COOKIE, rig.hits[0]["cookie"])
      assertEquals("identity", rig.hits[0]["accept-encoding"])
      assertEquals("close", rig.hits[0]["connection"])
      assertEquals("bytes=0-${STREAM_BLOCK - 1}", rig.hits[0]["range"])
      assertEquals(100, reader.read(STREAM_BLOCK * 2L, out, 0, 1000))
      assertEquals(2, rig.hits.size)
      assertEquals("bytes=${STREAM_BLOCK * 2}-${body.size - 1}", rig.hits[1]["range"])
      assertEquals(-1, reader.read(body.size.toLong(), out, 0, 10))
      assertEquals(2, rig.hits.size)
    }
  }

  @Test
  fun aWholeBodyAnswerOrAWrongRangeIsRefusedAndRemembered() {
    val body = bytes(5000)
    for ((status, header) in listOf<Pair<String, (Long, Long) -> String?>>(
      "200 OK" to { a, b -> "bytes $a-$b/5000" },
      "206 Partial Content" to { _, _ -> null },
      "206 Partial Content" to { _, _ -> "bytes 1-4999/5000" }
    )) {
      ranged(body, status, header).use { rig ->
        val reader = reader(rig.port, body.size)
        try {
          reader.read(0, ByteArray(10), 0, 10)
          throw AssertionError("expected a refusal for $status")
        } catch (_: IOException) {}
        assertNotNull(reader.broke)
      }
    }
  }

  @Test
  fun aRedirectToAnotherHostIsNeverFollowedAndTheCookieNeverLeaves() {
    ranged(bytes(100)).use { other ->
      Wire { "HTTP/1.1 302 Found\r\nLocation: http://localhost:${other.port}/stolen\r\nContent-Length: 0\r\nConnection: close\r\n\r\n".toByteArray() }.use { first ->
        val reader = reader(first.port, 100)
        try {
          reader.read(0, ByteArray(10), 0, 10)
          throw AssertionError("expected a refusal")
        } catch (_: IOException) {}
        assertEquals(1, first.hits.size)
        assertEquals(0, other.hits.size)
      }
    }
  }

  @Test
  fun everyConnectionTargetsLocalhostAndTheCookieGoesNowhereElse() {
    val seen = CopyOnWriteArrayList<URL>()
    val reader =
      RangedReader("http://localhost:4242", FLEET, "reports/a.mp4", COOKIE, 10) { url ->
        seen += url
        throw IOException("stop")
      }
    try {
      reader.read(0, ByteArray(1), 0, 1)
    } catch (_: IOException) {}
    assertEquals(listOf("localhost"), seen.map { it.host })
    assertEquals(listOf(4242), seen.map { it.port })
    for (base in listOf(
      "http://example.com:4242",
      "http://127.0.0.1:4242",
      "https://localhost:4242",
      "http://user@localhost:4242",
      "http://localhost.evil.test:4242",
      "http://localhost:0"
    )) {
      val touched = CopyOnWriteArrayList<URL>()
      try {
        RangedReader(base, FLEET, "reports/a.mp4", COOKIE, 10) { url ->
          touched += url
          throw IOException("must not connect")
        }
        throw AssertionError("expected a refusal for $base")
      } catch (_: IOException) {}
      assertTrue(base, touched.isEmpty())
    }
  }

  @Test
  fun aClosedReaderDropsItsBlockAndRefusesMoreReads() {
    val body = bytes(5000)
    ranged(body).use { rig ->
      val reader = reader(rig.port, body.size)
      reader.read(0, ByteArray(10), 0, 10)
      reader.close()
      try {
        reader.read(0, ByteArray(10), 0, 10)
        throw AssertionError("expected a refusal")
      } catch (_: IOException) {}
      assertEquals(1, rig.hits.size)
    }
  }

  @Test
  fun closeDoesNotWaitForAStalledFetchAndTheReadFailsCleanly() {
    val held = CopyOnWriteArrayList<Socket>()
    val accepted = CountDownLatch(1)
    val silent = ServerSocket(0, 8, InetAddress.getLoopbackAddress())
    val acceptor =
      Thread {
        try {
          held += silent.accept()
          accepted.countDown()
        } catch (_: IOException) {}
      }
    acceptor.isDaemon = true
    acceptor.start()
    try {
      val reader = reader(silent.localPort, 1000)
      val failure = CopyOnWriteArrayList<Throwable>()
      val worker =
        Thread {
          try {
            reader.read(0, ByteArray(10), 0, 10)
          } catch (error: IOException) {
            failure += error
          }
        }
      worker.isDaemon = true
      worker.start()
      assertTrue(accepted.await(5, TimeUnit.SECONDS))
      val closer = Thread { reader.close() }
      closer.isDaemon = true
      closer.start()
      closer.join(2000)
      assertFalse("close waited for the fetch", closer.isAlive)
      worker.join(5000)
      assertFalse("the stalled read never ended", worker.isAlive)
      assertEquals(1, failure.size)
      try {
        reader.read(0, ByteArray(10), 0, 10)
        throw AssertionError("expected a refusal")
      } catch (_: IOException) {}
    } finally {
      silent.close()
      held.forEach { it.close() }
    }
  }

  @Test
  fun readsOutsideTheFileAnswerEndOfFileWithoutAFetch() {
    val reader = RangedReader("http://localhost:4242", FLEET, "reports/a.mp4", COOKIE, 10) { throw IOException("no") }
    assertEquals(-1, reader.read(10, ByteArray(1), 0, 1))
    assertEquals(-1, reader.read(-1, ByteArray(1), 0, 1))
    assertNull(reader.broke)
  }

  private class Scripted(
    url: URL,
    private val body: ByteArray,
    private val start: Long,
    private val onResponse: () -> Unit = {},
    private val onRead: () -> Unit = {}
  ) : HttpURLConnection(url) {
    var responses = 0

    override fun connect() {}

    override fun disconnect() {}

    override fun usingProxy() = false

    override fun getResponseCode(): Int {
      responses++
      onResponse()
      return 206
    }

    override fun getHeaderField(name: String?) =
      if (name == "Content-Range") "bytes $start-${start + body.size - 1}/${body.size}" else null

    override fun getInputStream(): InputStream =
      object : FilterInputStream(ByteArrayInputStream(body)) {
        override fun read(buffer: ByteArray, offset: Int, length: Int): Int {
          onRead()
          return super.read(buffer, offset, length)
        }
      }
  }

  private fun scripted(
    size: Int,
    onResponse: (RangedReader) -> Unit = {},
    onRead: (RangedReader) -> Unit = {},
    onConnect: (RangedReader) -> Unit = {}
  ): Triple<RangedReader, CopyOnWriteArrayList<Scripted>, () -> Int> {
    val made = CopyOnWriteArrayList<Scripted>()
    val holder = arrayOfNulls<RangedReader>(1)
    val reader =
      RangedReader("http://localhost:1", FLEET, "reports/a.mp4", COOKIE, size.toLong()) { url ->
        holder[0]?.let(onConnect)
        Scripted(url, ByteArray(size), 0, { onResponse(holder[0]!!) }, { onRead(holder[0]!!) }).also { made += it }
      }
    holder[0] = reader
    return Triple(reader, made) { made.size }
  }

  private fun refused(reader: RangedReader) {
    try {
      reader.read(0, ByteArray(10), 0, 10)
      throw AssertionError("expected a refusal")
    } catch (_: IOException) {}
  }

  @Test
  fun aReadAfterCloseOpensNoConnection() {
    val (reader, made, count) = scripted(100)
    reader.close()
    refused(reader)
    assertEquals(0, count())
    assertTrue(made.isEmpty())
  }

  @Test
  fun aCloseDuringTheConnectSendsNoRequestAndFailsTheRead() {
    val (reader, made, _) = scripted(100, onConnect = { it.close() })
    refused(reader)
    assertEquals(1, made.size)
    assertEquals(0, made[0].responses)
    assertNotNull(reader.broke)
  }

  @Test
  fun aCloseDuringTheResponseFailsTheReadAndKeepsNothing() {
    val (reader, _, _) = scripted(100, onResponse = { it.close() })
    refused(reader)
    assertNotNull(reader.broke)
    assertBlockDropped(reader)
  }

  @Test
  fun aCloseDuringTheStreamFailsTheReadAndKeepsNothing() {
    val (reader, _, _) = scripted(100, onRead = { it.close() })
    refused(reader)
    assertNotNull(reader.broke)
    assertBlockDropped(reader)
  }

  @Test
  fun closingDropsTheCachedBlockAndItsPosition() {
    val (reader, _, count) = scripted(100)
    assertEquals(10, reader.read(0, ByteArray(10), 0, 10))
    assertEquals(1, count())
    assertEquals(100, blockSize(reader))
    reader.close()
    assertBlockDropped(reader)
  }

  private fun field(reader: RangedReader, name: String) =
    RangedReader::class.java.getDeclaredField(name).apply { isAccessible = true }.get(reader)

  private fun blockSize(reader: RangedReader) = (field(reader, "block") as ByteArray).size

  private fun assertBlockDropped(reader: RangedReader) {
    assertEquals(0, blockSize(reader))
    assertEquals(-1L, field(reader, "from"))
  }

  @Test
  fun closeBeforeReleasingThePlayerSoAStalledFetchEndsFirst() {
    val order = mutableListOf<String>()
    val reader = Closeable { order += "reader" }
    releaseStream(reader) { order += "player" }
    assertEquals(listOf("reader", "player"), order)
  }

  @Test
  fun theVideoSurfaceKeepsTheScreenOnWhilePlaying() {
    val source = File("src/main/java/io/github/atqamz/handcompanion/DocVideo.kt").readText()
    val surface = source.substringAfter("fun VideoSurface(")
    assertTrue(surface.contains("update = { it.keepScreenOn = playing }"))
  }
}
