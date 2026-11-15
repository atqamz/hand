package io.github.atqamz.handcompanion

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private fun sup(
  status: String = "running",
  live: Boolean = true,
  model: String? = "opus",
  effort: String? = "high",
  resumable: Boolean = false,
  blocked: Boolean = false,
  ref: String = "s1"
) =
  Supervisor(
    ref = ref,
    harness = "claude",
    model = model,
    effort = effort,
    status = status,
    live = live,
    pending = 0,
    resumable = resumable,
    blocked = blocked
  )

class ChatHeadTest {
  @Test
  fun aTurnThatRunsOffersInterruptAndStop() {
    assertEquals(
      listOf(HeadControl.Interrupt, HeadControl.Stop),
      headControls(sup(), "working")
    )
  }

  @Test
  fun aBlockedOrIdleTurnOffersOnlyStop() {
    assertEquals(listOf(HeadControl.Stop), headControls(sup(blocked = true), "blocked"))
    assertEquals(listOf(HeadControl.Stop), headControls(sup(), "ready"))
  }

  @Test
  fun anEndedSupervisorOffersResumeAsPrimaryAndStartNew() {
    val ended = sup(status = "stopped", live = false, resumable = true)
    assertEquals(listOf(HeadControl.Resume, HeadControl.Start), headControls(ended, "idle"))
    assertTrue(chatHead(ended, "idle").sendPrimary.not())
    assertTrue(chatHead(sup(), "working").sendPrimary)
  }

  @Test
  fun anEndedSupervisorWithoutAResumeOffersOnlyStartNew() {
    val ended = sup(status = "exited", live = false, resumable = false)
    assertEquals(listOf(HeadControl.Start), headControls(ended, "idle"))
    assertEquals("s1 exited", supervisorWords(ended))
  }

  @Test
  fun noSupervisorOffersOnlyStartNew() {
    assertEquals(listOf(HeadControl.Start), headControls(null, "idle"))
    assertEquals("No supervisor", supervisorWords(null))
    assertFalse(chatHead(null, "idle").sendPrimary)
  }

  @Test
  fun aSupervisorThatIsStillLaunchingOffersNoControl() {
    val starting = sup(status = "launching", live = true)
    assertTrue(headControls(starting, "idle").isEmpty())
    assertEquals("starting…", supervisorWords(starting))
  }

  @Test
  fun aRunningSupervisorShowsTheModelAndEffort() {
    assertEquals("opus · high", supervisorWords(sup()))
    assertEquals("claude · high", supervisorWords(sup(model = null)))
    assertEquals("opus", supervisorWords(sup(effort = null)))
  }

  @Test
  fun anUnreachableLaptopDisablesEveryControlWithTheBandSentence() {
    val head =
      chatHead(sup(), "working", reachable = false, reason = "Can't reach 4d92…6ed6 since 12:41")
    assertFalse(head.enabled)
    assertEquals("Can't reach 4d92…6ed6 since 12:41", head.reason)
    assertEquals(2, head.controls.size)
    assertEquals(CONTROL_UNREACHABLE, chatHead(sup(), "working", reachable = false).reason)
  }

  @Test
  fun aControlInFlightDisablesTheRest() {
    assertFalse(chatHead(sup(), "working", busy = true).enabled)
    assertTrue(chatHead(sup(), "working").enabled)
  }

  @Test
  fun stopArmsOnTheFirstPressSendsOnTheSecondAndFallsBackAfterTheTimeout() {
    val first = stopPress(null, 1_000)
    assertEquals(5_000L, first.armedUntil)
    assertFalse(first.send)
    val second = stopPress(first.armedUntil, 2_000)
    assertNull(second.armedUntil)
    assertTrue(second.send)
    val late = stopPress(first.armedUntil, 5_000)
    assertEquals(9_000L, late.armedUntil)
    assertFalse(late.send)
  }

  @Test
  fun eachControlNamesItsPathAndOnlyTheTwoTurnControlsSendARef() {
    assertEquals("supervisor/interrupt", controlPath(HeadControl.Interrupt))
    assertEquals("supervisor/stop", controlPath(HeadControl.Stop))
    assertEquals("supervisor/resume", controlPath(HeadControl.Resume))
    assertEquals("supervisor/start", controlPath(HeadControl.Start))
    assertEquals("""{"ref":"s1"}""", controlBody(HeadControl.Interrupt, "s1"))
    assertEquals("""{"ref":"s1"}""", controlBody(HeadControl.Stop, "s1"))
    assertNull(controlBody(HeadControl.Resume, "s1"))
    assertNull(controlBody(HeadControl.Start, "s1"))
  }

  @Test
  fun eachRefusalHasItsOwnSentence() {
    val conflict = "conflict: no supervisor is running; start one"
    assertEquals(NOTHING_TO_RESUME, headFailure(HeadControl.Resume, BoardError.NotFound("x")))
    assertEquals(CONTROLS_OFF, headFailure(HeadControl.Resume, BoardError.Forbidden("x")))
    assertEquals(conflict, headFailure(HeadControl.Stop, BoardError.Conflict(conflict)))
    assertEquals(
      "The Board refused it; nothing was sent.",
      headFailure(HeadControl.Start, BoardError.Conflict(null))
    )
    assertEquals(CONTROL_UNREACHABLE, headFailure(HeadControl.Stop, BoardError.Unreachable))
  }

  @Test
  fun aNoteLivesTheReceiptTheRefusalOrTheWarnLength() {
    assertEquals(RECEIPT_MILLIS, headNoteMillis(HeadNote("s1 stopping")))
    assertEquals(
      ERROR_MILLIS,
      headNoteMillis(HeadNote("conflict: no supervisor is running", Tone.Fail))
    )
    assertEquals(WARN_MILLIS, headNoteMillis(HeadNote("s1 stopping", warn = "starting anyway")))
    assertEquals(WARN_MILLIS, headNoteMillis(HeadNote("conflict: no", Tone.Fail, "starting anyway")))
    assertEquals(4_000L, RECEIPT_MILLIS)
    assertEquals(8_000L, ERROR_MILLIS)
    assertEquals(12_000L, WARN_MILLIS)
  }

  @Test
  fun theFleetStatePutsTheBandSentenceUnderTheDisabledControls() {
    val state =
      FleetState(
        supervisor = sup(),
        agentState = "working",
        reachable = false,
        band = Band("Can't reach 4d92…6ed6 since 12:41"),
        controlBusy = HeadControl.Stop,
        stopArmedUntil = 9_000,
        headNote = HeadNote("s1 stopping")
      )
    val head = state.head()
    assertEquals("Can't reach 4d92…6ed6 since 12:41", head.reason)
    assertFalse(head.enabled)
    assertTrue(head.stopArmed)
    assertEquals(HeadControl.Stop, state.controlBusy)
    assertEquals("s1 stopping", head.note?.text)
  }
}
