package io.github.atqamz.handcompanion

import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Rule
import org.junit.Test

private val listed = SkillsState(boardJson.decodeFromString(SkillsData.serializer(), skillsJson), loading = false)

private const val BUSY = "another skills request is running on this board; try again when it ends"

class SkillsScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 2400))

  private fun shot(dark: Boolean, state: SkillsState) =
    paparazzi.snapshot { HandTheme(dark) { SkillsScreen(state, {}, {}) } }

  @Test fun listLight() = shot(false, listed)

  @Test fun listDark() = shot(true, listed)

  @Test fun loadingLight() = shot(false, SkillsState())

  @Test fun busyLight() = shot(false, SkillsState(problem = BUSY, loading = false))

  @Test fun busyDark() = shot(true, SkillsState(problem = BUSY, loading = false))

  @Test fun staleLight() = shot(false, listed.copy(problem = "The Board could not be reached."))

  @Test fun unpairedLight() = shot(false, SkillsState(loading = false, unpaired = true))
}
