package io.github.atqamz.handcompanion

import java.util.concurrent.CountDownLatch
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private const val REJECTED = "This is not a Hand pairing code. Show a new one on your laptop."
private const val NEWER = "This code needs a newer Hand app. Update the app."
private const val WRONG =
  "That code is not right. Check it against the one on your laptop, or show a new code there."
private const val OFFERED = """{"laptop":"7c1e…a2f9","replaces":false}"""
private const val REPLACES = """{"laptop":"7c1e…a2f9","replaces":true}"""
private const val PAIRED = """{"fleets":[{"id":"f000000000003","name":"atlas"}]}"""

private fun failure(name: String, message: String) =
  """{"failure":"$name","message":"$message"}"""

private class FakeCore(var offer: String? = OFFERED, var pair: String? = PAIRED) : Pairing {
  val calls = mutableListOf<String>()

  override fun offer(link: String): String? {
    calls += "offer"
    return offer
  }

  val entered = CountDownLatch(1)
  var hold: CountDownLatch? = null

  override fun pair(name: String): String? {
    calls += "pair $name"
    entered.countDown()
    hold?.await()
    return pair
  }

  override fun cancel() {
    calls += "cancel"
  }
}

private val four = Current("4d92…6ed6", listOf("hand", "work", "research", "homelab"))

private fun model(core: FakeCore, current: () -> Current = { four }) =
  PairModel(core, "Pixel 8", current)

class PairModelTest {
  @Test
  fun theNameStartsAsThePhonesModel() {
    assertEquals("Pixel 8", model(FakeCore()).state.value.name)
  }

  @Test
  fun anAcceptedLinkOpensTheConfirmationAndIsNotKept() {
    val core = FakeCore()
    val model = model(core) { throw AssertionError("the same laptop loses nothing") }
    model.edit("handboard://c#v=1")
    model.offer("handboard://c#v=1")
    val state = model.state.value
    assertEquals("7c1e…a2f9", state.confirm?.laptop)
    assertNull(state.confirm?.notice)
    assertEquals("Pair", state.confirm?.action)
    assertEquals("", state.link)
    assertNull(state.error)
  }

  @Test
  fun aRejectedLinkShowsTheContractsSentenceUnderTheFieldAndOpensNothing() {
    val core = FakeCore(offer = failure("link", REJECTED))
    val model = model(core)
    model.offer("not a link")
    assertEquals(REJECTED, model.state.value.error)
    assertEquals("not a link", model.state.value.link)
    assertNull(model.state.value.confirm)
    model.edit("handboard")
    assertNull(model.state.value.error)
    core.offer = failure("link", NEWER)
    model.offer("handboard://c#v=2")
    assertEquals(NEWER, model.state.value.error)
  }

  @Test
  fun aLinkToAnotherLaptopWarnsByNameAndAsksToReplace() {
    val model = model(FakeCore(offer = REPLACES))
    model.offer("link")
    val confirm = model.state.value.confirm!!
    assertEquals("Replace and pair", confirm.action)
    assertEquals(
      "Once paired, this phone leaves 4d92…6ed6 and its 4 fleets: hand, work, research and homelab. " +
        "Their alerts stop. If pairing fails, nothing changes.",
      confirm.notice
    )
  }

  @Test
  fun theNoticeReadsRightForOneFleetAndForNone() {
    assertEquals(
      "Once paired, this phone leaves 4d92…6ed6 and its 1 fleet: hand. " +
        "Its alerts stop. If pairing fails, nothing changes.",
      replaceNotice(Current("4d92…6ed6", listOf("hand")))
    )
    assertEquals(
      "Once paired, this phone leaves 4d92…6ed6 and its 2 fleets: hand and work. " +
        "Their alerts stop. If pairing fails, nothing changes.",
      replaceNotice(Current("4d92…6ed6", listOf("hand", "work")))
    )
    assertEquals(
      "Once paired, this phone leaves 4d92…6ed6. If pairing fails, nothing changes.",
      replaceNotice(Current("4d92…6ed6", emptyList()))
    )
    assertEquals(
      "Once paired, this phone leaves your current laptop. If pairing fails, nothing changes.",
      replaceNotice(Current(null, emptyList()))
    )
  }

  @Test
  fun aNameHasNoControlCharacterAndAtMostFortyCharacters() {
    assertEquals("Pixel 8", cleanName("Pi\nxel\u0007 8"))
    assertEquals(40, cleanName("x".repeat(60)).length)
    val model = model(FakeCore())
    model.rename("Ana's phone\t")
    assertEquals("Ana's phone", model.state.value.name)
  }

  @Test
  fun aPairingSendsTheNameAndOnSuccessEndsTheFlow() {
    val core = FakeCore()
    val model = model(core)
    model.offer("link")
    model.rename("  Ana's phone ")
    runBlocking { model.submit() }
    assertEquals(listOf("offer", "pair   Ana's phone "), core.calls)
    val state = model.state.value
    assertTrue(state.done)
    assertNull(state.confirm)
    assertFalse(state.busy)
    assertNull(state.error)
    model.finish()
    assertFalse(model.state.value.done)
  }

  @Test
  fun aFailedPairingSaysWhyKeepsTheConfirmationAndCanBeTriedAgain() {
    val core = FakeCore(pair = failure("wrong_code", WRONG))
    val model = model(core)
    model.offer("link")
    runBlocking { model.submit() }
    var state = model.state.value
    assertEquals(WRONG, state.error)
    assertFalse(state.done)
    assertFalse(state.busy)
    assertEquals("7c1e…a2f9", state.confirm?.laptop)
    core.pair = PAIRED
    runBlocking { model.submit() }
    state = model.state.value
    assertTrue(state.done)
    assertNull(state.error)
  }

  @Test
  fun everyFailureTheCoreNamesShowsItsOwnSentence() {
    for (message in
      listOf(
        "That code has expired. A code works for 5 minutes. Show a new code on your laptop.",
        "That code was already used. A code works once. Show a new code on your laptop.",
        "Too many wrong codes. Every code was cancelled. Show a new code on your laptop.",
        "Too many wrong codes from this phone. Wait a minute, then try again.",
        "Your laptop is busy. Try again in a moment.",
        "Can't reach your laptop. Check that it is on, that Hand's Board is running, and that this phone has internet."
      )) {
      val model = model(FakeCore(pair = failure("x", message)))
      model.offer("link")
      runBlocking { model.submit() }
      assertEquals(message, model.state.value.error)
      assertFalse(model.state.value.done)
    }
  }

  @Test
  fun anAnswerTheAppCannotReadIsNotTakenForSuccess() {
    for (reply in listOf(null, "", "{", """{"unrelated":1}""")) {
      val model = model(FakeCore(pair = reply))
      model.offer("link")
      runBlocking { model.submit() }
      assertEquals(UNREAD, model.state.value.error)
      assertFalse(model.state.value.done)
    }
  }

  @Test
  fun aSecondLinkWhileConfirmingKeepsTheFirstAndShowsTheCoresSentence() {
    val core = FakeCore()
    val model = model(core)
    model.offer("first")
    core.offer = failure("link", "Finish or dismiss the current pairing first")
    model.offer("second")
    assertEquals("7c1e…a2f9", model.state.value.confirm?.laptop)
    assertEquals("Finish or dismiss the current pairing first", model.state.value.error)
  }

  @Test
  fun cancellingClosesThePairingInTheCoreAndKeepsTheName() {
    val core = FakeCore()
    val model = model(core)
    model.offer("link")
    model.rename("Ana")
    model.cancel()
    assertEquals(listOf("offer", "cancel"), core.calls)
    assertEquals(PairState(name = "Ana"), model.state.value)
  }

  @Test
  fun nothingIsSentWithoutAConfirmation() {
    val core = FakeCore()
    val model = model(core)
    runBlocking { model.submit() }
    assertTrue(core.calls.isEmpty())
  }

  @Test
  fun cancellingOrOfferingWhileAPairingIsInFlightChangesNothing() {
    val core = FakeCore()
    val hold = CountDownLatch(1)
    core.hold = hold
    val model = model(core)
    model.offer("link")
    runBlocking {
      val job = launch(Dispatchers.Default) { model.submit() }
      core.entered.await()
      model.cancel()
      model.offer("another")
      assertTrue(model.state.value.busy)
      assertEquals("7c1e…a2f9", model.state.value.confirm?.laptop)
      assertEquals(listOf("offer", "pair Pixel 8"), core.calls)
      hold.countDown()
      job.join()
    }
    assertTrue(model.state.value.done)
  }
}
