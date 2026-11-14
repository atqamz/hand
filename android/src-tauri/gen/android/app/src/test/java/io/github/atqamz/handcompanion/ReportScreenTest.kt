package io.github.atqamz.handcompanion

import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Rule
import org.junit.Test

private const val FLEET = "f000000000003"

private val task =
  TaskInfo(ref = "t41", title = "Rename the session cookie", project = "hand", status = "active")

private const val BODY =
  "The session cookie is now `hand_board_<fleet id>` everywhere. The old name is still read " +
    "for one release and never written.\n\n" +
    "- 3 files changed, 2 tests added\n" +
    "- `go test -race ./...` passes\n\n" +
    "```\n$ go test ./internal/board -run Cookie\nok  internal/board  0.412s\n```\n\n" +
    "> The relay page keeps showing the old name until it reads the new cookie.\n\n" +
    "| day | online |\n|---|---:|\n| Mon | 314 |\n| Tue | 1.2k |\n\n" +
    "Not done: the relay page still shows the old name in one screenshot."

private val delivered =
  listOf(
    TaskDeliverable("https://github.com/example/hand/pull/1243", "PR"),
    TaskDeliverable("docs/board.md", "file", "/doc/docs/board.md")
  )

private fun report(acked: Boolean = false) =
  TaskReport(
    ref = "r91",
    task = "t41",
    attempt = "a63",
    status = "done",
    summary = "Cookie rename: done",
    body = BODY,
    createdAt = "2026-11-11T11:48:00Z",
    ackedAt = if (acked) "2026-11-11T12:00:00Z" else "",
    ackedBy = if (acked) "phone (Pixel 8)" else ""
  )

private fun detail(body: String = BODY, acked: Boolean = false) =
  ReportDetail(report(acked).copy(body = body), task, delivered)

private fun state(
  detail: ReportDetail? = detail(),
  loading: Boolean = false,
  failed: Boolean = false,
  refused: String? = null,
  marking: Boolean = false,
  error: String? = null
) = ReportState(detail, loading, failed, refused, false, marking, error)

class ReportScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 3400))

  private fun shot(dark: Boolean, shown: ReportState) =
    paparazzi.snapshot {
      HandTheme(dark) {
        ReportScreen(FLEET, "r91", shown, {}, {}, {}, {}, {})
      }
    }

  @Test fun unreadLight() = shot(false, state())

  @Test fun unreadDark() = shot(true, state())

  @Test fun readLight() = shot(false, state(detail(acked = true)))

  @Test fun readDark() = shot(true, state(detail(acked = true)))

  @Test fun longBodyLight() =
    shot(
      false,
      state(
        detail(
          body = BODY + "\n\n" + (1..8).joinToString("\n\n") {
            "Paragraph $it keeps the report long enough to scroll under the docked action. " +
              "It carries `code`, **strong** and a link to https://example.com/x."
          }
        )
      )
    )

  @Test fun longBodyDark() =
    shot(
      true,
      state(
        detail(
          body = BODY + "\n\n" + (1..8).joinToString("\n\n") {
            "Paragraph $it keeps the report long enough to scroll under the docked action. " +
              "It carries `code`, **strong** and a link to https://example.com/x."
          }
        )
      )
    )

  @Test fun markingLight() = shot(false, state(marking = true))

  @Test fun markingDark() = shot(true, state(marking = true))

  @Test fun refusedMarkReadLight() =
    shot(false, state(error = "r91 is already read."))

  @Test fun refusedMarkReadDark() =
    shot(true, state(error = "r91 is already read."))

  @Test fun loadingLight() = shot(false, ReportState())

  @Test fun loadingDark() = shot(true, ReportState())

  @Test fun failedLight() = shot(false, state(detail = null, failed = true))

  @Test fun failedDark() = shot(true, state(detail = null, failed = true))

  @Test fun refusedLight() = shot(false, state(detail = null, refused = "not found: report r99"))

  @Test fun refusedDark() = shot(true, state(detail = null, refused = "not found: report r99"))
}
