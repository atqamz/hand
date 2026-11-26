package io.github.atqamz.handcompanion

import android.graphics.Bitmap
import java.io.File
import java.io.IOException
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

private const val FLEET = "f000000000007"

private fun mediaNode(kind: String, size: Long, name: String) =
  Raw(
    200,
    """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":{"path":"reports/$name","name":"$name","kind":"$kind","text":false,"size":$size}}"""
  )

private class MediaBoard(var answer: () -> Raw) : Transport {
  override fun request(fleet: String, post: Boolean, path: String, csrf: String?, body: String?) = answer()
}

private class FakePlayer(override val duration: Int = 60_000) : AudioPlayer {
  override var position = 0
  var released = false
  var running = false
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

  override fun release() {
    released = true
  }
}

private class FakeAudio : AudioOpener {
  val players = CopyOnWriteArrayList<FakePlayer>()
  var broken = false
  var events: AudioEvents? = null

  override fun open(bytes: ByteArray, events: AudioEvents): AudioPlayer {
    if (broken) throw IOException("no codec")
    this.events = events
    return FakePlayer().also { players += it }
  }
}

private class FakePages(override val count: Int) : PdfPages {
  var closed = false
  val asked = mutableListOf<Pair<Int, Int>>()

  override fun render(index: Int, width: Int): Bitmap? {
    asked += index to width
    return null
  }

  override fun close() {
    closed = true
  }
}

private class FakePdfs(var pages: Int = 3) : PdfOpener {
  val opened = CopyOnWriteArrayList<FakePages>()
  var failure: Exception? = null

  override fun open(bytes: ByteArray): PdfPages {
    failure?.let { throw it }
    return FakePages(pages).also { opened += it }
  }
}

@OptIn(ExperimentalCoroutinesApi::class)
class DocMediaTest {
  @get:Rule val folder = TemporaryFolder()

  @Before fun main() = Dispatchers.setMain(Dispatchers.Unconfined)

  @After fun reset() = Dispatchers.resetMain()

  private val calls = CopyOnWriteArrayList<String>()
  private val audio = FakeAudio()
  private val pdfs = FakePdfs()

  private fun model(
    board: MediaBoard,
    path: String,
    bytes: ByteArray? = byteArrayOf(1, 2, 3),
    wait: () -> Unit = {}
  ) =
    DocModel(
      FLEET,
      path,
      board,
      files =
        FileSource { fleet, route ->
          calls += "$fleet $route"
          wait()
          bytes
        },
      audioOpener = audio,
      pdfOpener = pdfs
    )

  private fun song(size: Long = 4000) = MediaBoard { mediaNode("audio", size, "song one.mp3") }

  private fun paper(size: Long = 4000) = MediaBoard { mediaNode("pdf", size, "paper.pdf") }

  @Test
  fun audioIsReadFromTheFleetByItsEncodedPathAndOpened() = runBlocking {
    val model = model(song(), "reports/song one.mp3")
    model.load()
    assertEquals(listOf("$FLEET /api/v1/doc/reports/song%20one.mp3?raw"), calls.toList())
    assertEquals(AudioView(duration = 60_000), model.state.value.audio)
    assertEquals(DocBody.Audio, docBody(model.state.value))
  }

  @Test
  fun audioAtTheLimitPlaysAndOneByteOverIsRefusedWithoutAFetch() = runBlocking {
    model(song(MEDIA_LIMIT), "reports/a.mp3").also { it.load() }
    assertEquals(1, audio.players.size)
    calls.clear()
    val over = model(song(MEDIA_LIMIT + 1), "reports/a.mp3")
    over.load()
    assertTrue(calls.isEmpty())
    assertEquals(1, audio.players.size)
    assertEquals(DocBody.MediaNotice, docBody(over.state.value))
    assertTrue(over.state.value.notice!!.detail.contains("15.0 MB"))
  }

  @Test
  fun anAnswerLargerThanTheLimitIsRefusedWhateverTheMetaSaid() = runBlocking {
    val model = model(song(10), "reports/a.mp3", ByteArray((MEDIA_LIMIT + 1).toInt()))
    model.load()
    assertEquals(0, audio.players.size)
    assertEquals(DocBody.MediaNotice, docBody(model.state.value))
  }

  @Test
  fun aFormatThePlatformCannotPlayIsANotice() = runBlocking {
    audio.broken = true
    val model = model(song(), "reports/a.mp3")
    model.load()
    assertEquals("This audio can't be played here.", model.state.value.notice?.title)
    assertNull(model.state.value.audio)
  }

  @Test
  fun anEmptyOrMissingAnswerIsAProblemWithRetry() = runBlocking {
    val model = model(song(), "reports/a.mp3", null)
    model.load()
    assertEquals("The audio could not be loaded.", model.state.value.problem)
    assertEquals(DocBody.Failed, docBody(model.state.value))
  }

  @Test
  fun playPauseSeekAndTickMoveThePosition() = runBlocking {
    val model = model(song(), "reports/a.mp3")
    model.load()
    val player = audio.players[0]
    model.start()
    assertTrue(player.running)
    assertTrue(model.state.value.audio!!.playing)
    player.position = 4000
    model.tick()
    assertEquals(4000, model.state.value.audio!!.position)
    model.pause()
    assertFalse(player.running)
    assertFalse(model.state.value.audio!!.playing)
    model.seek(9000)
    assertEquals(9000, player.position)
    assertEquals(9000, model.state.value.audio!!.position)
  }

  @Test
  fun aPausedPlayerIsNotTicked() = runBlocking {
    val model = model(song(), "reports/a.mp3")
    model.load()
    audio.players[0].position = 5000
    model.tick()
    assertEquals(0, model.state.value.audio!!.position)
  }

  @Test
  fun leavingTheScreenReleasesThePlayerAndPlayingAgainReopensAtThePosition() = runBlocking {
    val model = model(song(), "reports/a.mp3")
    model.load()
    model.start()
    audio.players[0].position = 7000
    model.release()
    assertTrue(audio.players[0].released)
    assertEquals(AudioView(false, 7000, 60_000), model.state.value.audio)
    model.start()
    assertEquals(2, audio.players.size)
    assertEquals(listOf(7000), audio.players[1].seeks)
    assertTrue(audio.players[1].running)
  }

  @Test
  fun clearingTheModelReleasesThePlayerAndTheBytes() = runBlocking {
    val model = model(song(), "reports/a.mp3")
    model.load()
    model.onCleared()
    assertTrue(audio.players[0].released)
    model.start()
    assertEquals(1, audio.players.size)
  }

  @Test
  fun loadingAgainReleasesTheEarlierPlayer() = runBlocking {
    val model = model(song(), "reports/a.mp3")
    model.load()
    model.load()
    assertTrue(audio.players[0].released)
    assertFalse(audio.players[1].released)
  }

  @Test
  fun aFinishedTrackStartsFromTheBeginning() = runBlocking {
    val model = model(song(), "reports/a.mp3")
    model.load()
    model.start()
    audio.events!!.ended()
    assertEquals(AudioView(false, 60_000, 60_000), model.state.value.audio)
    model.start()
    assertEquals(0, model.state.value.audio!!.position)
    assertTrue(model.state.value.audio!!.playing)
  }

  @Test
  fun aPlaybackErrorReleasesThePlayerAndALaterStartClearsIt() = runBlocking {
    val model = model(song(), "reports/a.mp3")
    model.load()
    model.start()
    audio.events!!.failed()
    assertTrue(audio.players[0].released)
    assertEquals("The audio stopped with an error.", model.state.value.audio!!.error)
    assertFalse(model.state.value.audio!!.playing)
    model.start()
    assertNull(model.state.value.audio!!.error)
    assertTrue(model.state.value.audio!!.playing)
  }

  @Test
  fun aFailedReopenShowsAnErrorAndALaterReopenClearsIt() = runBlocking {
    val model = model(song(), "reports/a.mp3")
    model.load()
    model.release()
    audio.broken = true
    model.start()
    assertEquals("The audio could not be played.", model.state.value.audio!!.error)
    audio.broken = false
    model.start()
    assertNull(model.state.value.audio!!.error)
  }

  @Test
  fun aLoadThatIsOverTakenReleasesWhatItOpened() {
    val entered = CountDownLatch(1)
    val go = CountDownLatch(1)
    var first = true
    val board = MediaBoard { if (first) mediaNode("audio", 10, "a.mp3") else mediaNode("folder", 0, "d") }
    val model =
      model(board, "reports/a.mp3") {
        if (first) {
          first = false
          entered.countDown()
          go.await(5, TimeUnit.SECONDS)
        }
      }
    val worker = Thread { runBlocking { model.load() } }
    worker.start()
    assertTrue(entered.await(5, TimeUnit.SECONDS))
    runBlocking { model.load() }
    go.countDown()
    worker.join(5000)
    assertEquals(1, audio.players.size)
    assertTrue(audio.players[0].released)
    assertEquals("folder", model.state.value.node?.kind)
    assertNull(model.state.value.audio)
  }

  @Test
  fun clearingTheModelDuringALoadReleasesWhatTheLoadOpens() {
    for (kind in listOf("audio", "pdf")) {
      val entered = CountDownLatch(1)
      val go = CountDownLatch(1)
      val model =
        model(MediaBoard { mediaNode(kind, 10, "a.$kind") }, "reports/a.$kind") {
          entered.countDown()
          go.await(5, TimeUnit.SECONDS)
        }
      val worker = Thread { runBlocking { model.load() } }
      worker.start()
      assertTrue(entered.await(5, TimeUnit.SECONDS))
      model.onCleared()
      go.countDown()
      worker.join(5000)
      if (kind == "audio") assertTrue(audio.players.all { it.released }) else assertTrue(pdfs.opened.all { it.closed })
    }
    assertEquals(1, audio.players.size)
    assertEquals(1, pdfs.opened.size)
  }

  @Test
  fun aPdfOverTheLimitByOneByteIsRefusedAndAtTheLimitOpens() = runBlocking {
    model(paper(MEDIA_LIMIT), "reports/paper.pdf").also { it.load() }
    assertEquals(1, pdfs.opened.size)
    val over = model(paper(MEDIA_LIMIT + 1), "reports/paper.pdf")
    over.load()
    assertEquals(1, pdfs.opened.size)
    assertEquals(DocBody.MediaNotice, docBody(over.state.value))
    assertEquals("This PDF is too large to show here.", over.state.value.notice?.title)
    assertTrue(over.state.value.notice!!.detail.contains("15.0 MB"))
  }

  @Test
  fun aPdfShowsItsPageCountAndRendersByIndexAndWidth() = runBlocking {
    val model = model(paper(), "reports/paper.pdf")
    model.load()
    assertEquals(3, model.state.value.pdfPages)
    assertEquals(DocBody.Pdf, docBody(model.state.value))
    model.render(2, 900)
    assertEquals(listOf(2 to 900), pdfs.opened[0].asked)
  }

  @Test
  fun aProtectedPdfIsRefusedWithASentence() = runBlocking {
    pdfs.failure = PdfRefused()
    val model = model(paper(), "reports/paper.pdf")
    model.load()
    assertEquals("This PDF is protected, so it can't be shown here.", model.state.value.notice?.title)
    assertEquals(DocBody.MediaNotice, docBody(model.state.value))
  }

  @Test
  fun aBrokenPdfAndAPdfWithoutPagesAreProblems() = runBlocking {
    pdfs.failure = IOException("bad")
    val broken = model(paper(), "reports/paper.pdf")
    broken.load()
    assertEquals("The PDF could not be opened.", broken.state.value.problem)
    pdfs.failure = null
    pdfs.pages = 0
    val empty = model(paper(), "reports/paper.pdf")
    empty.load()
    assertTrue(pdfs.opened[0].closed)
    assertEquals(DocBody.Failed, docBody(empty.state.value))
  }

  @Test
  fun aLaterSuccessClearsAPdfProblem() = runBlocking {
    pdfs.failure = IOException("bad")
    val model = model(paper(), "reports/paper.pdf")
    model.load()
    pdfs.failure = null
    model.load()
    assertNull(model.state.value.problem)
    assertEquals(3, model.state.value.pdfPages)
  }

  @Test
  fun leavingAPdfClosesItAndLoadingAgainClosesTheEarlierOne() = runBlocking {
    val model = model(paper(), "reports/paper.pdf")
    model.load()
    model.load()
    assertTrue(pdfs.opened[0].closed)
    assertFalse(pdfs.opened[1].closed)
    model.onCleared()
    assertTrue(pdfs.opened[1].closed)
    assertNull(model.render(0, 100))
  }

  @Test
  fun aPdfLoadThatIsOverTakenIsClosed() {
    val entered = CountDownLatch(1)
    val go = CountDownLatch(1)
    var first = true
    val board = MediaBoard { if (first) mediaNode("pdf", 10, "p.pdf") else mediaNode("folder", 0, "d") }
    val model =
      model(board, "reports/p.pdf") {
        if (first) {
          first = false
          entered.countDown()
          go.await(5, TimeUnit.SECONDS)
        }
      }
    val worker = Thread { runBlocking { model.load() } }
    worker.start()
    assertTrue(entered.await(5, TimeUnit.SECONDS))
    runBlocking { model.load() }
    go.countDown()
    worker.join(5000)
    assertEquals(1, pdfs.opened.size)
    assertTrue(pdfs.opened[0].closed)
    assertEquals(0, model.state.value.pdfPages)
  }

  @Test
  fun theTempCopyHoldsTheBytesPrivatelyAndIsDeletedOnClose() {
    val dir = folder.newFolder("doc")
    val copy = TempCopy.make(dir, byteArrayOf(9, 8, 7))
    assertArrayEquals(byteArrayOf(9, 8, 7), copy.file.readBytes())
    assertTrue(copy.file.name.startsWith(TEMP_PREFIX) && copy.file.name.endsWith(TEMP_SUFFIX))
    copy.close()
    assertFalse(copy.file.exists())
    assertEquals(0, dir.listFiles()!!.size)
  }

  @Test
  fun sweepingRemovesLeftoverCopiesAndNothingElse() {
    val dir = folder.newFolder("doc")
    File(dir, "doc-1.pdf").writeText("x")
    File(dir, "doc-2.pdf").writeText("x")
    File(dir, "keep.txt").writeText("x")
    TempCopy.sweep(dir)
    assertEquals(listOf("keep.txt"), dir.list()!!.toList())
    TempCopy.sweep(File(dir, "missing"))
  }

  @Test
  fun sliceReadsStopAtTheEndAndNeverPastIt() {
    val bytes = byteArrayOf(1, 2, 3, 4, 5)
    val out = ByteArray(8)
    assertEquals(3, readSlice(bytes, 2, out, 0, 8))
    assertArrayEquals(byteArrayOf(3, 4, 5), out.copyOf(3))
    assertEquals(2, readSlice(bytes, 0, out, 6, 2))
    assertEquals(-1, readSlice(bytes, 5, out, 0, 8))
    assertEquals(-1, readSlice(bytes, -1, out, 0, 8))
    assertEquals(1, readSlice(bytes, 4, out, 0, 8))
  }

  @Test
  fun everyFetchGoesToTheOpenFleetOnly() = runBlocking {
    model(song(), "reports/a.mp3").load()
    model(paper(), "reports/p.pdf").load()
    assertTrue(calls.all { it.startsWith("$FLEET /api/v1/doc/") && it.endsWith("?raw") })
    assertNotNull(calls.firstOrNull())
  }
}
