package io.github.atqamz.handcompanion

import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Rule
import org.junit.Test

private fun need(
  kind: String,
  word: String,
  tone: String,
  ref: String,
  title: String?,
  task: String? = null,
  at: String? = null,
  excerpt: String? = null
) =
  Need(
    kind,
    ref,
    title,
    word,
    tone,
    task,
    createdAt = at,
    excerpt = excerpt,
    report = if (kind == "report") ref else null
  )

private val needs =
  listOf(
    need("blocked", "BLOCKED", "fail", "s3", null),
    need(
      "failure",
      "FAILED",
      "fail",
      "a61",
      "Flaky test in the nightly pipeline on the arm64 runner after the dependency bump",
      "t39",
      "2026-11-01T11:20:00Z"
    ),
    need("decision", "DECISION", "wait", "d12", "Pin the parser to which version?", "t40", "2026-11-01T11:40:00Z"),
    need(
      "report",
      "REPORT",
      "neutral",
      "r91",
      "Cookie rename: done",
      "t41",
      "2026-11-01T11:48:00Z",
      "The cookie keeps its old name for one release, then the old reader goes.\n\nAll tests pass."
    ),
    need("report", "REPORT", "neutral", "r92", "Parser pinned: progress", "t40", "2026-11-01T11:50:00Z")
  )

private val several =
  FleetState(
    name = "atlas",
    pill = Badge("blocked", Tone.Fail),
    waiting = 5,
    tone = Tone.Fail,
    loaded = true,
    tab = Tab.Needs,
    bands = bands(needs),
    open = "report:r91"
  )

private val cut =
  several.copy(
    bands = bands(needs.map { if (it.ref == "r91") it.copy(cut = true) else it }),
    notice = Notice("r92", "r92 is already read.")
  )

private val rows =
  FleetState(
    name = "atlas",
    pill = Badge("blocked", Tone.Fail),
    waiting = 6,
    tone = Tone.Fail,
    loaded = true,
    tab = Tab.Needs,
    bands =
      bands(
        listOf(
          needs[0],
          need("worker", "BLOCKED", "fail", "a7", null, "t3", "2026-11-01T11:05:00Z"),
          needs[1],
          needs[2],
          need("nosup", "NO SUPERVISOR", "wait", "n1", "No supervisor is running", null, "2026-11-01T10:30:00Z")
        )
      ),
    more = 2
  )

private val clear =
  FleetState(
    name = "orchard",
    pill = Badge("ready", Tone.Pass),
    loaded = true,
    tab = Tab.Needs,
    cleared = "12:41",
    summary = "1 running · 6 inbox"
  )

private val chat = several.copy(tab = Tab.Chat, working = true, pill = Badge("working", Tone.Accent))

private val unreachable =
  FleetState(name = "atlas", band = band(BoardError.Unreachable, "4d92…6ed6", "12:41"))

class FleetScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 2000))

  private fun shot(dark: Boolean, id: String, state: FleetState) =
    paparazzi.snapshot {
      HandTheme(dark) { FleetScreen(id, state, {}, {}, {}, {}, {}, {}) {} }
    }

  @Test fun needsLight() = shot(false, "f000000000003", several)

  @Test fun needsDark() = shot(true, "f000000000003", several)

  @Test fun needsRowsLight() = shot(false, "f000000000003", rows)

  @Test fun needsRowsDark() = shot(true, "f000000000003", rows)

  @Test fun cutLight() = shot(false, "f000000000003", cut)

  @Test fun cutDark() = shot(true, "f000000000003", cut)

  @Test fun notLoadedLight() = shot(false, "f000000000003", FleetState(name = "atlas", tab = Tab.Needs))

  @Test fun tappedBeforeAnswerLight() = shot(false, "f000000000003", FleetState(name = "atlas", tab = Tab.Tasks))

  @Test fun tappedBeforeAnswerDark() = shot(true, "f000000000003", FleetState(name = "atlas", tab = Tab.Tasks))

  @Test fun firstFrameLight() = shot(false, "f000000000003", FleetState(name = "atlas"))

  @Test fun firstFrameDark() = shot(true, "f000000000003", FleetState(name = "atlas"))

  @Test fun failedFirstFrameLight() = shot(false, "f000000000003", unreachable)

  @Test fun failedFirstFrameDark() = shot(true, "f000000000003", unreachable)

  @Test fun allClearLight() = shot(false, "f000000000005", clear)

  @Test fun allClearDark() = shot(true, "f000000000005", clear)

  @Test fun tabsLight() = shot(false, "f000000000003", chat)

  @Test fun tabsDark() = shot(true, "f000000000003", chat)
}
