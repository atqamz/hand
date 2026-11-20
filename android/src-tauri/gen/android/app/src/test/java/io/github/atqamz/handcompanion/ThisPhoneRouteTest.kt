package io.github.atqamz.handcompanion

import android.view.View
import androidx.compose.material3.Text
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
  var forgets = 0
  val signings = mutableListOf<String?>()
  var settings = 0
  val pages = mutableSetOf<String>()
  var labels = emptyList<String>()
}

class ThisPhoneRouteTest {
  private val frames = Frames()

  @get:Rule
  val paparazzi =
    Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 2200), snapshotHandler = frames)

  private fun shown(phone: ThisPhone, signing: String?, act: Taps.() -> Unit): Calls {
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
        ThisPhoneRoute(
          phone,
          signing,
          { calls.signings += it },
          {},
          { calls.asks += it },
          { calls.forgets++ },
          { calls.settings++ }
        ) { path ->
          calls.pages += path
          Text(path)
        }
      }
    }
    return calls
  }

  @Test
  fun tappingForgetRaisesTheQuestionAndDoesNotForget() {
    val calls = shown(phoneOf(phoneSeveral), null) { tap("Forget") }
    assertEquals(listOf(Forgetting("e1", "atqa-laptop", 4)), calls.asks)
    assertEquals(0, calls.forgets)
    assertTrue(calls.signings.isEmpty())
  }

  @Test
  fun eachHostForgetNamesItsOwnHostAndForgetEverythingNamesNone() {
    val two = phoneOf(phoneSeveral, listOf(laptopLine, vpsLine))
    val calls = shown(two, null) {
      tap("Forget atqa-laptop")
      tap("Forget vps-berlin")
      tap("Forget everything")
    }
    assertEquals(
      listOf(
        Forgetting("e1", "atqa-laptop", 4),
        Forgetting("e2", "vps-berlin", 1),
        Forgetting(null, null, 4)
      ),
      calls.asks
    )
    assertEquals(0, calls.forgets)
  }

  @Test
  fun aHostForgetThatFailsWithTwoHostsIsShownOnThisCompanion() {
    val two =
      PhoneInfo(
        "9ab2…11fe",
        "0.1.0",
        listOf(
          PairedHost("e1", "4d92…6ed6", "atqa-laptop", listOf(PairedFleet("f000000000003", "atlas", 1))),
          PairedHost("e2", "7c1e…a2f9", "vps-berlin", listOf(PairedFleet("f000000000005", "orchard", 1)))
        )
      )
    val down = object : Transport {
      override fun request(fleet: String, post: Boolean, path: String, csrf: String?, body: String?): Raw =
        throw BoardError.Unreachable
    }
    val model = FleetsModel(down, { two }, hostForgetter = { false })
    runBlocking {
      model.refresh()
      model.forget(Forgetting("e2", "vps-berlin", 1))
    }
    val calls = shown(thisPhone(model.state.value, "0.2.0"), null) {}
    assertTrue(FORGET_FAILED in calls.labels)
    assertTrue("Forget vps-berlin" in calls.labels)
  }

  @Test
  fun oneHostHasNoSecondWayToForgetEverything() {
    val calls = shown(phoneOf(phoneSeveral), null) {}
    assertTrue("Forget everything" !in calls.labels)
  }

  @Test
  fun eachSignOutNamesItsOwnFleet() {
    val calls = shown(phoneOf(phoneSeveral), null) {
      tap("Sign out of atlas")
      tap("Sign out of harbor")
    }
    assertEquals(listOf("f000000000003", "f000000000017"), calls.signings)
    assertTrue(calls.asks.isEmpty())
    assertEquals(0, calls.forgets)
  }

  @Test
  fun settingsOpensTheNotificationSettingsAndNothingElse() {
    val calls = shown(phoneOf(phoneSeveral), null) { tap("Settings") }
    assertEquals(1, calls.settings)
    assertTrue(calls.asks.isEmpty())
    assertTrue(calls.signings.isEmpty())
  }

  @Test
  fun aFleetBeingSignedOutShowsItsPhonePageInTheFallbackAndCloseGoesBack() {
    val calls = shown(phoneOf(phoneSeveral), "f000000000005") { tap("Close") }
    assertEquals(setOf("/f000000000005/connect"), calls.pages)
    assertTrue("orchard · connect" in calls.labels)
    assertTrue("Forget" !in calls.labels)
    assertEquals(listOf<String?>(null), calls.signings)
  }

  @Test
  fun noFleetBeingSignedOutShowsThePhoneAndNoWebPage() {
    val calls = shown(phoneOf(phoneSeveral), null) {}
    assertTrue(calls.pages.isEmpty())
    assertTrue("This Companion" in calls.labels)
  }
}
