package io.github.atqamz.handcompanion

import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import java.util.TimeZone
import org.junit.Rule
import org.junit.Test

private const val FLEET = "f00000000e0aa"
private val UTC: TimeZone = TimeZone.getTimeZone("UTC")
private val NOW = epoch("2026-11-02T11:00:00Z")!!

private fun at(minutes: Int, day: Int = 2) =
  "2026-11-0${day}T${(10 + minutes / 60).toString().padStart(2, '0')}:${(minutes % 60).toString().padStart(2, '0')}:00Z"

private val wakes =
  listOf(
    TimelineEntry(
      role = "hand",
      at = at(8),
      text = "wake: decision.answered d11"
    ),
    TimelineEntry(role = "hand", at = at(9), text = "wake: report.done r8"),
    TimelineEntry(role = "hand", at = at(10), text = "wake: attempt.reported a1: r1 done")
  )

private val conversation =
  listOf(
    TimelineEntry(
      role = "supervisor",
      at = at(58),
      no = 41,
      ref = "s1",
      model = "sonnet low",
      text =
        "The rename is **t41**. `a63` has opened the PR and keeps the old name readable " +
          "for one release, as you answered.\n\n" +
          "- t40 waits on d12\n" +
          "- t39 failed twice; r89 has the log"
    ),
    TimelineEntry(
      role = "operator",
      at = at(40),
      input = 7,
      delivery = "delivered",
      delivered = at(40),
      text = "Please look at the cookie rename before lunch.\nThe docs still use the old name."
    ),
    TimelineEntry(role = "hand", at = at(20), text = "wake: decision.answered d11"),
    TimelineEntry(role = "wakes", at = at(10), wakes = wakes),
    TimelineEntry(
      role = "supervisor",
      at = at(0, day = 1),
      no = 38,
      ref = "s1",
      text = "Good morning. Overnight a61 failed twice on the arm64 runner."
    )
  )

private fun pageState(
  entries: List<TimelineEntry> = conversation,
  live: Boolean = true,
  working: TimelineWorking? =
    TimelineWorking(
      ref = "s1",
      since = at(55),
      on = "on a wake",
      last = at(57)
    ),
  note: String = "",
  before: Int = 118,
  aboveBottom: Boolean = false,
  newReplies: Int = 0,
  olderLoading: Boolean = false,
  olderFailed: Boolean = false,
  loading: Boolean = false,
  failed: Boolean = false
) =
  ChatState(
    page = pageKeys(entries),
    before = before,
    to = before + entries.size,
    live = live,
    note = note,
    working = working,
    loading = loading,
    failed = failed,
    olderLoading = olderLoading,
    olderFailed = olderFailed,
    aboveBottom = aboveBottom,
    newReplies = newReplies,
    now = NOW
  )

class ChatScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 3200))

  private fun shot(
    dark: Boolean,
    state: ChatState,
    expand: Set<String> = emptySet(),
    zone: TimeZone = UTC
  ) =
    paparazzi.snapshot {
      HandTheme(dark) {
        ChatScreen(
          FLEET,
          state,
          onOlder = {},
          onRetry = {},
          onRetryOlder = {},
          onScrolled = {},
          onTarget = {},
          zone = zone,
          expand = expand
        )
      }
    }

  @Test fun conversationLight() = shot(false, pageState())

  @Test fun conversationDark() = shot(true, pageState())

  @Test
  fun wakesExpandedLight() {
    val state = pageState()
    val fold = state.page.first { it.entry.role == "wakes" }
    shot(false, state, expand = setOf(fold.key))
  }

  @Test
  fun wakesExpandedDark() {
    val state = pageState()
    val fold = state.page.first { it.entry.role == "wakes" }
    shot(true, state, expand = setOf(fold.key))
  }

  @Test fun latestLight() = shot(false, pageState(aboveBottom = true, newReplies = 3))

  @Test fun latestDark() = shot(true, pageState(aboveBottom = true, newReplies = 3))

  @Test fun olderLoadingLight() = shot(false, pageState(olderLoading = true, before = 118))

  @Test fun olderLoadingDark() = shot(true, pageState(olderLoading = true, before = 118))

  @Test
  fun olderFailedLight() = shot(false, pageState(olderFailed = true, before = 118))

  @Test
  fun olderFailedDark() = shot(true, pageState(olderFailed = true, before = 118))

  @Test
  fun emptyLight() =
    shot(false, pageState(entries = emptyList(), working = null, note = "No supervisor"))

  @Test
  fun emptyDark() =
    shot(true, pageState(entries = emptyList(), working = null, note = "No supervisor"))

  @Test fun errorLight() = shot(false, ChatState(loading = false, failed = true, now = NOW))

  @Test fun errorDark() = shot(true, ChatState(loading = false, failed = true, now = NOW))

  @Test fun loadingLight() = shot(false, ChatState(now = NOW))

  @Test fun loadingDark() = shot(true, ChatState(now = NOW))

  @Test fun unpairedLight() = shot(false, ChatState(loading = false, unpaired = true, now = NOW))

  @Test fun unpairedDark() = shot(true, ChatState(loading = false, unpaired = true, now = NOW))

  @Test fun refusedLight() =
    shot(false, ChatState(loading = false, refused = "Controls are off", now = NOW))

  @Test fun refusedDark() =
    shot(true, ChatState(loading = false, refused = "Controls are off", now = NOW))
}
