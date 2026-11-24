package io.github.atqamz.handcompanion

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Surface
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Rule
import org.junit.Test

private val choices =
  listOf(
    ModelChoice("opus", "Opus · opus", true),
    ModelChoice("sonnet", "Sonnet · sonnet", false),
    ModelChoice("haiku", "haiku", false)
  )

class ModelMenuScreenTest {
  @get:Rule val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 900))

  private fun shot(dark: Boolean, state: ModelsState) =
    paparazzi.snapshot {
      HandTheme(dark) {
        Surface(color = LocalHand.current.bg) {
          Column(Modifier.padding(16.dp)) { ModelMenuBody(ModelsUi(state, {}, {})) }
        }
      }
    }

  private val open = ModelsState(supported = true, switchable = true, choices = choices)

  @Test fun menuLight() = shot(false, open)

  @Test fun menuDark() = shot(true, open)

  @Test fun switchingLight() = shot(false, open.copy(switching = "sonnet", pending = "sonnet high"))

  @Test fun switchingDark() = shot(true, open.copy(switching = "sonnet", pending = "sonnet high"))

  @Test fun refreshingLight() = shot(false, open.copy(refreshing = true))

  @Test fun refreshingDark() = shot(true, open.copy(refreshing = true))

  @Test
  fun errorLight() = shot(false, open.copy(error = "s2 is on screen, but s3 is running now; refresh and try again"))

  @Test
  fun errorDark() = shot(true, open.copy(error = "s2 is on screen, but s3 is running now; refresh and try again"))
}
