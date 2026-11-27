package io.github.atqamz.handcompanion

import android.view.View
import androidx.compose.ui.platform.LocalView
import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

private class Calls {
  val asks = mutableListOf<Forgetting?>()
  var signOuts = 0
  var forgets = 0
  var settings = 0
  var labels = emptyList<String>()
}

class ThisPhoneRouteTest {
  private val frames = Frames()

  @get:Rule
  val paparazzi =
    Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 2200), snapshotHandler = frames)

  private fun shown(phone: ThisPhone, act: Taps.() -> Unit): Calls {
    val calls = Calls()
    lateinit var view: View
    frames.onFrame = {
      val taps = Taps(view)
      calls.labels = taps.labels()
      taps.act()
    }
    paparazzi.snapshot {
      HandTheme(false) {
        view = LocalView.current
        ThisPhoneScreen(phone, {}, { calls.asks += it }, { calls.signOuts++ }, { calls.forgets++ }, { calls.settings++ })
      }
    }
    return calls
  }

  @Test
  fun tappingSignOutRaisesTheQuestionAndDoesNothingElse() {
    val calls = shown(phoneOf(phoneSeveral)) { tap("Sign out") }
    assertEquals(listOf(Forgetting("e1", "atqa-laptop", 4)), calls.asks)
    assertEquals(0, calls.signOuts)
    assertEquals(0, calls.forgets)
  }

  @Test
  fun eachHostSignOutNamesItsOwnHostAndSignOutOfEverythingNamesNone() {
    val two = phoneOf(phoneSeveral, listOf(laptopLine, vpsLine))
    val calls = shown(two) {
      tap("Sign out atqa-laptop")
      tap("Sign out vps-berlin")
      tap("Sign out of everything")
    }
    assertEquals(
      listOf(
        Forgetting("e1", "atqa-laptop", 4),
        Forgetting("e2", "vps-berlin", 1),
        Forgetting(null, null, 4)
      ),
      calls.asks
    )
    assertEquals(0, calls.signOuts)
    assertEquals(0, calls.forgets)
  }

  private val twoHosts =
    PhoneInfo(
      "9ab2…11fe",
      "0.1.0",
      listOf(
        PairedHost("e1", "4d92…6ed6", "atqa-laptop", listOf(PairedFleet("f000000000003", "atlas", 1))),
        PairedHost("e2", "7c1e…a2f9", "vps-berlin", listOf(PairedFleet("f000000000005", "orchard", 1)))
      )
    )

  private val down =
    object : Transport {
      override fun request(fleet: String, post: Boolean, path: String, csrf: String?, body: String?): Raw =
        throw BoardError.Unreachable
    }

  @Test
  fun aHostForgetThatFailsWithTwoHostsIsShownOnThisCompanion() {
    val model = FleetsModel(down, { twoHosts }, hostForgetter = { false })
    runBlocking {
      model.refresh()
      model.forget(Forgetting("e2", "vps-berlin", 1))
    }
    val calls = shown(thisPhone(model.state.value, "0.2.0")) {}
    assertTrue(FORGET_FAILED in calls.labels)
    assertTrue("Sign out vps-berlin" in calls.labels)
  }

  @Test
  fun aSignOutThatFailsKeepsTheQuestionAndCarriesItsSentenceToTheScreen() {
    val model =
      FleetsModel(
        down,
        { twoHosts },
        signer = { SignOutResult.Failed(listOf(SignOutMiss("f000000000005", "orchard", "unreachable"))) }
      )
    runBlocking {
      model.refresh()
      model.ask(Forgetting("e2", "vps-berlin", 1))
      model.signOut(Forgetting("e2", "vps-berlin", 1))
    }
    val phone = thisPhone(model.state.value, "0.2.0")
    assertEquals(Forgetting("e2", "vps-berlin", 1), phone.asking)
    assertEquals(model.state.value.failure, phone.failure)
    assertEquals(false, phone.signing)
    assertEquals(2, phone.hosts.size)
  }

  @Test
  fun theDialogOffersSignOutThenTryAgainThenOnlyForgetOnlyWhenRetryCannotHelp() {
    assertEquals(SignOutChoices("Sign out", false, true, true), signOutChoices(null, false))
    assertEquals(SignOutChoices("Sign out", false, true, false), signOutChoices(null, true))
    val silent = SignOutFailure("did not answer", true)
    assertEquals(SignOutChoices("Try again", false, true, true), signOutChoices(silent, false))
    val old = SignOutFailure(SIGN_OUT_TOO_OLD, false)
    assertEquals(SignOutChoices("Forget only", true, false, true), signOutChoices(old, false))
  }

  @Test
  fun oneHostHasNoSecondWayToSignOutOfEverything() {
    val calls = shown(phoneOf(phoneSeveral)) {}
    assertTrue("Sign out of everything" !in calls.labels)
  }

  @Test
  fun noFleetLineOffersASignOutOfItsOwn() {
    val calls = shown(phoneOf(phoneSeveral)) {}
    assertTrue(calls.labels.none { it == "Sign out atlas" || it == "Forget atlas" })
    assertTrue("atlas" in calls.labels)
  }

  @Test
  fun settingsOpensTheNotificationSettingsAndNothingElse() {
    val calls = shown(phoneOf(phoneSeveral)) { tap("Settings") }
    assertEquals(1, calls.settings)
    assertTrue(calls.asks.isEmpty())
  }

  @Test
  fun theScreenIsThePhoneAndNeverAWebPage() {
    val calls = shown(phoneOf(phoneSeveral)) {}
    assertTrue("This Companion" in calls.labels)
  }
}
