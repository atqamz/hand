package io.github.atqamz.handcompanion

import androidx.compose.runtime.Composable
import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Rule
import org.junit.Test

private val fleets = Current("4d92…6ed6", listOf("hand", "work", "research", "homelab"))
private val plain = PairState(name = "Pixel 8", confirm = Confirm("7c1e…a2f9", null))
private val replacing =
  PairState(name = "Pixel 8", confirm = Confirm("7c1e…a2f9", replaceNotice(fleets)))
private val failed =
  plain.copy(
    error = "That code has expired. A code works for 5 minutes. Show a new code on your laptop."
  )
private val rejected =
  PairState(
    link = "hello",
    error = "This is not a Hand pairing code. Show a new one on your laptop."
  )

class PairScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 1500))

  private fun shot(dark: Boolean, content: @Composable () -> Unit) =
    paparazzi.snapshot { HandTheme(dark) { content() } }

  private fun add(state: PairState): @Composable () -> Unit = {
    AddFleetScreen(state, {}, {}, {}, {})
  }

  private fun confirm(state: PairState): @Composable () -> Unit = {
    ConfirmScreen(state, {}, {}, {})
  }

  private fun scan(state: Scan): @Composable () -> Unit = { ScanScreen(state, {}, {}, {}, {}, {}) {} }

  @Test fun addLight() = shot(false, add(PairState(name = "Pixel 8")))

  @Test fun addDark() = shot(true, add(PairState(name = "Pixel 8")))

  @Test fun addRejectedLight() = shot(false, add(rejected))

  @Test fun addRejectedDark() = shot(true, add(rejected))

  @Test fun scanRefusedLight() = shot(false, scan(Scan.Refused))

  @Test fun scanRefusedDark() = shot(true, scan(Scan.Refused))

  @Test fun scanLight() = shot(false, scan(Scan.Preview))

  @Test fun scanDark() = shot(true, scan(Scan.Preview))

  @Test fun confirmLight() = shot(false, confirm(plain))

  @Test fun confirmDark() = shot(true, confirm(plain))

  @Test fun replaceLight() = shot(false, confirm(replacing))

  @Test fun replaceDark() = shot(true, confirm(replacing))

  @Test fun confirmErrorLight() = shot(false, confirm(failed))

  @Test fun confirmErrorDark() = shot(true, confirm(failed))
}
