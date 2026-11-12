package io.github.atqamz.handcompanion

import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Rule
import org.junit.Test

private val rows =
  listOf(
    FleetRow(
      "f000000000003",
      "atlas",
      Badge("1 blocked", Tone.Fail),
      5,
      pill = Badge("blocked", Tone.Fail),
      seen = "12:41"
    ),
    FleetRow(
      "f000000000005",
      "orchard",
      Badge("2 waiting", Tone.Wait),
      3,
      pill = Badge("working", Tone.Accent),
      seen = "12:41"
    ),
    FleetRow(
      "f000000000017",
      "harbor",
      clear = true,
      since = "12:41",
      pill = Badge("ready", Tone.Pass),
      seen = "12:41"
    ),
    FleetRow(
      "f000000000011",
      "meadow",
      Badge("1 report", Tone.Neutral),
      1,
      pill = Badge("stopped", Tone.Neutral),
      seen = "12:41"
    )
  )

private val connected = FleetsState("4d92…6ed6", rows)

private val unreachable =
  FleetsState(
    "4d92…6ed6",
    rows,
    Band(
      "Can't reach 4d92…6ed6 since 12:41",
      "Check that the laptop is on, that Hand's Board is running, and that this phone has internet."
    )
  )

class FleetsScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 1500))

  private fun shot(dark: Boolean, state: FleetsState) =
    paparazzi.snapshot { HandTheme(dark) { FleetsScreen(state, {}, {}, {}, {}, {}, {}) } }

  @Test fun fleetsLight() = shot(false, connected)

  @Test fun fleetsDark() = shot(true, connected)

  @Test fun unreachableLight() = shot(false, unreachable)

  @Test fun unreachableDark() = shot(true, unreachable)
}
