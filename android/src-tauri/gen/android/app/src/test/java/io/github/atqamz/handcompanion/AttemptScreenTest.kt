package io.github.atqamz.handcompanion

import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Rule
import org.junit.Test

private const val FLEET = "f000000000003"

private val attempt =
  TaskAttempt(
    ref = "a62",
    task = "t40",
    harness = "codex",
    model = "gpt-5",
    effort = "high",
    status = "running",
    state = "running",
    branch = "hand/t40-a62",
    createdAt = "2026-11-11T11:10:00Z"
  )

private val task =
  TaskInfo(ref = "t40", title = "Pin the luvus version in CI", project = "hand", status = "active")

private val brief =
  "t40: pin the luvus version in CI\n\n" +
    "Pin luvus in ci.yaml, the e2e image and install.sh to one version. " +
    "Fail CI when the three differ. Ask before choosing the rule.\n\n" +
    "Take the version the e2e image already pins, and leave the trailing comment that says why " +
    "the image follows the parser rather than the release."

private val report =
  TaskReport(
    ref = "r90",
    task = "t40",
    attempt = "a62",
    status = "progress",
    summary = "Three places install luvus",
    body = "Only ci.yaml floats; the e2e image and install.sh already pin 0.14.2. " +
      "I will pin ci.yaml and add a check that fails when the pins differ.",
    createdAt = "2026-11-11T11:31:00Z"
  )

private val turns =
  listOf(
    AttemptTurn("worker", "Three places install luvus: ci.yaml, the e2e image and install.sh. Only ci.yaml floats.", "2026-11-11T11:24:00Z"),
    AttemptTurn(
      "worker",
      "Reported r90. I am waiting for d12 before I choose the pin.",
      "2026-11-11T11:31:00Z"
    )
  )

private fun detail(
  state: String = "running",
  status: String = "running",
  transcript: AttemptTranscript =
    AttemptTranscript(
      shown = true,
      more = true,
      model = "Asked for gpt-5, answered by gpt-5-mini.",
      answered = "gpt-5",
      turns = turns
    ),
  unread: Boolean = true,
  moreReports: Boolean = false
) =
  AttemptDetail(
    attempt = attempt.copy(status = status, state = state),
    task = task,
    state = state,
    brief = brief,
    modelNote = "Asked for high effort; ran with medium.",
    duration = if (state == "running") "21m" else "21m 04s",
    reports =
      if (unread) listOf(report)
      else listOf(report.copy(ackedAt = "2026-11-11T12:00:00Z", ackedBy = "phone (Pixel 8)")),
    moreReports = moreReports,
    events =
      listOf(
        TaskEvent(30, "2026-11-11T11:31:00Z", "attempt.reported", "a62 reported r90", "t40"),
        TaskEvent(21, "2026-11-11T11:31:00Z", "decision.asked", "d12 asked", "t40"),
        TaskEvent(10, "2026-11-11T11:10:00Z", "attempt.launched", "codex high", "t40")
      ),
    moreEvents = true,
    delivered = listOf(TaskDeliverable("plan-t40.md", "file", "/doc/plan-t40.md")),
    transcript = transcript
  )

private fun state(
  detail: AttemptDetail? = detail(),
  loading: Boolean = false,
  failed: Boolean = false,
  refused: String? = null,
  briefOpen: Boolean = false,
  marking: Set<String> = emptySet()
) = AttemptState(detail, loading, failed, refused, false, marking, briefOpen = briefOpen)

class AttemptScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 3400))

  private fun shot(dark: Boolean, shown: AttemptState) =
    paparazzi.snapshot {
      HandTheme(dark) {
        AttemptScreen(FLEET, "a62", shown, {}, {}, {}, {}, {}, {})
      }
    }

  @Test fun fullLight() = shot(false, state())

  @Test fun fullDark() = shot(true, state())

  @Test fun briefShownLight() = shot(false, state(briefOpen = true))

  @Test fun briefShownDark() = shot(true, state(briefOpen = true))

  @Test
  fun finishedLight() =
    shot(
      false,
      state(
        detail(state = "idle", status = "done", transcript = AttemptTranscript(), unread = false)
      )
    )

  @Test
  fun finishedDark() =
    shot(
      true,
      state(
        detail(state = "idle", status = "done", transcript = AttemptTranscript(), unread = false)
      )
    )

  @Test
  fun queuedLight() =
    shot(
      false,
      state(
        detail(
          transcript =
            AttemptTranscript(
              shown = true,
              turns =
                listOf(
                  AttemptTurn("worker", "Pinning ci.yaml now.", "2026-11-11T11:26:00Z"),
                  AttemptTurn("operator", "Keep 0.14.x for CI only.", "2026-11-11T11:27:00Z", queued = true)
                )
            )
        )
      )
    )

  @Test
  fun queuedDark() =
    shot(
      true,
      state(
        detail(
          transcript =
            AttemptTranscript(
              shown = true,
              turns =
                listOf(
                  AttemptTurn("worker", "Pinning ci.yaml now.", "2026-11-11T11:26:00Z"),
                  AttemptTurn("operator", "Keep 0.14.x for CI only.", "2026-11-11T11:27:00Z", queued = true)
                )
            )
        )
      )
    )

  @Test fun markReadLight() = shot(false, state(marking = setOf("r90")))

  @Test fun markReadDark() = shot(true, state(marking = setOf("r90")))

  @Test fun loadingLight() = shot(false, AttemptState())

  @Test fun loadingDark() = shot(true, AttemptState())

  @Test fun failedLight() = shot(false, state(detail = null, failed = true))

  @Test fun failedDark() = shot(true, state(detail = null, failed = true))

  @Test fun refusedLight() = shot(false, state(detail = null, refused = "not found: attempt a99"))

  @Test fun refusedDark() = shot(true, state(detail = null, refused = "not found: attempt a99"))
}
