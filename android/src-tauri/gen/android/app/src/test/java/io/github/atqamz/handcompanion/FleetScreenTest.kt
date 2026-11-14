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
    open = "blocked:s3"
  )

private val notice =
  several.copy(notice = Notice("r92", "r92 is already read."))

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
    more = 2,
    open = ""
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

private const val DIGEST = "f8c722e0710879ae811b2fe7ad0c8ba0"

private val screenText =
  "Bash command\n   rm -f \$r/\$f\nClaude Code will automatically deny this request in 1:59\n" +
    "Do you want to proceed?\n❯ 1. Yes\n  2. No\nEsc to cancel · Tab to amend"

private val askCard =
  Ask(
    "Do you want to proceed?",
    listOf(AskOption("2 No", listOf("2")), AskOption("1 Yes", listOf("1")))
  )

private fun blocked(
  ask: Ask? = askCard,
  left: String? = null,
  notice: Notice? = null
): FleetState =
  FleetState(
    name = "atlas",
    pill = Badge("blocked", Tone.Fail),
    waiting = 1,
    tone = Tone.Fail,
    loaded = true,
    tab = Tab.Needs,
    bands =
      bands(
        listOf(
          Need(
            "blocked",
            "s3",
            "Bash: rm -f \$r/\$f · Do you want to proceed?",
            "BLOCKED",
            "fail",
            excerpt = screenText
          )
        )
      ),
    supervisor =
      Supervisor(
        "s3",
        "claude",
        "opus",
        "high",
        "running",
        true,
        0,
        false,
        blocked = true,
        keys = Keys(7, DIGEST, 0),
        ask = ask
      ),
    left = left,
    notice = notice
  )

private val reconnecting = several.copy(band = Band("Reconnecting…", retry = false, live = true))

private val liveLost =
  several.copy(
    band =
      Band(
        "Can't reach 4d92…6ed6 since 12:41",
        "Check that the laptop is on, that Hand's Board is running, and that this phone has internet.",
        retry = false,
        live = true
      )
  )

class FleetScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 2000))

  private fun shot(dark: Boolean, id: String, state: FleetState) =
    paparazzi.snapshot {
      HandTheme(dark) { FleetScreen(id, state, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}) {} }
    }

  @Test fun needsLight() = shot(false, "f000000000003", several)

  @Test fun needsDark() = shot(true, "f000000000003", several)

  @Test fun needsRowsLight() = shot(false, "f000000000003", rows)

  @Test fun needsRowsDark() = shot(true, "f000000000003", rows)

  @Test fun noticeLight() = shot(false, "f000000000003", notice)

  @Test fun noticeDark() = shot(true, "f000000000003", notice)

  @Test fun notLoadedLight() = shot(false, "f000000000003", FleetState(name = "atlas", tab = Tab.Needs))

  @Test fun tappedBeforeAnswerLight() = shot(false, "f000000000003", FleetState(name = "atlas", tab = Tab.Tasks))

  @Test fun tappedBeforeAnswerDark() = shot(true, "f000000000003", FleetState(name = "atlas", tab = Tab.Tasks))

  @Test fun firstFrameLight() = shot(false, "f000000000003", FleetState(name = "atlas"))

  @Test fun firstFrameDark() = shot(true, "f000000000003", FleetState(name = "atlas"))

  @Test fun failedFirstFrameLight() = shot(false, "f000000000003", unreachable)

  @Test fun failedFirstFrameDark() = shot(true, "f000000000003", unreachable)

  @Test fun reconnectingLight() = shot(false, "f000000000003", reconnecting)

  @Test fun reconnectingDark() = shot(true, "f000000000003", reconnecting)

  @Test fun liveLostLight() = shot(false, "f000000000003", liveLost)

  @Test fun liveLostDark() = shot(true, "f000000000003", liveLost)

  @Test fun allClearLight() = shot(false, "f000000000005", clear)

  @Test fun allClearDark() = shot(true, "f000000000005", clear)

  @Test fun tabsLight() = shot(false, "f000000000003", chat)

  @Test fun tabsDark() = shot(true, "f000000000003", chat)

  @Test fun blockedAskLight() = shot(false, "f000000000003", blocked())

  @Test fun blockedAskDark() = shot(true, "f000000000003", blocked())

  @Test fun blockedKeysLight() = shot(false, "f000000000003", blocked(ask = null))

  @Test fun blockedKeysDark() = shot(true, "f000000000003", blocked(ask = null))

  @Test fun blockedCountdownLight() = shot(false, "f000000000003", blocked(left = "1:59"))

  @Test fun blockedCountdownDark() = shot(true, "f000000000003", blocked(left = "1:59"))

  @Test
  fun blockedStaleLight() =
    shot(false, "f000000000003", blocked(notice = Notice("s3", "The screen moved; nothing was sent.")))

  @Test
  fun blockedStaleDark() =
    shot(true, "f000000000003", blocked(notice = Notice("s3", "The screen moved; nothing was sent.")))
}
