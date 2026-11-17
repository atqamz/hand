package io.github.atqamz.handcompanion

import java.io.File
import org.junit.Assert.assertTrue
import org.junit.Test

private val sources = "src/main/java/io/github/atqamz/handcompanion"

private fun body(source: String, signature: String): String {
  val start = source.indexOf(signature)
  check(start >= 0) { signature }
  val open = source.indexOf('{', start)
  var depth = 0
  var end = open
  do {
    when (source[end]) {
      '{' -> depth++
      '}' -> depth--
    }
    end++
  } while (depth > 0)
  return source.substring(open, end)
}

class HandActivityWiringTest {
  private val activity = File("$sources/HandActivity.kt").readText()
  private val notifier = File("$sources/Notifier.kt").readText()

  @Test
  fun aNewIntentFromTheBackgroundIsTakenLikeTheFirstOne() {
    assertTrue(body(activity, "override fun onNewIntent").contains("take(intent)"))
  }

  @Test
  fun aFirstLaunchTakesItsIntent() {
    assertTrue(body(activity, "override fun onCreate").contains("if (savedInstanceState == null) take(intent)"))
  }

  @Test
  fun takeSendsTheActionThroughTheRoute() {
    assertTrue(
      body(activity, "private fun take").contains("incomingLink(intent.action, intent.data?.toString())")
    )
  }

  @Test
  fun thePhoneLandingShowsThePhoneAndTheRouteIsFedItsSigningState() {
    assertTrue(body(activity, "Landing.Phone ->").contains("phone = true"))
    assertTrue(activity.contains("{ signing = it }"))
  }

  @Test
  fun theNotificationBuildsItsIntentFromTheTap() {
    val open = body(notifier, "private fun open")
    assertTrue(open.contains("tapOf(spec)"))
    assertTrue(open.contains(".setAction(tap.action)"))
  }
}
