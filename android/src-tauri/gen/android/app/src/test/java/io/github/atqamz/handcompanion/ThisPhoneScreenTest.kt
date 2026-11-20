package io.github.atqamz.handcompanion

import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Rule
import org.junit.Test

internal val phoneAtlas = FleetLine("f000000000003", "atlas", "paired Nov 3 · signs out Feb 1")

internal val phoneSeveral =
  listOf(
    phoneAtlas,
    FleetLine("f000000000005", "orchard", "paired Nov 3 · signs out Feb 1"),
    FleetLine("f000000000017", "harbor", "paired Nov 9 · signs out Feb 7"),
    FleetLine("f000000000011", "meadow", "paired Nov 9 · signs out Feb 7")
  )

internal val laptopLine = HostLine("e1", "atqa-laptop", "4d92…6ed6", "connected", 4)

internal val vpsLine = HostLine("e2", "vps-berlin", "7c1e…a2f9", "not connected", 1)

internal fun phoneOf(fleets: List<FleetLine>, hosts: List<HostLine> = listOf(laptopLine.copy(fleets = fleets.size))) =
  ThisPhone(
    hosts = hosts,
    fleets = fleets,
    alertsDetail = "Hand is connected · watching ${fleets.size} fleet${if (fleets.size == 1) "" else "s"}",
    phoneId = "9ab2…11fe",
    app = "0.2.0",
    core = "0.1.0"
  )

class ThisPhoneScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 2200))

  private fun shot(dark: Boolean, phone: ThisPhone) =
    paparazzi.snapshot { HandTheme(dark) { ThisPhoneScreen(phone, {}, {}, {}, {}, {}) } }

  @Test fun oneFleetLight() = shot(false, phoneOf(listOf(phoneAtlas)))

  @Test fun oneFleetDark() = shot(true, phoneOf(listOf(phoneAtlas)))

  @Test fun severalFleetsLight() = shot(false, phoneOf(phoneSeveral))

  @Test fun severalFleetsDark() = shot(true, phoneOf(phoneSeveral))

  private val off =
    phoneOf(phoneSeveral).copy(alerts = "Alerts are off", alertsDetail = "Allow notifications in Settings.")

  @Test fun alertsOffLight() = shot(false, off)

  @Test fun alertsOffDark() = shot(true, off)

  @Test fun forgetConfirmLight() = shot(false, phoneOf(phoneSeveral).copy(asking = Forgetting("e1", "atqa-laptop", 4)))

  @Test fun forgetConfirmDark() = shot(true, phoneOf(phoneSeveral).copy(asking = Forgetting("e1", "atqa-laptop", 4)))

  private val two = phoneOf(phoneSeveral + FleetLine("f000000000021", "ops", "paired Nov 9 · signs out Feb 7"), listOf(laptopLine, vpsLine))

  @Test fun hostsLight() = shot(false, two)

  @Test fun hostsDark() = shot(true, two)
}
