package io.github.atqamz.handcompanion

import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Rule
import org.junit.Test

private const val DIGEST = "f8c722e0710879ae811b2fe7ad0c8ba0"

private fun waiting(kind: String, ref: String, task: String?) =
  Need(
    kind,
    ref,
    "Bash: rm -f \$r/\$f · Do you want to proceed?",
    "BLOCKED",
    "fail",
    task,
    attempt = ref.takeIf { kind == "worker" },
    excerpt = "Bash command\n   rm -f \$r/\$f\nDo you want to proceed?\n❯ 1. Yes\n  2. No",
    keys = NeedKeys(listOf(AskOption("1 Yes", listOf("1")), AskOption("2 No", listOf("2"))), 7, DIGEST, 0)
  )

private fun countdown(left: String) =
  FleetState(
    name = "atlas",
    pill = Badge("blocked", Tone.Fail),
    waiting = 2,
    tone = Tone.Fail,
    loaded = true,
    tab = Tab.Needs,
    bands = bands(listOf(waiting("worker", "a61", "t39"))),
    left = left
  )

private val noLongerPaired = FleetState(name = "atlas", loaded = true, tab = Tab.Needs, unpaired = true)

class StatesScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 1400))

  private fun fleet(dark: Boolean, state: FleetState) =
    paparazzi.snapshot {
      HandTheme(dark) { FleetScreen("f000000000003", state, {}, {}, {}, {}, {}, {}, {}, {}, { _, _ -> }, {}, {}, chat = {}) {} }
    }

  @Test fun countdownLastSecondsLight() = fleet(false, countdown("0:07"))

  @Test fun countdownLastSecondsDark() = fleet(true, countdown("0:07"))

  @Test fun countdownLongestLight() = fleet(false, countdown("999:00"))

  @Test fun countdownLongestDark() = fleet(true, countdown("999:00"))

  @Test fun fleetNoLongerPairedLight() = fleet(false, noLongerPaired)

  @Test fun fleetNoLongerPairedDark() = fleet(true, noLongerPaired)

  private fun decision(dark: Boolean, state: DecisionState) =
    paparazzi.snapshot { HandTheme(dark) { DecisionScreen("d12", state, {}, {}, {}, {}, {}) } }

  @Test fun decisionNoLongerPairedLight() = decision(false, DecisionState(unpaired = true))

  @Test fun decisionNoLongerPairedDark() = decision(true, DecisionState(unpaired = true))

  @Test fun decisionFailedLight() = decision(false, DecisionState(failed = true))

  @Test fun decisionFailedDark() = decision(true, DecisionState(failed = true))

  private fun attempt(dark: Boolean, state: AttemptState) =
    paparazzi.snapshot { HandTheme(dark) { AttemptScreen("f000000000003", "a62", state, {}, {}, {}, {}, {}) } }

  @Test fun attemptNoLongerPairedLight() = attempt(false, AttemptState(loading = false, unpaired = true))

  @Test fun attemptNoLongerPairedDark() = attempt(true, AttemptState(loading = false, unpaired = true))

  private fun task(dark: Boolean, state: TaskState) =
    paparazzi.snapshot { HandTheme(dark) { TaskScreen("f000000000003", "t40", state, {}, {}, {}, {}) } }

  @Test fun taskNoLongerPairedLight() = task(false, TaskState(loading = false, unpaired = true))

  @Test fun taskNoLongerPairedDark() = task(true, TaskState(loading = false, unpaired = true))

  @Test fun taskRefusedLight() = task(false, TaskState(loading = false, refused = "not found: task t99"))

  @Test fun taskRefusedDark() = task(true, TaskState(loading = false, refused = "not found: task t99"))

  private fun phone(dark: Boolean, phone: ThisPhone) =
    paparazzi.snapshot { HandTheme(dark) { ThisPhoneScreen(phone, {}, {}, {}, {}) } }

  private val nothing =
    ThisPhone(alerts = "Alerts are off", alertsDetail = "No fleet is paired.", phoneId = "9ab2…11fe", app = "0.2.0", core = "0.1.0")

  @Test fun thisPhoneNothingPairedLight() = phone(false, nothing)

  @Test fun thisPhoneNothingPairedDark() = phone(true, nothing)

  @Test fun thisPhoneLoadingLight() = phone(false, ThisPhone(loaded = false))

  @Test fun thisPhoneLoadingDark() = phone(true, ThisPhone(loaded = false))
}
