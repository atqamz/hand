package io.github.atqamz.handcompanion

import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import app.cash.paparazzi.DeviceConfig
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import app.cash.paparazzi.Paparazzi
import org.junit.Rule
import java.io.ByteArrayOutputStream
import org.junit.Test

private const val FLEET = "f000000000007"

private const val STANDUP =
  "# Standup\n\n" +
    "**Shipped**\nExport job retried clean\nManifest written last\nBackfill at 30 days\n---\n" +
    "**Next**\nRegion split\nAlert on stale manifest\n---\n" +
    "**Blocked**\nNothing\n\n" +
    "- the plan is in `reports/t1/plan.md`\n- owner: **ops**\n\n" +
    "> Region split waits for the manifest change.\n"

private fun markdownState(path: String = "reports/standup.md") =
  DocState(
    node = DocNode(path, "standup.md", "markdown", true, 410),
    blocks = markdown(STANDUP),
    loading = false
  )

private val textLines =
  listOf(
    "2026-11-20 08:15:01 start export",
    "2026-11-20 08:15:02 manifest\twritten",
    "2026-11-20 08:15:03 \u001b[31mred\u001b[0m ends",
    "2026-11-20 08:15:04 a very long line that wraps because the screen is narrow and the log keeps going on",
    "2026-11-20 08:15:05 done"
  ).map(::docLine)

private fun textState(next: Int? = 6, moreProblem: String? = null) =
  DocState(
    node = DocNode("reports/export.log", "export.log", "text", true, 1800),
    lines = textLines,
    next = next,
    moreProblem = moreProblem,
    loading = false
  )

private val folderState =
  DocState(
    node =
      DocNode(
        "reports/t1",
        "t1",
        "folder",
        entries =
          listOf(
            DocEntry("notes", "reports/t1/notes", "folder", "DIR", true),
            DocEntry("design.md", "reports/t1/design.md", "file", "MD", false, 303),
            DocEntry("flow.png", "reports/t1/flow.png", "file", "PNG", false, 48211),
            DocEntry("export.log", "reports/t1/export.log", "file", "LOG", false, 1800),
            DocEntry("outside", "reports/t1/outside", "link", "LINK", false, null, "leads outside the fleet folder")
          ),
        page = 1,
        pages = 2,
        total = 640
      ),
    loading = false
  )

private fun picture(): ByteArray {
  val bitmap = Bitmap.createBitmap(320, 200, Bitmap.Config.ARGB_8888)
  val canvas = Canvas(bitmap)
  canvas.drawColor(Color.rgb(236, 240, 244))
  val paint = Paint()
  paint.color = Color.rgb(46, 110, 180)
  canvas.drawRect(30f, 40f, 160f, 160f, paint)
  paint.color = Color.rgb(214, 120, 40)
  canvas.drawCircle(220f, 100f, 56f, paint)
  val out = ByteArrayOutputStream()
  bitmap.compress(Bitmap.CompressFormat.PNG, 100, out)
  return out.toByteArray()
}

private fun imageState(bytes: ByteArray?, size: Long = 48211, problem: String? = null) =
  DocState(
    node = DocNode("reports/flow.png", "flow.png", "image", false, size),
    image = bytes,
    problem = problem,
    loading = false
  )

private val findHits =
  textState().copy(find = DocFind("manifest", listOf(2), 0))

private fun audioState(audio: Playback) =
  DocState(
    node = DocNode("reports/standup.mp3", "standup.mp3", "audio", false, 2_400_000),
    audio = audio,
    loading = false
  )

private fun clipState(video: Playback) =
  DocState(
    node = DocNode("reports/demo.mp4", "demo.mp4", "video", false, 18_400_000),
    audio = video,
    loading = false
  )

private val pageNode = DocNode("reports/summary.html", "summary.html", "html", true, 1_240)

private fun pageState(rendered: Boolean = true, html: String? = "<h1>Summary</h1>", notice: DocNotice? = null) =
  DocState(
    node = pageNode,
    lines = listOf("<h1>Summary</h1>", "<p>Export job retried clean.</p>", "<p>Manifest written last.</p>"),
    html = html,
    rendered = rendered,
    notice = notice,
    loading = false
  )

private val framePlaceholder: @Composable (Boolean, Modifier) -> Unit = { _, modifier ->
  Box(modifier.background(Color.rgb(32, 52, 84).toComposeColor()))
}

private val pagePlaceholder: @Composable (String, () -> Unit, Modifier) -> Unit = { _, _, modifier ->
  Column(modifier.background(Color.WHITE.toComposeColor()).padding(16.dp)) {
    Text("Summary", color = Color.BLACK.toComposeColor())
    Text("Export job retried clean.", color = Color.BLACK.toComposeColor())
    Text("Manifest written last.", color = Color.BLACK.toComposeColor())
  }
}

private fun Int.toComposeColor() = androidx.compose.ui.graphics.Color(this)

private fun pdfState(pages: Int = 3) =
  DocState(
    node = DocNode("reports/paper.pdf", "paper.pdf", "pdf", false, 310_000),
    pdfPages = pages,
    loading = false
  )

private fun pdfNode(size: Long) = DocNode("reports/paper.pdf", "paper.pdf", "pdf", false, size)

private fun sheet(width: Int, index: Int): Bitmap {
  val bitmap = Bitmap.createBitmap(width, width * 4 / 3, Bitmap.Config.ARGB_8888)
  val canvas = Canvas(bitmap)
  canvas.drawColor(Color.WHITE)
  val paint = Paint()
  paint.color = Color.rgb(60, 70, 90)
  for (row in 0 until 14) canvas.drawRect(width * 0.1f, width * (0.12f + row * 0.07f), width * (0.9f - (row % 3) * 0.12f), width * (0.15f + row * 0.07f), paint)
  paint.color = Color.rgb(46, 110, 180)
  canvas.drawRect(width * 0.1f, width * 0.04f, width * (0.3f + index * 0.1f), width * 0.08f, paint)
  return bitmap
}

private fun tall(width: Int, index: Int): Bitmap {
  val bitmap = Bitmap.createBitmap(8, 8192, Bitmap.Config.ARGB_8888)
  val canvas = Canvas(bitmap)
  canvas.drawColor(Color.WHITE)
  val paint = Paint()
  paint.color = Color.rgb(46, 110, 180)
  canvas.drawRect(0f, 0f, 8f, 600f, paint)
  return bitmap
}

private fun wide(width: Int, index: Int): Bitmap {
  val bitmap = Bitmap.createBitmap(width, 1, Bitmap.Config.ARGB_8888)
  bitmap.eraseColor(Color.rgb(46, 110, 180))
  return bitmap
}

class DocScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 2400))

  private fun shot(dark: Boolean, path: String, shown: DocState, seed: DocTool? = null) =
    paparazzi.snapshot {
      HandTheme(dark) { DocScreen(FLEET, path, shown, {}, {}, {}, {}, {}, DocTools(), seed) }
    }

  @Test fun markdownLight() = shot(false, "reports/standup.md", markdownState())

  @Test fun markdownDark() = shot(true, "reports/standup.md", markdownState())

  @Test fun textLight() = shot(false, "reports/export.log", textState())

  @Test fun textDark() = shot(true, "reports/export.log", textState())

  @Test fun textMoreFailedLight() =
    shot(false, "reports/export.log", textState(moreProblem = "The Board could not be reached."))

  @Test fun folderLight() = shot(false, "reports/t1", folderState)

  @Test fun folderDark() = shot(true, "reports/t1", folderState)

  @Test fun errorLight() =
    shot(false, "reports/standup.md", DocState(loading = false, problem = "The Board could not be reached."))

  @Test fun errorDark() =
    shot(true, "reports/standup.md", DocState(loading = false, problem = "The Board could not be reached."))

  @Test fun refusedLight() =
    shot(
      false,
      "reports/standup.md",
      DocState(loading = false, refused = "this file has more than one hard link, so the Board does not show it; copy it to show it")
    )

  @Test fun refusedDark() =
    shot(
      true,
      "reports/standup.md",
      DocState(loading = false, refused = "this file has more than one hard link, so the Board does not show it; copy it to show it")
    )

  @Test fun staleLight() =
    shot(false, "reports/standup.md", markdownState().copy(problem = "The Board could not be reached."))

  @Test fun tooOldLight() = shot(false, "reports/standup.md", DocState(loading = false, tooOld = true))

  @Test fun noticeLight() =
    shot(
      false,
      "reports/flow.bin",
      DocState(node = DocNode("reports/flow.bin", "flow.bin", "binary", false, 48211), loading = false)
    )

  @Test fun loadingLight() = shot(false, "reports/standup.md", DocState())

  @Test fun imageLight() = shot(false, "reports/flow.png", imageState(picture()))

  @Test fun imageDark() = shot(true, "reports/flow.png", imageState(picture()))

  @Test fun imageErrorLight() =
    shot(false, "reports/flow.png", imageState(null, problem = "The image could not be loaded."))

  @Test fun imageErrorDark() =
    shot(true, "reports/flow.png", imageState(null, problem = "The image could not be loaded."))

  @Test fun imageTooLargeLight() = shot(false, "reports/flow.png", imageState(null, IMAGE_LIMIT + 1))

  @Test fun imageTooLargeDark() = shot(true, "reports/flow.png", imageState(null, IMAGE_LIMIT + 1))

  @Test fun findHitsLight() = shot(false, "reports/export.log", findHits, DocTool.Find)

  @Test fun findHitsDark() = shot(true, "reports/export.log", findHits, DocTool.Find)

  @Test fun findNoneLight() =
    shot(false, "reports/export.log", textState().copy(find = DocFind("zzz")), DocTool.Find)

  @Test fun goToLight() =
    shot(
      false,
      "reports/export.log",
      textState().copy(goLine = 5000, goProblem = "line 5000 is past the end of this file"),
      DocTool.GoTo
    )

  @Test fun goToDark() =
    shot(
      true,
      "reports/export.log",
      textState().copy(goLine = 5000, goProblem = "line 5000 is past the end of this file"),
      DocTool.GoTo
    )

  @Test fun wrapOffLight() = shot(false, "reports/export.log", textState().copy(wrap = false))

  @Test fun wrapOffDark() = shot(true, "reports/export.log", textState().copy(wrap = false))

  @Test fun copyFailedLight() =
    shot(false, "reports/export.log", textState().copy(copyProblem = "The file is too large to copy here."))

  private fun media(dark: Boolean, path: String, shown: DocState, draw: (Int, Int) -> Bitmap = ::sheet) =
    paparazzi.snapshot {
      HandTheme(dark) {
        DocScreen(FLEET, path, shown, {}, {}, {}, {}, {}, DocTools(drawn = { index, width -> draw(width, index) }))
      }
    }

  @Test fun audioIdleLight() = media(false, "reports/standup.mp3", audioState(Playback(false, 0, 215_000)))

  @Test fun audioIdleDark() = media(true, "reports/standup.mp3", audioState(Playback(false, 0, 215_000)))

  @Test fun audioPlayingLight() = media(false, "reports/standup.mp3", audioState(Playback(true, 72_000, 215_000)))

  @Test fun audioPlayingDark() = media(true, "reports/standup.mp3", audioState(Playback(true, 72_000, 215_000)))

  @Test
  fun audioErrorLight() =
    media(false, "reports/standup.mp3", audioState(Playback(false, 72_000, 215_000, "The audio stopped with an error.")))

  @Test
  fun audioErrorDark() =
    media(true, "reports/standup.mp3", audioState(Playback(false, 72_000, 215_000, "The audio stopped with an error.")))

  @Test fun pdfPageLight() = media(false, "reports/paper.pdf", pdfState())

  @Test fun pdfPageDark() = media(true, "reports/paper.pdf", pdfState())

  @Test fun pdfTallPageLight() = media(false, "reports/paper.pdf", pdfState(1), ::tall)

  @Test fun pdfTallPageDark() = media(true, "reports/paper.pdf", pdfState(1), ::tall)

  @Test fun pdfWidePageLight() = media(false, "reports/paper.pdf", pdfState(1), ::wide)

  @Test fun pdfWidePageDark() = media(true, "reports/paper.pdf", pdfState(1), ::wide)

  @Test
  fun pdfTooLargeLight() =
    media(
      false,
      "reports/paper.pdf",
      DocState(
        node = pdfNode(21_000_000),
        notice = DocNotice("This PDF is too large to show here.", "pdf · 20.0 MB. The limit is 15.0 MB."),
        loading = false
      )
    )

  @Test
  fun pdfTooLargeDark() =
    media(
      true,
      "reports/paper.pdf",
      DocState(
        node = pdfNode(21_000_000),
        notice = DocNotice("This PDF is too large to show here.", "pdf · 20.0 MB. The limit is 15.0 MB."),
        loading = false
      )
    )

  @Test
  fun pdfErrorLight() =
    media(
      false,
      "reports/paper.pdf",
      DocState(node = pdfNode(310_000), problem = "The PDF could not be opened.", loading = false)
    )

  @Test
  fun pdfErrorDark() =
    media(
      true,
      "reports/paper.pdf",
      DocState(node = pdfNode(310_000), problem = "The PDF could not be opened.", loading = false)
    )

  @Test
  fun pdfProtectedLight() =
    media(
      false,
      "reports/paper.pdf",
      DocState(
        node = pdfNode(310_000),
        notice = DocNotice("This PDF is protected, so it can't be shown here.", "It needs a password."),
        loading = false
      )
    )

  private fun frames(dark: Boolean, path: String, shown: DocState) =
    paparazzi.snapshot {
      HandTheme(dark) {
        DocScreen(
          FLEET,
          path,
          shown,
          {},
          {},
          {},
          {},
          {},
          DocTools(video = framePlaceholder, html = pagePlaceholder)
        )
      }
    }

  @Test fun videoIdleLight() = frames(false, "reports/demo.mp4", clipState(Playback(false, 0, 95_000, null, 1280, 720)))

  @Test fun videoIdleDark() = frames(true, "reports/demo.mp4", clipState(Playback(false, 0, 95_000, null, 1280, 720)))

  @Test
  fun videoPlayingLight() = frames(false, "reports/demo.mp4", clipState(Playback(true, 41_000, 95_000, null, 1280, 720)))

  @Test
  fun videoPlayingDark() = frames(true, "reports/demo.mp4", clipState(Playback(true, 41_000, 95_000, null, 1280, 720)))

  @Test
  fun videoErrorLight() =
    frames(
      false,
      "reports/demo.mp4",
      clipState(Playback(false, 41_000, 95_000, "The video stopped with an error.", 1280, 720))
    )

  @Test
  fun videoErrorDark() =
    frames(
      true,
      "reports/demo.mp4",
      clipState(Playback(false, 41_000, 95_000, "The video stopped with an error.", 1280, 720))
    )

  @Test fun htmlRenderedLight() = frames(false, "reports/summary.html", pageState())

  @Test fun htmlRenderedDark() = frames(true, "reports/summary.html", pageState())

  @Test fun htmlSourceLight() = frames(false, "reports/summary.html", pageState(rendered = false))

  @Test fun htmlSourceDark() = frames(true, "reports/summary.html", pageState(rendered = false))

  @Test
  fun htmlGoneLight() =
    frames(
      false,
      "reports/summary.html",
      pageState(html = null, notice = DocNotice("The preview stopped working.", "The page closed the viewer. The source is shown instead."))
    )

  @Test
  fun htmlGoneDark() =
    frames(
      true,
      "reports/summary.html",
      pageState(html = null, notice = DocNotice("The preview stopped working.", "The page closed the viewer. The source is shown instead."))
    )

  @Test
  fun videoNoticeLight() =
    frames(
      false,
      "reports/demo.mp4",
      DocState(
        node = DocNode("reports/demo.mp4", "demo.mp4", "video", false, 18_400_000),
        notice = DocNotice("This video can't be played here.", "video · 17.5 MB"),
        loading = false
      )
    )
}
