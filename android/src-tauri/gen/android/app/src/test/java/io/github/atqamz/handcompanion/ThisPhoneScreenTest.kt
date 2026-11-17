package io.github.atqamz.handcompanion

import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Rule
import org.junit.Test

internal val atlas = FleetLine("f000000000003", "atlas", "paired Nov 3 · signs out Feb 1")

internal val several =
  listOf(
    atlas,
    FleetLine("f000000000005", "orchard", "paired Nov 3 · signs out Feb 1"),
    FleetLine("f000000000017", "harbor", "paired Nov 9 · signs out Feb 7"),
    FleetLine("f000000000011", "meadow", "paired Nov 9 · signs out Feb 7")
  )

internal fun phone(fleets: List<FleetLine>) =
  ThisPhone(
    laptop = "4d92…6ed6",
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

  @Test fun oneFleetLight() = shot(false, phone(listOf(atlas)))

  @Test fun oneFleetDark() = shot(true, phone(listOf(atlas)))

  @Test fun severalFleetsLight() = shot(false, phone(several))

  @Test fun severalFleetsDark() = shot(true, phone(several))

  private val off =
    phone(several).copy(alerts = "Alerts are off", alertsDetail = "Allow notifications in Settings.")

  @Test fun alertsOffLight() = shot(false, off)

  @Test fun alertsOffDark() = shot(true, off)

  @Test fun forgetConfirmLight() = shot(false, phone(several).copy(asking = true))

  @Test fun forgetConfirmDark() = shot(true, phone(several).copy(asking = true))
}
