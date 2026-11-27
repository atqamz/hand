package io.github.atqamz.handcompanion

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.Modifier
import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Rule
import org.junit.Test

private val artifacts =
  Need("artifacts", null, "3 old files in reports/ and shots/", "OLD FILES", "neutral")
private val stalled =
  Need("stalled", "s2", "Supervisor idle with 2 inbox tasks and no running attempt", "STALLED", "wait")

private val queued =
  FleetState(
    name = "atlas",
    pill = Badge("blocked", Tone.Wait),
    waiting = 2,
    tone = Tone.Wait,
    loaded = true,
    tab = Tab.Needs,
    bands = bands(listOf(stalled, artifacts)),
    open = ""
  )

class NoticeScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 2000))

  private fun shot(dark: Boolean, text: String) =
    paparazzi.snapshot {
      HandTheme(dark) {
        Box(Modifier.fillMaxSize()) {
          FleetScreen("f000000000003", queued, {}, {}, {}, {}, {}, {}, {}, {}, { _, _ -> }, {}, {}, chat = {}) {}
          Refusal(text, true)
        }
      }
    }

  private val item = notShown(queued.bands.last().entries.single().unshown())

  private val page = notShown((landing("x") { """{"path":"/f000000000003/connect"}""" } as Landing.Unshown).what)

  @Test fun queueItemLight() = shot(false, item)

  @Test fun queueItemDark() = shot(true, item)

  @Test fun pageLight() = shot(false, page)

  @Test fun pageDark() = shot(true, page)
}
