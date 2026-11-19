package io.github.atqamz.handcompanion

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

private const val MINUTE = 60 * 1000L

private class FakeTime : IdleEffects {
  var now = 0L
  var graceAt: Long? = null
  var alarmAt: Long? = null
  val calls = mutableListOf<String>()
  lateinit var cycle: IdleCycle

  fun advance(ms: Long) {
    val end = now + ms
    while (true) {
      val next = listOfNotNull(graceAt, alarmAt).filter { it <= end }.minOrNull() ?: break
      now = next
      if (graceAt == next) {
        graceAt = null
        cycle.onGrace()
      } else {
        alarmAt = null
        cycle.onAlarm()
      }
    }
    now = end
  }

  fun take(): List<String> = calls.toList().also { calls.clear() }

  override fun armGrace(delayMs: Long) {
    graceAt = now + delayMs
    calls += "armGrace $delayMs"
  }

  override fun cancelGrace() {
    graceAt = null
    calls += "cancelGrace"
  }

  override fun sleep() {
    calls += "sleep"
  }

  override fun wake() {
    calls += "wake"
  }

  override fun armAlarm(delayMs: Long) {
    alarmAt = now + delayMs
    calls += "armAlarm $delayMs"
  }

  override fun cancelAlarm() {
    alarmAt = null
    calls += "cancelAlarm"
  }

  override fun poll() {
    calls += "poll"
  }
}

class IdleCycleTest {
  private val time = FakeTime()
  private val cycle = IdleCycle(time).also { time.cycle = it }

  private fun asleep(): IdleCycle {
    cycle.start(screenOn = true, charging = false)
    cycle.onScreen(false)
    time.advance(IdleCycle.GRACE_MS)
    time.take()
    assertTrue(cycle.asleep)
    return cycle
  }

  @Test
  fun anAwakePhoneOnTheScreenDoesNothing() {
    cycle.start(screenOn = true, charging = false)
    time.advance(60 * MINUTE)

    assertEquals(emptyList<String>(), time.take())
    assertFalse(cycle.asleep)
  }

  @Test
  fun screenOffOnBatteryArmsTheGraceAndSleepsOnlyAfterTwoMinutes() {
    cycle.start(screenOn = true, charging = false)
    cycle.onScreen(false)
    assertEquals(listOf("armGrace 120000"), time.take())

    time.advance(2 * MINUTE - 1)
    assertEquals(emptyList<String>(), time.take())
    assertFalse(cycle.asleep)

    time.advance(1)
    assertEquals(listOf("sleep", "armAlarm 300000"), time.take())
    assertTrue(cycle.asleep)
  }

  @Test
  fun screenOnInsideTheGraceCancelsItAndNeverSleeps() {
    cycle.start(screenOn = true, charging = false)
    cycle.onScreen(false)
    time.advance(MINUTE)
    time.take()

    cycle.onScreen(true)
    time.advance(10 * MINUTE)

    assertEquals(listOf("cancelGrace"), time.take())
    assertFalse(cycle.asleep)
  }

  @Test
  fun aSecondScreenOffRestartsTheWholeGrace() {
    cycle.start(screenOn = true, charging = false)
    cycle.onScreen(false)
    time.advance(MINUTE + 30 * 1000)
    cycle.onScreen(true)
    cycle.onScreen(false)
    time.take()
    time.advance(2 * MINUTE - 1)
    assertFalse(cycle.asleep)

    time.advance(1)

    assertTrue(cycle.asleep)
  }

  @Test
  fun aRepeatedScreenOffDoesNotArmTheGraceTwice() {
    cycle.start(screenOn = true, charging = false)
    cycle.onScreen(false)
    time.advance(MINUTE)
    cycle.onScreen(false)

    assertEquals(listOf("armGrace 120000"), time.take())
    time.advance(MINUTE)
    assertTrue(cycle.asleep)
  }

  @Test
  fun aChargerInsideTheGraceCancelsItAndKeepsTheFeedLive() {
    cycle.start(screenOn = true, charging = false)
    cycle.onScreen(false)
    time.advance(MINUTE)
    time.take()

    cycle.onCharging(true)
    time.advance(10 * MINUTE)

    assertEquals(listOf("cancelGrace"), time.take())
    assertFalse(cycle.asleep)
  }

  @Test
  fun screenOffWhileChargingNeverArmsTheGraceUntilTheChargerLeaves() {
    cycle.start(screenOn = true, charging = true)
    cycle.onScreen(false)
    time.advance(30 * MINUTE)
    assertEquals(emptyList<String>(), time.take())

    cycle.onCharging(false)
    assertEquals(listOf("armGrace 120000"), time.take())
    time.advance(2 * MINUTE)
    assertTrue(cycle.asleep)
  }

  @Test
  fun aServiceStartedWithTheScreenOffOnBatteryStartsTheGrace() {
    cycle.start(screenOn = false, charging = false)

    assertEquals(listOf("armGrace 120000"), time.take())
    assertFalse(cycle.asleep)
  }

  @Test
  fun aServiceStartedOnTheChargerArmsNothing() {
    cycle.start(screenOn = false, charging = true)
    assertEquals(emptyList<String>(), time.take())
  }

  @Test
  fun anAlarmPollsAndArmsTheNextOneEveryFiveMinutes() {
    asleep()

    time.advance(5 * MINUTE)
    assertEquals(listOf("armAlarm 300000", "poll"), time.take())
    time.advance(5 * MINUTE)
    assertEquals(listOf("armAlarm 300000", "poll"), time.take())
    time.advance(4 * MINUTE + 59 * 1000)
    assertEquals(emptyList<String>(), time.take())
    assertTrue(cycle.asleep)
  }

  @Test
  fun screenOnWhileAsleepWakesAndCancelsTheAlarm() {
    asleep()
    time.advance(7 * MINUTE)
    time.take()

    cycle.onScreen(true)
    time.advance(30 * MINUTE)

    assertEquals(listOf("cancelAlarm", "wake"), time.take())
    assertFalse(cycle.asleep)
    assertEquals(null, time.alarmAt)
  }

  @Test
  fun unlockWhileAsleepWakes() {
    asleep()

    cycle.onUnlocked()

    assertEquals(listOf("cancelAlarm", "wake"), time.take())
    assertFalse(cycle.asleep)
  }

  @Test
  fun aChargerWhileAsleepWakesWithTheScreenStillOff() {
    asleep()

    cycle.onCharging(true)

    assertEquals(listOf("cancelAlarm", "wake"), time.take())
    time.advance(30 * MINUTE)
    assertEquals(emptyList<String>(), time.take())
  }

  @Test
  fun leavingTheChargerWithTheScreenOffSleepsAgainAfterTheGraceAndPollsFromThen() {
    asleep()
    cycle.onCharging(true)
    time.take()

    cycle.onCharging(false)
    time.advance(2 * MINUTE)
    assertEquals(listOf("armGrace 120000", "sleep", "armAlarm 300000"), time.take())

    time.advance(5 * MINUTE)
    assertEquals(listOf("armAlarm 300000", "poll"), time.take())
  }

  @Test
  fun aStaleAlarmWhileLiveIsIgnored() {
    cycle.start(screenOn = true, charging = false)

    cycle.onAlarm()

    assertEquals(emptyList<String>(), time.take())
  }

  @Test
  fun aStaleGraceAfterTheScreenCameBackIsIgnored() {
    cycle.start(screenOn = true, charging = false)
    cycle.onScreen(false)
    time.take()
    cycle.onScreen(true)
    time.take()

    cycle.onGrace()

    assertEquals(emptyList<String>(), time.take())
    assertFalse(cycle.asleep)
  }

  @Test
  fun sleepingAgainAfterAWakeStartsAFreshAlarmCycle() {
    asleep()
    time.advance(3 * MINUTE)
    cycle.onScreen(true)
    cycle.onScreen(false)
    time.take()

    time.advance(2 * MINUTE)
    assertEquals(listOf("sleep", "armAlarm 300000"), time.take())
    time.advance(5 * MINUTE)
    assertEquals(listOf("armAlarm 300000", "poll"), time.take())
  }

  @Test
  fun aGraceDeliveredLateAfterASuspendStillSleepsAndThePollCountsFromTheDelivery() {
    cycle.start(screenOn = true, charging = false)
    cycle.onScreen(false)
    time.take()

    time.graceAt = null
    time.now += 40 * MINUTE
    cycle.onGrace()

    assertEquals(listOf("sleep", "armAlarm 300000"), time.take())
    assertEquals(time.now + 5 * MINUTE, time.alarmAt)
    assertTrue(cycle.asleep)
  }

  @Test
  fun anUnreachableHostDoublesTheAlarmToAFortyMinuteCapAndOneAnswerResetsIt() {
    asleep()
    val gaps = mutableListOf<String>()
    repeat(5) {
      time.advance(time.alarmAt!! - time.now)
      time.take()
      cycle.onPolled(reached = false)
      gaps += time.take().single()
    }

    assertEquals(
      listOf("armAlarm 600000", "armAlarm 1200000", "armAlarm 2400000", "armAlarm 2400000", "armAlarm 2400000"),
      gaps
    )

    cycle.onPolled(reached = true)
    assertEquals(listOf("armAlarm 300000"), time.take())
  }

  @Test
  fun anAlarmAfterAMissedPollIsArmedAtTheBackedOffGapBeforeItPolls() {
    asleep()
    time.advance(5 * MINUTE)
    time.take()
    cycle.onPolled(reached = false)
    time.take()

    time.advance(10 * MINUTE)

    assertEquals(listOf("armAlarm 600000", "poll"), time.take())
  }

  @Test
  fun aWakeResetsTheBackoffAndALatePollResultWhileLiveIsIgnored() {
    asleep()
    time.advance(5 * MINUTE)
    cycle.onPolled(reached = false)
    cycle.onScreen(true)
    time.take()

    cycle.onPolled(reached = false)
    cycle.onScreen(false)
    time.advance(2 * MINUTE)
    assertEquals(listOf("armGrace 120000", "sleep", "armAlarm 300000"), time.take())
  }

  @Test
  fun aFailedWakeIsRetriedAfterThirtySecondsAtMostThreeTimes() {
    asleep()
    cycle.onScreen(true)
    time.take()

    repeat(3) {
      assertTrue(cycle.onWakeFailed())
      assertEquals(listOf("armAlarm 30000"), time.take())
      time.advance(30 * 1000)
      assertEquals(listOf("wake"), time.take())
    }

    assertFalse(cycle.onWakeFailed())
    time.advance(60 * MINUTE)
    assertEquals(emptyList<String>(), time.take())
  }

  @Test
  fun aWakeThatFailsWhileTheCycleIsAsleepIsNotRetried() {
    asleep()

    assertFalse(cycle.onWakeFailed())
    assertEquals(emptyList<String>(), time.take())
  }

  @Test
  fun aFailedWakeThenSleepingAgainPollsInsteadOfWaking() {
    asleep()
    cycle.onScreen(true)
    cycle.onWakeFailed()
    cycle.onScreen(false)
    time.take()

    time.graceAt = null
    cycle.onGrace()
    assertEquals(listOf("sleep", "armAlarm 300000"), time.take())
    time.advance(5 * MINUTE)

    assertEquals(listOf("armAlarm 300000", "poll"), time.take())
  }

  @Test
  fun aNewWakeStartsItsRetriesAgain() {
    asleep()
    cycle.onScreen(true)
    repeat(3) {
      cycle.onWakeFailed()
      time.advance(30 * 1000)
    }
    assertFalse(cycle.onWakeFailed())
    cycle.onScreen(false)
    time.advance(2 * MINUTE)
    cycle.onScreen(true)
    time.take()

    assertTrue(cycle.onWakeFailed())
  }
}
