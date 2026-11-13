package io.github.atqamz.handcompanion

import org.junit.Assert.assertEquals
import org.junit.Test

class AlertGateTest {
  @Test
  fun nothingPairedStartsNothingAndAsksNothing() {
    for (allowed in listOf(true, false)) {
      for (asked in listOf(true, false)) {
        assertEquals(AlertStep.Idle, alertStep(false, allowed, asked))
      }
    }
  }

  @Test
  fun aPairedPhoneWithPermissionStartsTheService() {
    assertEquals(AlertStep.Start, alertStep(true, true, false))
    assertEquals(AlertStep.Start, alertStep(true, true, true))
  }

  @Test
  fun aPairedPhoneAsksOnceAndThenSaysAlertsAreOff() {
    assertEquals(AlertStep.Ask, alertStep(true, false, false))
    assertEquals(AlertStep.Off, alertStep(true, false, true))
  }
}
