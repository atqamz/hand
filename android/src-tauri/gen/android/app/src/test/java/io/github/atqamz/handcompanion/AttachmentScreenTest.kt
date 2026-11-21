package io.github.atqamz.handcompanion

import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.text.input.TextFieldValue
import java.io.ByteArrayOutputStream
import java.util.TimeZone
import java.util.concurrent.atomic.AtomicInteger
import java.util.zip.CRC32
import java.util.zip.Deflater
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

private const val FLEET = "f00000000e0aa"
private val UTC: TimeZone = TimeZone.getTimeZone("UTC")
private const val FIRST = "20261121-101500-1-before.png"
private const val SECOND = "20261121-101500-2-after.png"
private const val BROKEN = "20261121-101500-3-broken.png"

private fun png(width: Int, height: Int, background: Int, accent: Int): ByteArray {
  val bitmap = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888)
  val canvas = Canvas(bitmap)
  val paint = Paint()
  paint.color = background
  canvas.drawRect(0f, 0f, width.toFloat(), height.toFloat(), paint)
  paint.color = accent
  canvas.drawRect(width * 0.08f, height * 0.12f, width * 0.92f, height * 0.26f, paint)
  canvas.drawRect(width * 0.08f, height * 0.38f, width * 0.5f, height * 0.88f, paint)
  canvas.drawRect(width * 0.58f, height * 0.38f, width * 0.92f, height * 0.6f, paint)
  val out = ByteArrayOutputStream()
  bitmap.compress(Bitmap.CompressFormat.PNG, 100, out)
  return out.toByteArray()
}

private fun hugePng(side: Int): ByteArray {
  val out = ByteArrayOutputStream()
  fun chunk(type: String, data: ByteArray) {
    val head = type.toByteArray(Charsets.US_ASCII)
    val crc = CRC32().apply { update(head); update(data) }
    out.write(
      java.nio.ByteBuffer.allocate(4).putInt(data.size).array() + head + data +
        java.nio.ByteBuffer.allocate(4).putInt(crc.value.toInt()).array()
    )
  }
  out.write(byteArrayOf(0x89.toByte(), 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a))
  chunk(
    "IHDR",
    java.nio.ByteBuffer.allocate(13).putInt(side).putInt(side).put(1).put(0).put(0).put(0).put(0).array()
  )
  val row = ByteArray(1 + (side + 7) / 8)
  val deflater = Deflater()
  val packed = ByteArrayOutputStream()
  val buffer = ByteArray(8192)
  repeat(side) {
    deflater.setInput(row)
    while (!deflater.needsInput()) packed.write(buffer, 0, deflater.deflate(buffer))
  }
  deflater.finish()
  while (!deflater.finished()) packed.write(buffer, 0, deflater.deflate(buffer))
  chunk("IDAT", packed.toByteArray())
  chunk("IEND", ByteArray(0))
  return out.toByteArray()
}

private class Inbox(private val files: Map<String, ByteArray>) : FileSource {
  val asked = mutableListOf<Pair<String, String>>()

  override fun file(fleet: String, path: String): ByteArray? {
    synchronized(asked) { asked += fleet to path }
    return files[path.removePrefix("/api/v1/inbox/")]
  }
}

private fun at(minutes: Int) =
  "2026-11-02T${(10 + minutes / 60).toString().padStart(2, '0')}:${(minutes % 60).toString().padStart(2, '0')}:00Z"

private fun shot(n: Int, name: String, label: String, image: Boolean = true, missing: Boolean = false) =
  Shot(n = n, name = name, label = label, kind = if (image) "image" else "log", size = "48 KB", image = image, missing = missing)

private val entries =
  listOf(
    TimelineEntry(
      role = "operator",
      at = at(50),
      input = 9,
      delivery = "delivered",
      delivered = at(50),
      text = "The log is attached: [attachment 1]. The screenshot [attachment 2] is gone.",
      shots =
        listOf(
          shot(1, "20261121-101500-1-run.log", "run.log", image = false),
          shot(2, "20261121-101500-2-old.png", "old.png", missing = true)
        )
    ),
    TimelineEntry(
      role = "operator",
      at = at(40),
      input = 8,
      delivery = "delivered",
      delivered = at(40),
      text = "Look at [attachment 1], as you see there is misalignment. It should be like [attachment 2].\nThe first again: [attachment 1]",
      shots = listOf(shot(1, FIRST, "before.png"), shot(2, SECOND, "after.png"))
    ),
    TimelineEntry(
      role = "supervisor",
      at = at(30),
      no = 41,
      ref = "s1",
      text = "I will compare image 1 and image 2 and report back."
    )
  )

private val state =
  ChatState(
    page = pageKeys(entries),
    before = 0,
    to = entries.size,
    live = true,
    loading = false,
    now = epoch("2026-11-02T11:00:00Z")!!
  )

private val supervisor =
  Supervisor(
    ref = "s1",
    harness = "claude",
    model = "opus",
    effort = "high",
    status = "running",
    live = true,
    pending = 0,
    resumable = false
  )

class AttachmentScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 3700))

  private val inbox by lazy {
    Inbox(
      mapOf(
        FIRST to png(1200, 800, Color.rgb(246, 244, 238), Color.rgb(200, 90, 60)),
        SECOND to png(1200, 800, Color.rgb(246, 244, 238), Color.rgb(60, 120, 90))
      )
    )
  }

  private fun scene(dark: Boolean) {
    val loader = AttachmentLoader(inbox, Dispatchers.Unconfined)
    runBlocking {
      loader.image(FLEET, FIRST)
      loader.image(FLEET, SECOND)
    }
    paparazzi.snapshot {
      CompositionLocalProvider(LocalAttachments provides loader) {
        HandTheme(dark) {
          ChatScreen(
            FLEET,
            state,
            onOlder = {},
            onRetry = {},
            onRetryOlder = {},
            onScrolled = {},
            onTarget = {},
            zone = UTC,
            head = chatHead(supervisor, "idle"),
            input = remember { mutableStateOf(TextFieldValue("")) }
          )
        }
      }
    }
  }

  private fun cards(dark: Boolean) =
    paparazzi.snapshot {
      HandTheme(dark) {
        Column(
          Modifier.fillMaxSize().background(LocalHand.current.bg).padding(16.dp),
          verticalArrangement = Arrangement.spacedBy(12.dp)
        ) {
          val shot = shot(1, BROKEN, "broken.png")
          AttachedLoaded(shot, Loaded.Busy) {}
          AttachedLoaded(shot, Loaded.Failed) {}
        }
      }
    }

  @Test fun cardsLight() = cards(false)

  @Test fun cardsDark() = cards(true)

  @Test fun inlineLight() = scene(false)

  @Test fun inlineDark() = scene(true)

  @Test
  fun aBigPictureIsDecodedUnderTheBound() {
    val picture = decodeBounded(png(3000, 2000, Color.WHITE, Color.BLACK))!!
    assertTrue(picture.width.toLong() * picture.height <= MAX_PIXELS)
    assertEquals(3, picture.width * 2 / picture.height)
    val small = decodeBounded(png(200, 100, Color.WHITE, Color.BLACK))!!
    assertEquals(200 to 100, small.width to small.height)
  }

  @Test
  fun bytesThatAreNotAPictureAreNone() {
    assertNull(decodeBounded(ByteArray(0)))
    assertNull(decodeBounded("not a picture".toByteArray()))
  }

  @Test
  fun aSourceOverTheLimitIsNotDecoded() {
    assertNotNull(decodeBounded(hugePng(9000)))
    assertNull(decodeBounded(hugePng(11000)))
  }

  @Test
  fun aPictureIsAskedForByItsNameAndKept() = runBlocking {
    val loader = AttachmentLoader(inbox, Dispatchers.Unconfined)
    assertNotNull(loader.image(FLEET, FIRST))
    assertNotNull(loader.image(FLEET, FIRST))
    assertEquals(listOf(FLEET to "/api/v1/inbox/$FIRST"), inbox.asked)
  }

  @Test
  fun aNameIsEncodedAsOnePathSegment() = runBlocking {
    val loader = AttachmentLoader(inbox, Dispatchers.Unconfined)
    assertNull(loader.image(FLEET, "a b/../c?d"))
    assertEquals("/api/v1/inbox/a%20b%2F..%2Fc%3Fd", inbox.asked.single().second)
  }

  @Test
  fun aFailedFetchIsNotKept() = runBlocking {
    val loader = AttachmentLoader(inbox, Dispatchers.Unconfined)
    assertNull(loader.image(FLEET, BROKEN))
    assertNull(loader.image(FLEET, BROKEN))
    assertEquals(2, inbox.asked.size)
  }

  @Test
  fun aFleetsPictureIsNotAnotherFleets() = runBlocking {
    val loader = AttachmentLoader(inbox, Dispatchers.Unconfined)
    loader.image(FLEET, FIRST)
    loader.image("f11111111e0bb", FIRST)
    assertEquals(listOf(FLEET, "f11111111e0bb"), inbox.asked.map { it.first })
  }

  @Test
  fun atMostTwoFetchesRunAtOnce() = runBlocking {
    val running = AtomicInteger()
    val most = AtomicInteger()
    val source =
      FileSource { _, _ ->
        most.accumulateAndGet(running.incrementAndGet(), ::maxOf)
        Thread.sleep(30)
        running.decrementAndGet()
        null
      }
    val loader = AttachmentLoader(source, Dispatchers.IO)
    (1..6).map { async(Dispatchers.Default) { loader.image(FLEET, "n$it") } }.awaitAll()
    assertEquals(2, most.get())
  }

  @Test
  fun aPictureAskedForTwiceAtOnceIsFetchedOnceForEachFreeSlotOnly() = runBlocking {
    val source = FileSource { _, path -> Thread.sleep(60); inbox.file(FLEET, path) }
    val loader = AttachmentLoader(source, Dispatchers.IO)
    (1..4).map { async(Dispatchers.Default) { loader.image(FLEET, FIRST) } }.awaitAll()
    assertEquals(2, inbox.asked.size)
  }
}
