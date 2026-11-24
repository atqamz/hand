package io.github.atqamz.handcompanion

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private const val HOME = "f000000000003"
private const val NOT_PAIRED = "Pair this Companion with your host first."
private const val REJECTED = "This is not a link Hand can open."

private class Opener(private val paired: Set<String>) : (String) -> String? {
  val asked = mutableListOf<String>()

  override fun invoke(link: String): String? {
    asked += link
    val fleet = link.removePrefix("handboard://open/").substringBefore('/')
    return when {
      paired.isEmpty() -> """{"message":"$NOT_PAIRED"}"""
      !link.startsWith("handboard://open/") || fleet !in paired -> """{"message":"$REJECTED"}"""
      else -> path(fleet, "")
    }
  }
}

private fun path(fleet: String, page: String) = """{"path":"/$fleet/$page"}"""

class LandingTest {
  @Test
  fun aPairingLinkGoesToConfirmWithoutAskingTheCore() {
    val open = Opener(setOf(HOME))
    val link = "handboard://c#v1.abc"
    assertEquals(Landing.Pairing(link), landing(link, open))
    assertEquals(Landing.Pairing("handboard://c"), landing("handboard://c", open))
    assertTrue(open.asked.isEmpty())
  }

  @Test
  fun theNotificationTapGoesToThisPhoneWithoutAskingTheCore() {
    val open = Opener(setOf(HOME))
    assertEquals(Landing.Phone, landing(PHONE_LINK, open))
    assertTrue(open.asked.isEmpty())
    assertEquals(Landing.Phone, landing(incomingLink(THIS_PHONE_ACTION, null).orEmpty(), open))
  }

  @Test
  fun eachNotificationTapLandsWhereItShould() {
    val link = "handboard://open/$HOME/decision/d3"
    val open = { text: String -> path(HOME, text.removePrefix("handboard://open/$HOME/")) }
    fun land(tap: Tap) = incomingLink(tap.action, tap.uri)?.let { landing(it, open) }

    assertEquals(Landing.Phone, land(tapOf(null)))
    assertEquals(
      Landing.Decision(HOME, "d3"),
      land(tapOf(alertSpec(HOME, "hand", "decision.asked", "d3", "t", link)))
    )
    assertNull(land(tapOf(unpairedSpec(HOME, "hand"))))
    assertNull(land(tapOf(unreadableSpec())))
    assertEquals(Landing.Phone, land(Tap(THIS_PHONE_ACTION, link)))
    assertEquals(Landing.Fleet(HOME), land(Tap("android.intent.action.VIEW", "handboard://open/$HOME/")))
  }

  @Test
  fun onlyTheTapActionSendsThePhoneLinkAndALinkIsPassedOnAsItIs() {
    val link = "handboard://open/$HOME/decision/d3"
    assertEquals(PHONE_LINK, incomingLink(THIS_PHONE_ACTION, null))
    assertEquals(PHONE_LINK, incomingLink(THIS_PHONE_ACTION, link))
    assertEquals(link, incomingLink("android.intent.action.VIEW", link))
    assertEquals(link, incomingLink(null, link))
    assertNull(incomingLink(null, null))
    assertNull(incomingLink("android.intent.action.MAIN", null))
  }

  @Test
  fun aDecisionLinkGoesToTheDecision() {
    val link = "handboard://open/$HOME/decision/d12"
    assertEquals(Landing.Decision(HOME, "d12"), landing(link) { path(HOME, "decision/d12") })
  }

  @Test
  fun theDecisionRefComesFromThePathTheCoreAccepted() {
    assertEquals(
      Landing.Decision(HOME, "d123456789"),
      landing("handboard://open/$HOME/decision/d123456789") { path(HOME, "decision/d123456789") }
    )
    assertEquals(
      Landing.Thread(HOME, listOf("t3", "r5")),
      landing("handboard://open/$HOME/task/t3#r5") { path(HOME, "task/t3#r5") }
    )
  }

  @Test
  fun aFleetLinkGoesToTheFleetScreen() {
    assertEquals(
      Landing.Fleet(HOME),
      landing("handboard://open/$HOME/") { path(HOME, "") }
    )
  }

  @Test
  fun aTaskAttemptOrReportLinkOpensItsNativeScreen() {
    val cases =
      listOf(
        "task/t3" to listOf("t3"),
        "attempt/a7" to listOf("a7"),
        "task/t3#r5" to listOf("t3", "r5")
      )
    for ((page, refs) in cases) {
      assertEquals(
        Landing.Thread(HOME, refs),
        landing("handboard://open/$HOME/$page") { path(HOME, page) }
      )
    }
  }

  @Test
  fun aReportLinkStacksAboveItsTaskAndBackReturnsToTheTask() {
    val thread = landing("handboard://open/$HOME/task/t3#r5") { path(HOME, "task/t3#r5") } as Landing.Thread
    val stack = thread.stack()
    assertEquals("r5", currentTask(stack))
    assertEquals("t3", currentTask(popTask(stack)))
    assertEquals("", popTask(popTask(stack)))
    assertEquals("a7", currentTask(Landing.Thread(HOME, listOf("a7")).stack()))
  }

  @Test
  fun aPathWithALineBreakIsRefusedBeforeItCanSplitTheStack() {
    val page = "task/t3\\nr5"
    assertEquals(Landing.Refused(REJECTED), landing("handboard://open/$HOME/$page") { path(HOME, page) })
  }

  @Test
  fun aPageWithNoNativeScreenKeepsTheWebFallback() {
    assertEquals(
      Landing.Web(HOME, "/$HOME/connect"),
      landing("handboard://open/$HOME/connect") { path(HOME, "connect") }
    )
  }

  @Test
  fun theCoreRefusesWhatItDoesNotAccept() {
    val open = Opener(setOf(HOME))
    val other = "handboard://open/f000000000005/task/t3"
    assertEquals(Landing.Refused(REJECTED), landing(other, open))
    assertEquals(Landing.Refused(REJECTED), landing("https://example.test/$HOME/task/t3", open))
    assertEquals(Landing.Refused(NOT_PAIRED), landing("handboard://open/$HOME/", Opener(emptySet())))
  }

  @Test
  fun anAnswerThatCannotBeReadIsRefused() {
    for (answer in listOf(null, "", "{}", "not json")) {
      assertEquals(
        Landing.Refused(REJECTED),
        landing("handboard://open/$HOME/") { answer }
      )
    }
  }
}
