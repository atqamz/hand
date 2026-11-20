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

private const val LAPTOP = "4d92000000000000000000000000000000000000000000000000000000006ed6"
private const val VPS = "7c1e00000000000000000000000000000000000000000000000000000000a2f9"
private val DOWN =
  Band(
    "Can't reach 4d92…6ed6 since 12:41",
    "Check that the host is on, that Hand's Board is running, and that this phone has internet."
  )

private fun laptop(rows: List<FleetRow>, band: Band? = null, name: String = "atqa-laptop") =
  HostGroup(LAPTOP, "4d92…6ed6", name, rows, band)

private val ops =
  FleetRow(
    "f000000000021",
    "ops",
    clear = true,
    since = "09:12",
    pill = Badge("ready", Tone.Pass),
    seen = "09:12"
  )

private fun vps(rows: List<FleetRow>, band: Band? = null) =
  HostGroup(VPS, "7c1e…a2f9", "vps-berlin", rows, band)

private val connected = FleetsState(listOf(laptop(rows, name = "4d92…6ed6")), loaded = true)

private val unreachable =
  FleetsState(listOf(laptop(rows, DOWN, "4d92…6ed6")), loaded = true)

private val twoHosts =
  FleetsState(listOf(laptop(rows.take(2)), vps(listOf(rows[2], rows[3]))), loaded = true)

private val oneDead =
  FleetsState(
    listOf(
      laptop(rows.take(2)),
      vps(listOf(ops), Band("Can't reach vps-berlin since 09:12", "Check."))
    ),
    loaded = true
  )

private val unpaired =
  FleetsState(
    listOf(
      laptop(rows.take(2)),
      vps(listOf(ops.copy(paired = false, clear = false), rows[3].copy(paired = false)))
    ),
    loaded = true
  )

private val oneUnpaired =
  FleetsState(listOf(laptop(listOf(rows[0], rows[1].copy(paired = false)))), loaded = true)

private val loading = FleetsState()

class FleetsScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 1500))

  private fun shot(dark: Boolean, state: FleetsState) =
    paparazzi.snapshot { HandTheme(dark) { FleetsScreen(state, {}, {}, {}, {}, {}, {}) } }

  @Test fun fleetsLight() = shot(false, connected)

  @Test fun fleetsDark() = shot(true, connected)

  @Test fun unreachableLight() = shot(false, unreachable)

  @Test fun unreachableDark() = shot(true, unreachable)

  @Test fun loadingLight() = shot(false, loading)

  @Test fun loadingDark() = shot(true, loading)

  @Test fun twoHostsLight() = shot(false, twoHosts)

  @Test fun twoHostsDark() = shot(true, twoHosts)

  @Test fun oneDeadHostLight() = shot(false, oneDead)

  @Test fun oneDeadHostDark() = shot(true, oneDead)

  @Test fun everyFleetUnpairedLight() = shot(false, unpaired)

  @Test fun everyFleetUnpairedDark() = shot(true, unpaired)

  @Test fun oneFleetUnpairedLight() = shot(false, oneUnpaired)

  @Test fun oneFleetUnpairedDark() = shot(true, oneUnpaired)
}
