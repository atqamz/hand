package io.github.atqamz.handcompanion

import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import java.util.TimeZone
import org.junit.Rule
import org.junit.Test

private val utc: TimeZone = TimeZone.getTimeZone("UTC")

private val hits =
  listOf(
    hit("i412", "operator", "Can you check the «amber» kiln schedule before the Friday release?", "2026-11-20T09:15:00Z"),
    hit("y388", "supervisor", "…moved the «amber» kiln job to the evening window and told the worker.", "2026-11-19T17:40:00Z"),
    hit("r57", "report", "Kiln schedule: «amber» profile verified on the staging board. Delivered.", "2026-11-18T11:02:00Z"),
    hit("d9", "decision", "Ship the «amber» profile now or wait for the next window?", "2026-11-17T08:30:00Z"),
    hit("t23", "task", "Verify the «amber» kiln profile", "2026-11-16T14:05:00Z")
  )

private val found = HistoryState(query = "amber", searched = "amber", hits = hits, total = hits.size)

private val many = found.copy(total = 31, more = true)

private val entry =
  HistoryOpen(
    hits[0],
    HistoryEntry(
      "i412",
      "operator",
      "2026-11-20T09:15:00Z",
      text = "Can you check the amber kiln schedule before the Friday release? The profile changed on Wednesday and I want the evening window kept free.",
      truncated = false
    ),
    loading = false
  )

class HistoryScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 2400))

  private fun shot(dark: Boolean, state: HistoryState) =
    paparazzi.snapshot {
      HandTheme(dark) {
        HistoryScreen(state, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, utc)
      }
    }

  @Test fun boxLight() = shot(false, HistoryState())

  @Test fun boxDark() = shot(true, HistoryState())

  @Test fun typedLight() = shot(false, HistoryState(query = "amber kiln"))

  @Test fun hitsLight() = shot(false, found)

  @Test fun hitsDark() = shot(true, found)

  @Test fun moreLight() = shot(false, many)

  @Test fun searchingLight() = shot(false, HistoryState(query = "amber", searching = true))

  @Test fun emptyLight() = shot(false, HistoryState(query = "zzz", searched = "zzz"))

  @Test fun emptyDark() = shot(true, HistoryState(query = "zzz", searched = "zzz"))

  @Test
  fun errorLight() =
    shot(false, HistoryState(query = "amber", searched = "amber", problem = "q must not be blank"))

  @Test
  fun errorDark() =
    shot(true, HistoryState(query = "amber", searched = "amber", problem = "The Board could not be reached."))

  @Test
  fun staleLight() = shot(false, many.copy(problem = "The Board could not be reached."))

  @Test fun unpairedLight() = shot(false, HistoryState(unpaired = true))

  @Test fun entryLight() = shot(false, found.copy(open = entry))

  @Test fun entryDark() = shot(true, found.copy(open = entry))

  @Test fun entryLoadingLight() = shot(false, found.copy(open = HistoryOpen(hits[0])))

  @Test
  fun entryErrorLight() =
    shot(false, found.copy(open = HistoryOpen(hits[2], loading = false, problem = "no such entry")))

  @Test
  fun reportEntryLight() =
    shot(
      false,
      found.copy(
        open =
          HistoryOpen(
            hits[2],
            HistoryEntry("r57", "report", "2026-11-18T11:02:00Z", task = "t23", text = "Kiln schedule: amber profile verified.", truncated = true),
            loading = false
          )
      )
    )
}
