package io.github.atqamz.handcompanion

import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Rule
import org.junit.Test

private val choices =
  listOf(
    ChoiceOption("1", "Pin 0.14.2", "reproducible builds; we bump it by hand", "1. Pin 0.14.2", recommended = true),
    ChoiceOption("2", "Track 0.14.x", "patch fixes arrive by themselves", "2. Track 0.14.x"),
    ChoiceOption("3", "Track the latest release", "newest features, and CI may break again", "3. Track the latest release"),
    ChoiceOption("4", "Other", "", "4. Other", other = true)
  )

private fun detailOf(status: String = "open", answer: String = "", by: String = "") =
  DecisionDetail(
    DecisionInfo(status, "Pin luvus to which version?", answer, by, "2026-11-11T11:40:00Z"),
    DecisionTask("t40", "Pin the luvus version in CI"),
    "CI broke when luvus 0.15 changed the output of its session list. Pick how CI follows luvus from now on.\n\n" +
      choices.joinToString("\n") { "${it.key}. ${it.label} - ${it.note}" },
    choices
  )

private val recommended = DecisionState(detailOf(), picked = "2")

private val other =
  DecisionState(detailOf(), picked = "4", note = "Pin to 0.14.x for CI only, and let the laptop follow the latest.")

private val answered =
  DecisionState(
    detailOf("answered", "2. Track 0.14.x\nPatch fixes are enough for CI.", "phone (Pixel 8)"),
    message = "d12 is already answered."
  )

private val needs =
  FleetState(
    name = "atlas",
    pill = Badge("blocked", Tone.Fail),
    waiting = 3,
    tone = Tone.Wait,
    loaded = true,
    tab = Tab.Needs,
    bands =
      bands(
        listOf(
          Need("decision", "d12", "Pin luvus to which version?", "DECISION", "wait", "t40", createdAt = "2026-11-11T11:40:00Z"),
          Need("decision", "d11", "Keep the old cookie name readable for one release?", "DECISION", "wait", "t41", createdAt = "2026-11-11T10:05:00Z"),
          Need("report", "r91", "Cookie rename: done", "REPORT", "neutral", "t41", createdAt = "2026-11-11T11:48:00Z", report = "r91")
        )
      ),
    open = "decision:d12"
  )

class DecisionScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 2000))

  private fun screen(dark: Boolean, state: DecisionState) =
    paparazzi.snapshot {
      HandTheme(dark) { DecisionScreen("d12", state, {}, {}, {}, {}, {}) }
    }

  private fun inPlace(dark: Boolean) =
    paparazzi.snapshot {
      HandTheme(dark) {
        FleetScreen("f000000000003", needs, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, chat = {}) {
          ChoiceForm("d12", recommended, {}, {}, {}, {}) {}
        }
      }
    }

  @Test fun recommendedLight() = screen(false, recommended)

  @Test fun recommendedDark() = screen(true, recommended)

  @Test fun otherLight() = screen(false, other)

  @Test fun otherDark() = screen(true, other)

  @Test fun answeredLight() = screen(false, answered)

  @Test fun answeredDark() = screen(true, answered)

  @Test fun needsInPlaceLight() = inPlace(false)

  @Test fun needsInPlaceDark() = inPlace(true)
}
