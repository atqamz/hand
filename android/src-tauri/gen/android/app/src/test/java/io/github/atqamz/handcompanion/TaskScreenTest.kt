package io.github.atqamz.handcompanion

import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Rule
import org.junit.Test

private const val FLEET = "f000000000003"

private fun taskInfo(parent: String = "", status: String = "active") =
  TaskInfo(
    ref = "t40",
    title = "Pin the luvus version in CI",
    goal = "",
    project = "hand",
    status = status,
    parent = parent,
    createdAt = "2026-11-11T09:00:00Z",
    updatedAt = "2026-11-11T11:52:00Z"
  )

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

private val decision =
  TaskDecision(
    ref = "d12",
    task = "t40",
    status = "open",
    pill = "waiting",
    headline = "Pin luvus to which version?",
    question = "Pin luvus to which version?",
    createdAt = "2026-11-11T11:40:00Z"
  )

private val answered =
  decision.copy(
    status = "answered",
    pill = "passing",
    answer = "2. Track 0.14.x",
    answeredBy = "phone (Pixel 8)",
    closedAt = "2026-11-11T11:45:00Z"
  )

private fun detail(
  parent: String = "",
  status: String = "active",
  pill: String = "running",
  openDecision: Boolean = true,
  unread: Boolean = true,
  withReport: Boolean = true,
  children: Boolean = false
) =
  TaskDetail(
    task = taskInfo(parent, status),
    pill = pill,
    plan =
      TaskPlan(
        ref = "p2",
        revision = 2,
        body = "Pin luvus in ci.yaml, the e2e image and install.sh to one version, and fail CI " +
          "when the three differ. Ask which version rule to follow before choosing the pin.",
        createdAt = "2026-11-11T10:00:00Z"
      ),
    delivered =
      listOf(
        TaskDeliverable("https://github.com/atqamz/hand/pull/1241", "PR"),
        TaskDeliverable("plan-t40.md", "file", "/doc/plan-t40.md")
      ),
    openDecisions = if (openDecision) listOf(decision) else emptyList(),
    decisions = listOf(if (openDecision) decision else answered),
    unread = if (unread) listOf(report) else emptyList(),
    reports =
      when {
        !withReport -> emptyList()
        unread -> listOf(report)
        else ->
          listOf(report.copy(ackedAt = "2026-11-11T12:00:00Z", ackedBy = "phone (Pixel 8)"))
      },
    moreReports = false,
    attempts =
      listOf(
        TaskAttempt(
          ref = "a62",
          task = "t40",
          harness = "codex",
          model = "gpt-5",
          effort = "high",
          status = "running",
          state = "running",
          branch = "hand/a62",
          createdAt = "2026-11-11T11:10:00Z"
        )
      ),
    events =
      listOf(
        TaskEvent(3, "2026-11-11T11:52:00Z", "attempt.reported", "a62 reported r90", "t40"),
        TaskEvent(2, "2026-11-11T11:40:00Z", "decision.asked", "d12 asked", "t40")
      ),
    moreEvents = false,
    children =
      if (children)
        listOf(TaskRow("t42", "Bisect the arm64 failure", "idle", status = "inbox", parent = "t40"))
      else emptyList()
  )

private fun state(detail: TaskDetail? = detail(), failed: Boolean = false, loading: Boolean = false) =
  TaskState(detail = detail, loading = loading, failed = failed)

class TaskScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 3000))

  private fun shot(dark: Boolean, shown: TaskState) =
    paparazzi.snapshot {
      HandTheme(dark) {
        TaskScreen(FLEET, "t40", shown, {}, {}, {}, {}, {})
      }
    }

  @Test fun taskLight() = shot(false, state())

  @Test fun taskDark() = shot(true, state())

  @Test fun nothingWaitingLight() =
    shot(false, state(detail(openDecision = false, unread = false, withReport = false)))

  @Test fun nothingWaitingDark() =
    shot(true, state(detail(openDecision = false, unread = false, withReport = false)))

  @Test fun subTaskLight() = shot(false, state(detail(parent = "t39")))

  @Test fun subTaskDark() = shot(true, state(detail(parent = "t39")))

  @Test fun childrenLight() =
    shot(false, state(detail(openDecision = false, unread = false, withReport = false, children = true)))

  @Test fun childrenDark() =
    shot(true, state(detail(openDecision = false, unread = false, withReport = false, children = true)))

  @Test fun inboxLight() =
    shot(false, state(detail(status = "inbox", pill = "neutral", openDecision = false, unread = false, withReport = false)))

  @Test fun inboxDark() =
    shot(true, state(detail(status = "inbox", pill = "neutral", openDecision = false, unread = false, withReport = false)))

  @Test fun readLight() = shot(false, state(detail(openDecision = false, unread = false)))

  @Test fun readDark() = shot(true, state(detail(openDecision = false, unread = false)))

  @Test fun loadingLight() = shot(false, TaskState())

  @Test fun loadingDark() = shot(true, TaskState())

  @Test fun failedLight() = shot(false, state(detail = null, failed = true, loading = false))

  @Test fun failedDark() = shot(true, state(detail = null, failed = true, loading = false))
}
