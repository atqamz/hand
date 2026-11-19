package io.github.atqamz.handcompanion

import android.view.View
import androidx.compose.ui.platform.LocalView
import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

private class Taken {
  val presses = mutableListOf<Pair<String, List<String>>>()
  val attempts = mutableListOf<String>()
  val toggles = mutableListOf<String>()
}

private fun workerState(ref: String, keys: NeedKeys?, title: String = "Bash: git push --force") =
  FleetState(
    name = "atlas",
    pill = Badge("working", Tone.Pass),
    waiting = 1,
    tone = Tone.Fail,
    loaded = true,
    tab = Tab.Needs,
    bands =
      bands(
        listOf(
          Need(
            "worker",
            ref,
            title,
            "BLOCKED",
            "fail",
            task = "t39",
            attempt = ref,
            excerpt = "Run git push?",
            keys = keys
          )
        )
      )
  )

private val threeKeys =
  NeedKeys(
    listOf(
      AskOption("1 Yes", listOf("1")),
      AskOption("2 Pick", listOf("down", "enter")),
      AskOption("3 No", listOf("3"))
    ),
    6,
    "f8c722e0710879ae811b2fe7ad0c8ba0",
    0
  )

class WorkerCardRouteTest {
  private val frames = Frames()

  @get:Rule
  val paparazzi =
    Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 2000), snapshotHandler = frames)

  private fun shown(state: FleetState, act: Taps.() -> Unit): Taken {
    val taken = Taken()
    lateinit var view: View
    frames.onFrame = { Taps(view).act() }
    paparazzi.snapshot {
      HandTheme(false) {
        view = LocalView.current
        FleetScreen(
          "f000000000003",
          state,
          {},
          {},
          {},
          { taken.toggles += it },
          {},
          {},
          {},
          {},
          { entry, keys -> taken.presses += entry to keys },
          {},
          { taken.attempts += it },
          chat = {}
        ) {}
      }
    }
    return taken
  }

  @Test
  fun eachButtonSendsItsOwnOptionsKeysForItsOwnCard() {
    val taken =
      shown(workerState("a61", threeKeys)) {
        tap("2 Pick")
        tap("3 No")
        tap("1 Yes")
      }
    assertEquals(
      listOf(
        "worker:a61" to listOf("down", "enter"),
        "worker:a61" to listOf("3"),
        "worker:a61" to listOf("1")
      ),
      taken.presses
    )
    assertTrue(taken.attempts.isEmpty())
  }

  @Test
  fun openAttemptOpensThatAttemptAndPressesNothing() {
    val taken = shown(workerState("a61", threeKeys)) { tap("Open a61") }
    assertEquals(listOf("a61"), taken.attempts)
    assertTrue(taken.presses.isEmpty())
  }

  @Test
  fun aWorkerWithNoKeysOpensItsAttemptScreen() {
    val taken = shown(workerState("a62", null)) { tap("Bash: git push --force") }
    assertEquals(listOf("a62"), taken.attempts)
    assertTrue(taken.presses.isEmpty())
    assertTrue(taken.toggles.isEmpty())
  }
}
