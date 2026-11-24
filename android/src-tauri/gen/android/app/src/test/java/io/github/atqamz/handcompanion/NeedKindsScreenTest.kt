package io.github.atqamz.handcompanion

import android.view.View
import androidx.compose.ui.platform.LocalView
import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

private fun kind(
  kind: String,
  word: String,
  tone: String,
  ref: String?,
  title: String?,
  task: String? = null,
  attempt: String? = null,
  at: String? = null
) =
  Need(
    kind,
    ref,
    title,
    word,
    tone,
    task,
    attempt = attempt,
    attemptMeta = attempt?.let { NeedAttempt("codex") },
    createdAt = at
  )

private val failure =
  kind("failure", "FAILED", "fail", "a61", "Flaky test in the nightly pipeline", "t39", "a61", "2026-11-01T11:20:00Z")
private val resume = kind("resume", "INTERRUPTED", "fail", "s2", "The supervisor stopped unexpectedly")
private val limited = kind("limited", "LIMITED", "wait", "s2", null)
private val limitedAttempt = kind("limited", "LIMITED", "wait", "a70", null, "t41", "a70", "2026-11-01T11:30:00Z")
private val nosup = kind("nosup", "NO SUPERVISOR", "wait", null, "Work is waiting for a supervisor")
private val stalled =
  kind("stalled", "STALLED", "wait", "s2", "Supervisor idle with 2 inbox tasks and no running attempt")
private val quiet =
  kind("quiet", "QUIET", "neutral", "a8", "Its turn ended without a report", "t12", "a8", "2026-11-01T10:10:00Z")
private val long =
  kind("long", "LONG TURN", "neutral", "a9", "Turn running for 3h 05m", "t13", "a9", "2026-11-01T08:00:00Z")
private val kept =
  kind("kept", "KEPT", "neutral", "a55", "Worktree kept: uncommitted changes", "t30", "a55", "2026-11-01T09:00:00Z")
private val artifacts = kind("artifacts", "OLD FILES", "neutral", null, "3 old files in reports/ and shots/")

private fun queue(vararg needs: Need) =
  FleetState(
    name = "atlas",
    pill = Badge("blocked", Tone.Wait),
    waiting = needs.size,
    tone = Tone.Wait,
    loaded = true,
    tab = Tab.Needs,
    bands = bands(needs.toList()),
    open = ""
  )

private val interrupted = queue(failure, resume, limited, limitedAttempt, nosup, stalled)
private val background = queue(quiet, long, kept, artifacts)

class NeedKindsScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 2400))

  private fun shot(dark: Boolean, state: FleetState) =
    paparazzi.snapshot {
      HandTheme(dark) { FleetScreen("f000000000003", state, {}, {}, {}, {}, {}, {}, {}, {}, { _, _ -> }, {}, {}, chat = {}) {} }
    }

  @Test fun failureResumeLimitedStalledLight() = shot(false, interrupted)

  @Test fun failureResumeLimitedStalledDark() = shot(true, interrupted)

  @Test fun quietLongKeptArtifactsLight() = shot(false, background)

  @Test fun quietLongKeptArtifactsDark() = shot(true, background)
}

class NeedKindsTest {
  private fun entries(vararg needs: Need) = bands(needs.toList()).flatMap { it.entries }

  @Test
  fun everyKindOpensTheWayItsRowPromises() {
    assertEquals(
      listOf(
        Opens.Row,
        Opens.Web,
        Opens.Web,
        Opens.Web,
        Opens.Web,
        Opens.Web,
        Opens.Web,
        Opens.Web,
        Opens.Web,
        Opens.Web
      ),
      entries(failure, resume, limited, limitedAttempt, nosup, stalled, quiet, long, kept, artifacts).map { it.opens }
    )
  }

  @Test
  fun aBandHoldsRunsOfOneWordInTheBoardsOrderAndTakesTheKindsTone() {
    val bands = bands(listOf(failure, resume, limited, limitedAttempt, nosup, stalled, quiet, long, kept, artifacts))
    assertEquals(
      listOf("Failed", "Interrupted", "Limited", "No supervisor", "Stalled", "Quiet", "Long turn", "Kept", "Old files"),
      bands.map { it.title }
    )
    assertEquals(2, bands.first { it.title == "Limited" }.entries.size)
    assertEquals(
      listOf(Tone.Fail, Tone.Fail, Tone.Wait, Tone.Wait, Tone.Wait, Tone.Neutral, Tone.Neutral, Tone.Neutral, Tone.Neutral),
      bands.map { it.tone }
    )
  }

  @Test
  fun everyKindKeepsItsOwnKeyEvenWithoutARef() {
    assertEquals(
      listOf("nosup:null", "artifacts:null", "limited:s2", "limited:a70"),
      entries(nosup, artifacts, limited, limitedAttempt).map { it.key }
    )
  }
}

class NeedKindsRouteTest {
  private val frames = Frames()

  @get:Rule
  val paparazzi =
    Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 2400), snapshotHandler = frames)

  private class Taken {
    val attempts = mutableListOf<String>()
    val reports = mutableListOf<String>()
    var web = 0
  }

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
          {},
          { taken.reports += it },
          { taken.web++ },
          {},
          {},
          { _, _ -> },
          {},
          { taken.attempts += it },
          chat = {}
        ) {}
      }
    }
    return taken
  }

  @Test
  fun aKindThatNamesAnAttemptOpensThatAttempt() {
    val taken =
      shown(interrupted) {
        tap("Flaky test in the nightly pipeline")
        tap("Limited a70")
      }
    assertEquals(listOf("a61", "a70"), taken.attempts)
    assertEquals(0, taken.web)
  }

  @Test
  fun aKindWithNoAttemptFallsBackToTheBoardPage() {
    val taken =
      shown(interrupted) {
        tap("The supervisor stopped unexpectedly")
        tap("Limited s2")
        tap("Work is waiting for a supervisor")
        tap("Supervisor idle with 2 inbox tasks and no running attempt")
      }
    assertTrue(taken.attempts.isEmpty())
    assertEquals(4, taken.web)
  }

  @Test
  fun theBackgroundKindsOpenTheirAttemptOrThePage() {
    val taken =
      shown(background) {
        tap("Its turn ended without a report")
        tap("Turn running for 3h 05m")
        tap("Worktree kept: uncommitted changes")
        tap("3 old files in reports/ and shots/")
      }
    assertEquals(listOf("a8", "a9", "a55"), taken.attempts)
    assertEquals(1, taken.web)
  }
}
