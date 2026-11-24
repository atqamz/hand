package io.github.atqamz.handcompanion

import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Rule
import org.junit.Test

private fun found(ref: String, title: String, state: String, project: String = "hand", parent: String = "", decisions: Int = 0) =
  TaskSearchRow(ref, title, state, project, parent, null, decisions)

private val results =
  TaskSearchData(
    page = 1,
    total = 4,
    pages = 1,
    groups =
      listOf(
        TaskSearchGroup(
          "Nov 20",
          3,
          listOf(
            found("t41", "Rename the session cookie", "passing"),
            found("t40", "Pin the luvus version in CI", "failing", "api", decisions = 1),
            found("t39", "Fix the flaky nightly test", "passing", parent = "t30")
          )
        ),
        TaskSearchGroup("Nov 19", 1, listOf(found("t38", "Add the alert feed to the contract", "idle", "api")))
      )
  )

private val filled =
  TaskSearchForm(q = "fix", status = "all", project = "hand", since = "2026-11-01", until = "2026-11-24")

private val applied = TaskSearchQuery("fix", "all", "hand", "2026-11-01", "2026-11-24")

private val base =
  TaskSearchState(supported = true, projects = listOf("hand", "api"), form = filled, applied = applied)

private val fleet =
  FleetState(
    name = "atlas",
    pill = Badge("blocked", Tone.Fail),
    waiting = 5,
    tone = Tone.Fail,
    loaded = true,
    tab = Tab.Tasks,
    tasks =
      TasksState(
        loaded = true,
        groups =
          listOf(
            TaskGroup(
              "Active",
              2,
              listOf(TaskRow("t41", "Rename the session cookie", "running"), TaskRow("t40", "Pin the luvus version in CI", "waiting"))
            )
          ),
        finished = 7
      )
  )

class TaskSearchScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 3000))

  private fun shot(dark: Boolean, search: TaskSearchState) =
    paparazzi.snapshot {
      HandTheme(dark) {
        FleetScreen(
          "f000000000003",
          fleet,
          {},
          {},
          {},
          {},
          {},
          {},
          {},
          {},
          { _, _ -> },
          {},
          {},
          search = TaskSearchUi(search),
          chat = {}
        ) {}
      }
    }

  @Test fun idleLight() = shot(false, TaskSearchState(supported = true, projects = listOf("hand", "api")))

  @Test fun idleDark() = shot(true, TaskSearchState(supported = true, projects = listOf("hand", "api")))

  @Test fun oneProjectLight() = shot(false, TaskSearchState(supported = true, projects = listOf("hand")))

  @Test fun undecidedLight() = shot(false, TaskSearchState(supported = null))

  @Test fun resultsLight() = shot(false, base.copy(result = results))

  @Test fun resultsDark() = shot(true, base.copy(result = results))

  @Test
  fun pagerLight() = shot(false, base.copy(result = results.copy(page = 2, pages = 3, total = 104)))

  @Test
  fun pagerDark() = shot(true, base.copy(result = results.copy(page = 2, pages = 3, total = 104)))

  @Test fun emptyLight() = shot(false, base.copy(result = TaskSearchData(total = 0, pages = 0)))

  @Test fun emptyDark() = shot(true, base.copy(result = TaskSearchData(total = 0, pages = 0)))

  @Test fun loadingLight() = shot(false, base.copy(busy = true))

  @Test fun loadingDark() = shot(true, base.copy(busy = true))

  @Test
  fun failedLight() = shot(false, base.copy(error = "since must be a date"))

  @Test
  fun failedDark() = shot(true, base.copy(error = "The Board could not be reached."))

  @Test
  fun staleLight() = shot(false, base.copy(result = results, stale = "The Board could not be reached."))

  @Test
  fun staleDark() = shot(true, base.copy(result = results, stale = "The Board could not be reached."))
}
