package io.github.atqamz.handcompanion

import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import app.cash.paparazzi.DeviceConfig
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
      "reports/flow.pdf",
      DocState(node = DocNode("reports/flow.pdf", "flow.pdf", "pdf", false, 48211), loading = false)
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
      textState().copy(goLine = 5000, goProblem = "The file has fewer than 5000 lines."),
      DocTool.GoTo
    )

  @Test fun goToDark() =
    shot(
      true,
      "reports/export.log",
      textState().copy(goLine = 5000, goProblem = "The file has fewer than 5000 lines."),
      DocTool.GoTo
    )

  @Test fun wrapOffLight() = shot(false, "reports/export.log", textState().copy(wrap = false))

  @Test fun wrapOffDark() = shot(true, "reports/export.log", textState().copy(wrap = false))

  @Test fun copyFailedLight() =
    shot(false, "reports/export.log", textState().copy(copyProblem = "The file is too large to copy here."))
}
