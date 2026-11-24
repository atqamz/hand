package io.github.atqamz.handcompanion

import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import java.util.TimeZone
import org.junit.Rule
import org.junit.Test

private val utc: TimeZone = TimeZone.getTimeZone("UTC")

private val listed =
  AlertsState(data = alertsNewestFirst(alertsData), loading = false)

private val empty =
  AlertsState(
    data = boardJson.decodeFromString(AlertsData.serializer(), alertsJson(emptyList(), EMPTY_STATS)),
    loading = false
  )

class AlertsScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 2400))

  private fun shot(dark: Boolean, state: AlertsState) =
    paparazzi.snapshot { HandTheme(dark) { AlertsScreen(state, {}, {}, utc) } }

  @Test fun rowsLight() = shot(false, listed)

  @Test fun rowsDark() = shot(true, listed)

  @Test fun emptyLight() = shot(false, empty)

  @Test fun emptyDark() = shot(true, empty)

  @Test fun loadingLight() = shot(false, AlertsState())

  @Test
  fun errorLight() = shot(false, AlertsState(loading = false, problem = "The Board could not be reached."))

  @Test
  fun errorDark() = shot(true, AlertsState(loading = false, problem = "The Board could not be reached."))

  @Test
  fun staleLight() = shot(false, listed.copy(problem = "The Board could not be reached."))

  @Test fun oldHostLight() = shot(false, AlertsState(loading = false, tooOld = true))

  @Test fun oldHostDark() = shot(true, AlertsState(loading = false, tooOld = true))
}
