package io.github.atqamz.handcompanion

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import kotlinx.coroutines.runBlocking
import org.junit.Rule
import org.junit.Test

private val rows =
  listOf(
    FleetRow("f000000000003", "atlas", Badge("1 blocked", Tone.Fail), 5, pill = Badge("blocked", Tone.Fail), seen = "12:41"),
    FleetRow("f000000000005", "orchard", Badge("2 waiting", Tone.Wait), 3, pill = Badge("working", Tone.Accent), seen = "12:41")
  )

private val connected = FleetsState("4d92…6ed6", rows, loaded = true)

private val fleet =
  FleetState(
    name = "atlas",
    pill = Badge("blocked", Tone.Fail),
    loaded = true,
    tab = Tab.Chat,
    cleared = "12:41"
  )

@Composable
private fun Over(menu: List<Pair<String, () -> Unit>>, screen: @Composable () -> Unit) {
  Box(Modifier.fillMaxSize()) {
    screen()
    Box(Modifier.align(Alignment.TopEnd).padding(top = 52.dp, end = 8.dp)) {
      Surface(
        color = MaterialTheme.colorScheme.surfaceContainer,
        shape = MaterialTheme.shapes.medium,
        shadowElevation = 8.dp
      ) {
        Column(Modifier.width(IntrinsicSize.Max)) { MenuItems(menu) }
      }
    }
  }
}

class FallbackScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 1500))

  private fun shot(dark: Boolean, content: @Composable () -> Unit) =
    paparazzi.snapshot { HandTheme(dark) { content() } }

  private val fleets: @Composable (FleetsState) -> Unit = { FleetsScreen(it, {}, {}, {}, {}, {}, {}) }

  private val web: @Composable () -> Unit = {
    FallbackScreen("f000000000003", "atlas", "/f000000000003/task/t40#r91", {}) {
      Box(Modifier.fillMaxSize().background(LocalHand.current.bg).padding(16.dp)) {
        Meta("The Board's page loads here.")
      }
    }
  }

  @Test fun fallbackLight() = shot(false, web)

  @Test fun fallbackDark() = shot(true, web)

  private val webLoading: @Composable () -> Unit = {
    FallbackScreen("f000000000003", "atlas", "/f000000000003/task/t40#r91", {}) { BoardBody(null, {}) {} }
  }

  @Test fun webLoadingLight() = shot(false, webLoading)

  @Test fun webLoadingDark() = shot(true, webLoading)

  private val notPairedHere: Page =
    runBlocking { openBoard("/f000000000003/", { "{}" }, Cookies { _, _ -> true }) }

  private val cookieRefused: Page =
    runBlocking {
      openBoard(
        "/f000000000003/",
        { """{"port":50123,"cookie":"hand_phone_f000000000003=7.0"}""" },
        Cookies { _, _ -> false }
      )
    }

  private fun failed(page: Page): @Composable () -> Unit = {
    FallbackScreen("f000000000003", "atlas", "/f000000000003/task/t40#r91", {}) {
      BoardBody(page, {}, onRetry = {}) {}
    }
  }

  @Test fun notPairedHereLight() = shot(false, failed(notPairedHere))

  @Test fun notPairedHereDark() = shot(true, failed(notPairedHere))

  @Test fun cookieRefusedLight() = shot(false, failed(cookieRefused))

  @Test fun cookieRefusedDark() = shot(true, failed(cookieRefused))

  private val webTimedOut: @Composable () -> Unit = {
    FallbackScreen("f000000000003", "atlas", "/f000000000003/task/t40#r91", {}) {
      BoardBody(Page.Failed(BOARD_TIMED_OUT, retry = true), {}, onRetry = {}) {}
    }
  }

  @Test fun webTimedOutLight() = shot(false, webTimedOut)

  @Test fun webTimedOutDark() = shot(true, webTimedOut)

  @Test
  fun fleetMenuLight() =
    shot(false) {
      Over(listOf("Open web Board" to {})) { FleetScreen("f000000000003", fleet, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}) {} }
    }

  @Test
  fun fleetMenuDark() =
    shot(true) {
      Over(listOf("Open web Board" to {})) { FleetScreen("f000000000003", fleet, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}) {} }
    }

  @Test fun listMenuLight() = shot(false) { Over(listOf("Forget this laptop" to {})) { fleets(connected) } }

  @Test fun listMenuDark() = shot(true) { Over(listOf("Forget this laptop" to {})) { fleets(connected) } }

  @Test fun forgetLight() = shot(false) { fleets(connected.copy(asking = true)) }

  @Test fun forgetDark() = shot(true) { fleets(connected.copy(asking = true)) }

  @Test fun forgetFailedLight() = shot(false) { fleets(connected.copy(notice = FORGET_FAILED)) }

  @Test fun alertsOffLight() = shot(false) { fleets(connected.copy(alertsOff = true)) }

  @Test fun alertsOffDark() = shot(true) { fleets(connected.copy(alertsOff = true)) }

  @Test
  fun refusedLinkLight() =
    shot(false) {
      Box(Modifier.fillMaxSize()) {
        FleetScreen("f000000000003", fleet, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}) {}
        Snack("This is not a link Hand can open.", Modifier.align(Alignment.BottomCenter).padding(bottom = TABS_HEIGHT))
      }
    }

  @Test
  fun refusedLinkDark() =
    shot(true) {
      Box(Modifier.fillMaxSize()) {
        FleetScreen("f000000000003", fleet, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}) {}
        Snack("This is not a link Hand can open.", Modifier.align(Alignment.BottomCenter).padding(bottom = TABS_HEIGHT))
      }
    }

  @Test fun startFailureLight() = shot(false) { StartFailure("the phone's key could not start the companion: the Keystore is unavailable") }

  @Test fun startFailureDark() = shot(true) { StartFailure("the phone's key could not start the companion: the Keystore is unavailable") }
}
