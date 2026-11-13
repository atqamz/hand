package io.github.atqamz.handcompanion

import android.app.Notification
import android.app.NotificationManager
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class AlertNoticeTest {
  private val link = "handboard://open/f0123456789ab/decision/d3"

  @Test
  fun aDecisionHasItsOwnChannel() {
    assertEquals(Channels.DECISIONS, channelFor("decision.asked"))
    assertEquals("decisions", Channels.DECISIONS)
  }

  @Test
  fun stuckWorkNeedsYou() {
    for (kind in
      listOf(
        "attempt.blocked",
        "attempt.limited",
        "attempt.failed",
        "supervisor.blocked",
        "supervisor.limited",
        "supervisor.stalled"
      )) {
      assertEquals(kind, Channels.NEEDS, channelFor(kind))
    }
  }

  @Test
  fun everyOtherKindIsProgress() {
    for (kind in listOf("attempt.finished", "task.done", "future.kind", "")) {
      assertEquals(kind, Channels.PROGRESS, channelFor(kind))
    }
  }

  @Test
  fun anAlertReplacesTheOlderOneOnTheSameItem() {
    val first = alertSpec("f0123456789ab", "hand", "decision.asked", "d3", "old", link)
    val second = alertSpec("f0123456789ab", "hand", "decision.asked", "d3", "new", link)
    val other = alertSpec("f999999999999", "hand", "decision.asked", "d3", "old", null)
    assertEquals("f0123456789ab/d3", first.tag)
    assertEquals(first.tag, second.tag)
    assertEquals("f999999999999/d3", other.tag)
  }

  @Test
  fun theTitleNamesTheFleetAndTheItem() {
    assertEquals("hand · d3", alertSpec("f0123456789ab", "hand", "x", "d3", "t", null).title)
    assertEquals("f0123456789ab · d3", alertSpec("f0123456789ab", " ", "x", "d3", "t", null).title)
  }

  @Test
  fun theTextStaysPlainAndTheLockScreenStaysPrivate() {
    val spec = alertSpec("f0123456789ab", "hand", "x", "d3", "<b>t3</b> &amp; more", link)
    assertEquals("<b>t3</b> &amp; more", spec.text)
    assertEquals(Notification.VISIBILITY_PRIVATE, spec.visibility)
    assertEquals(Notification.VISIBILITY_PRIVATE, unpairedSpec("f0123456789ab", "hand").visibility)
  }

  @Test
  fun aTapOpensAnOpenLinkAndNothingElse() {
    assertEquals(link, alertSpec("f0123456789ab", "hand", "x", "d3", "t", link).tapUri)
    assertNull(alertSpec("f0123456789ab", "hand", "x", "d3", "t", null).tapUri)
    for (foreign in
      listOf(
        "https://example.test/f0123456789ab",
        "handboard://c#v1.abc",
        "intent://x#Intent;end",
        "tel:123"
      )) {
      assertNull(foreign, alertSpec("f0123456789ab", "hand", "x", "d3", "t", foreign).tapUri)
    }
  }

  @Test
  fun aLostPairingNamesTheFleetOrItsId() {
    val named = unpairedSpec("f0123456789ab", "hand")
    assertEquals("No longer paired with hand", named.text)
    assertEquals("f0123456789ab/unpaired", named.tag)
    assertEquals(Channels.NEEDS, named.channel)
    assertNull(named.tapUri)
    assertEquals("No longer paired with f0123456789ab", unpairedSpec("f0123456789ab", null).text)
  }

  @Test
  fun unreadablePairingsSayToCheckTheAppAndNameNothing() {
    val spec = unreadableSpec()
    assertEquals("Pairings could not be read; check the app", spec.text)
    assertEquals("sessions/unreadable", spec.tag)
    assertEquals(Channels.NEEDS, spec.channel)
    assertNull(spec.tapUri)
    assertEquals(Notification.VISIBILITY_PRIVATE, spec.visibility)
  }

  @Test
  fun aNoticeIsAcknowledgedOnlyAfterItWasPosted() {
    var posted = 0
    assertEquals(false, deliver(false) { posted++ })
    assertEquals(0, posted)
    assertEquals(true, deliver(true) { posted++ })
    assertEquals(1, posted)
  }

  @Test
  fun everyChannelAKindOrAnObjectNamesIsCreated() {
    val kinds =
      listOf(
        "decision.asked",
        "attempt.blocked",
        "attempt.limited",
        "attempt.failed",
        "supervisor.blocked",
        "supervisor.limited",
        "supervisor.stalled",
        "attempt.long",
        "future.kind",
        ""
      )
    for (kind in kinds) {
      assertTrue(kind, channelFor(kind) in channelIds)
    }
    assertTrue(unpairedSpec("f0123456789ab", null).channel in channelIds)
    val constants =
      Channels::class.java.declaredFields
        .filter { java.lang.reflect.Modifier.isStatic(it.modifiers) && it.type == String::class.java }
        .map { it.apply { isAccessible = true }.get(null) as String }
    assertEquals(channelIds.size, constants.size)
    for (constant in constants) {
      assertTrue(constant, constant in channelIds)
    }
    assertEquals(channelIds.size, channelIds.toSet().size)
  }

  @Test
  fun aDecisionChannelInterruptsLikeNeeds() {
    val importance = channelSpecs.associate { it.id to it.importance }
    assertEquals(NotificationManager.IMPORTANCE_HIGH, importance[Channels.DECISIONS])
    assertEquals(NotificationManager.IMPORTANCE_HIGH, importance[Channels.NEEDS])
    assertEquals(NotificationManager.IMPORTANCE_LOW, importance[Channels.SERVICE])
  }
}
