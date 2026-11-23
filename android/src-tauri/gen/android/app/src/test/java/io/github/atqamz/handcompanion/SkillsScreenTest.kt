package io.github.atqamz.handcompanion

import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Rule
import org.junit.Test

private val listed = SkillsState(boardJson.decodeFromString(SkillsData.serializer(), skillsJson), loading = false)

private val syncable = listed.copy(canSync = true)

private val syncedData =
  listed.data!!.copy(
    scopes =
      listed.data.scopes.map { scope ->
        scope.copy(
          skills =
            scope.skills.map {
              if (it.name == "review") it.copy(commit = it.latest, state = "current") else it
            }
        )
      }
  )

private fun refusal(text: String) =
  syncable.copy(outcome = SyncOutcome(SyncKey("project", "hand"), problem = text), canSync = false)

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

  @Test fun syncIdleLight() = shot(false, syncable)

  @Test fun syncIdleDark() = shot(true, syncable)

  @Test
  fun syncRunningLight() = shot(false, syncable.copy(syncing = SyncKey("project", "hand")))

  @Test
  fun syncRunningDark() = shot(true, syncable.copy(syncing = SyncKey("project", "hand")))

  private val done =
    syncable.copy(
      data = syncedData,
      outcome =
        SyncOutcome(
          SyncKey("project", "hand"),
          "Skills sync: 1 behind, 1 updated",
          listOf(
            SkillResult("review", "updated", "111111111111 -> 222222222222"),
            SkillResult("notes", "behind", "third-party, latest 4444444444444444444444444444444444444444; read the diff, then apply it on the host")
          )
        )
    )

  @Test fun syncDoneLight() = shot(false, done)

  @Test fun syncDoneDark() = shot(true, done)

  @Test
  fun syncRefusedLight() = shot(false, refusal("the board's controls are off"))

  @Test
  fun syncRefusedDark() = shot(true, refusal("the board's controls are off"))

  @Test
  fun syncBusyLight() =
    shot(false, syncable.copy(outcome = SyncOutcome(SyncKey("project", "hand"), problem = BUSY)))

  @Test
  fun syncBusyDark() =
    shot(true, syncable.copy(outcome = SyncOutcome(SyncKey("project", "hand"), problem = BUSY)))

  @Test fun unpairedLight() = shot(false, SkillsState(loading = false, unpaired = true))
}
