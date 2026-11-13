package io.github.atqamz.handcompanion

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

private const val HOME = "f000000000003"
private const val NOT_PAIRED = "Pair this phone with your laptop first."
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
      Landing.Web(HOME, "/$HOME/task/t3#r5"),
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
  fun everyOtherPageOpensTheFleetWithTheWebFallback() {
    val cases =
      listOf(
        "task/t3" to "/$HOME/task/t3",
        "attempt/a7" to "/$HOME/attempt/a7",
        "task/t3#r5" to "/$HOME/task/t3#r5"
      )
    for ((page, expected) in cases) {
      assertEquals(
        Landing.Web(HOME, expected),
        landing("handboard://open/$HOME/$page") { path(HOME, page) }
      )
    }
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
