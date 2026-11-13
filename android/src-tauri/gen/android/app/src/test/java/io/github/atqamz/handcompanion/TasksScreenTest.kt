package io.github.atqamz.handcompanion

import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Rule
import org.junit.Test

private fun task(
  ref: String,
  title: String,
  state: String,
  parent: String? = null,
  waits: TaskWaits? = null
) = TaskRow(ref, title, state, parent = parent ?: "", waits = waits)

private fun finishedTasks(extra: Boolean) =
  listOf(
    task("t38", "Add the alert feed to the contract", "passing"),
    task("t37", "Sign debug builds with one committed key", "passing"),
    task("t35", "Spike: start the service at boot", "idle"),
    task("t33", "Read the companion vectors by path", "passing"),
    task("t32", "Bound the web Board open", "idle")
  ) + if (extra) listOf(task("t31", "Keep the pairing lock out", "passing"), task("t30", "Name the app role", "idle"))
  else emptyList()

private val taskGroups =
  listOf(
    TaskGroup(
      "Active",
      3,
      listOf(
        task("t41", "Rename the session cookie", "running"),
        task("t40", "Pin the luvus version in CI", "waiting"),
        task("t39", "Fix the flaky nightly test", "failing")
      )
    ),
    TaskGroup(
      "Inbox",
      2,
      listOf(
        task("t43", "Write the release notes for 0.15", "idle"),
        task("t44", "Try the new relay on the travel laptop", "idle", waits = TaskWaits("2026-11-14T09:00:00Z", "the laptop is away"))
      )
    ),
    TaskGroup("Finished", 7, finishedTasks(false))
  )

private val subTaskGroups =
  listOf(
    TaskGroup(
      "Active",
      2,
      listOf(
        task("t39", "Fix the flaky nightly test", "failing"),
        task("t42", "Bisect the arm64 failure", "idle", parent = "t39")
      )
    ),
    TaskGroup("Inbox", 1, listOf(task("t43", "Write the release notes for 0.15", "idle", parent = "t40")))
  )

private val allGroups =
  listOf(
    TaskGroup("Active", 600, taskGroups[0].tasks),
    taskGroups[1],
    TaskGroup("Done", 5, finishedTasks(true).take(5)),
    TaskGroup("Abandoned", 2, finishedTasks(true).drop(5))
  )

private fun tasks(
  groups: List<TaskGroup> = taskGroups,
  all: Boolean = false,
  hidden: Int = 0
) =
  FleetState(
    name = "atlas",
    pill = Badge("blocked", Tone.Fail),
    waiting = 5,
    tone = Tone.Fail,
    loaded = true,
    tab = Tab.Tasks,
    tasks = TasksState(loaded = true, all = all, groups = groups, hidden = hidden, finished = 7)
  )

class TasksScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 3000))

  private fun shot(dark: Boolean, state: FleetState) =
    paparazzi.snapshot {
      HandTheme(dark) {
        FleetScreen("f000000000003", state, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}) {}
      }
    }

  @Test fun tasksLight() = shot(false, tasks())

  @Test fun tasksDark() = shot(true, tasks())

  @Test fun tasksSubTasksLight() = shot(false, tasks(subTaskGroups))

  @Test fun tasksSubTasksDark() = shot(true, tasks(subTaskGroups))

  @Test
  fun tasksAllLight() = shot(false, tasks(allGroups, all = true, hidden = 597))

  @Test
  fun tasksAllDark() = shot(true, tasks(allGroups, all = true, hidden = 597))

  @Test fun tasksEmptyLight() = shot(false, tasks(groups = emptyList()))

  @Test fun tasksEmptyDark() = shot(true, tasks(groups = emptyList()))

  @Test
  fun tasksLoadingLight() = shot(false, FleetState(name = "atlas", loaded = true, tab = Tab.Tasks))

  @Test
  fun tasksLoadingDark() = shot(true, FleetState(name = "atlas", loaded = true, tab = Tab.Tasks))

  @Test
  fun tasksUnreachableLight() =
    shot(false, FleetState(name = "atlas", loaded = true, tab = Tab.Tasks, band = band(BoardError.Unreachable, "4d92…6ed6", "12:41"), tasks = TasksState(failed = true)))

  @Test
  fun tasksUnreachableDark() =
    shot(true, FleetState(name = "atlas", loaded = true, tab = Tab.Tasks, band = band(BoardError.Unreachable, "4d92…6ed6", "12:41"), tasks = TasksState(failed = true)))
}
