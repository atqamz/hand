package io.github.atqamz.handcompanion

import java.io.File
import org.junit.Assert.assertFalse
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
  fun aDocumentReleasesItsMediaWhenTheActivityStops() {
    assertTrue(
      body(activity, "private fun Doc(").contains("LifecycleEventEffect(Lifecycle.Event.ON_STOP) { model.release() }")
    )
  }

  @Test
  fun theThisPhoneScreenHandsEachCallbackToTheDialogUnderItsOwnName() {
    val screen = File("$sources/ThisPhoneScreen.kt").readText()
    val call = screen.substringAfter("phone.asking?.let {").substringBefore("\n}\n")
    for (line in listOf("onSignOut = onSignOut,", "onForget = onForget,", "onCancel = { onAsk(null) }")) {
      assertTrue(line, call.lines().any { it.trim() == line })
    }
    val dialog = body(screen, "internal fun SignOutDialog(")
    assertTrue(dialog.contains("signOutContent(asking, failure, signing, onSignOut = onSignOut, onForget = onForget, onCancel = onCancel)"))
    assertTrue(dialog.contains("onDismissRequest = { if (!signing) onCancel() },"))
    assertTrue(dialog.contains("confirmButton = content.confirm,"))
    assertTrue(dialog.contains("dismissButton = content.dismiss"))
  }

  @Test
  fun aStoreThatWillNotClearAfterASignOutChecksTheAlertsLikeAFailedForget() {
    val effect = activity.substringAfter("LaunchedEffect(listed.notice, listed.failure)").substringBefore("var refused")
    assertTrue(effect.contains("listed.notice == FORGET_FAILED"))
    assertTrue(effect.contains("listed.failure?.sentence == FORGET_FAILED"))
    assertTrue(effect.contains("checkAlerts()"))
  }

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
  fun aThreadLinkStacksItsRefsThroughTheTaskStack() {
    val thread = activity.substringAfter("is Landing.Thread ->").substringBefore("is Landing.Unshown")
    assertTrue(thread.contains("threads = to.stack()"))
  }

  @Test
  fun theNotificationBuildsItsIntentFromTheTap() {
    val open = body(notifier, "private fun open")
    assertTrue(open.contains("tapOf(spec)"))
    assertTrue(open.contains(".setAction(tap.action)"))
  }

  @Test
  fun theSkillsReadStartsInTheModelsScopeSoARotationCannotCancelIt() {
    val skills = body(activity, "private fun Skills(")
    assertTrue(skills.contains("model.reload()"))
    assertFalse(skills.contains("model.load()"))
  }

  @Test
  fun theSkillsSyncStartsInTheModelsScopeSoARotationCannotCancelIt() {
    val skills = body(activity, "private fun Skills(")
    assertTrue(skills.contains("model.sync(it.scope, it.project)"))
    assertFalse(skills.contains("runSync"))
  }

  @Test
  fun theFleetMenuOpensTheSkillsScreen() {
    assertTrue(activity.contains("onSkills = { skills = open }"))
  }

  private fun squashed(text: String) = text.replace(Regex("\\s+"), "")

  @Test
  fun anUnshownPageShowsItsFleetAndNamesThePageInTheNotice() {
    val branch = activity.substringAfter("is Landing.Unshown ->").substringBefore("is Landing.Refused")
    assertTrue(branch.contains("show(to.fleet)"))
    assertTrue(branch.contains("refused = notShown(to.what)"))
  }

  @Test
  fun aRefusedLinkShowsTheCoresMessage() {
    val branch = activity.substringAfter("is Landing.Refused ->").lineSequence().first()
    assertTrue(branch.contains("refused = to.message"))
  }

  @Test
  fun theQueueNoticeCallbackSetsTheNotice() {
    val call = squashed(activity.substringAfter("holder.SaveableStateProvider(\"fleet:\$open\")").substringBefore("chat ="))
    assertTrue(call.contains("{threads=pushTask(threads,it)},{refused=it},{threads=pushTask(threads,it)}"))
  }

  @Test
  fun theNoticeIsDrawnFromTheRefusedState() {
    assertTrue(squashed(activity).contains("refused?.let{Refusal(it,tabs)}"))
  }

  @Test
  fun everyMarkdownLinkSpanAsksTheLinkTarget() {
    val markdown = File("$sources/MarkdownView.kt").readText()
    assertTrue(Regex("""link\(linkTarget\(span\),""").findAll(markdown).count() == 3)
    assertFalse(markdown.contains("ThreadTarget.Browser("))
    assertTrue(squashed(body(markdown, "internal fun linkTarget")).contains("isMdLink->externalTarget(span.url)"))
  }
}
