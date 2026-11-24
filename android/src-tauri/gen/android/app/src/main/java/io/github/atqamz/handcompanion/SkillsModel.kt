package io.github.atqamz.handcompanion

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

@Serializable
data class SkillInfo(
  val name: String,
  val source: String = "",
  val ref: String = "",
  val commit: String = "",
  val latest: String = "",
  val managed: Boolean = false,
  val trusted: Boolean = false,
  val state: String = "",
  val detail: String = "",
  val diff: String = "",
  @SerialName("diff_cut") val diffCut: Boolean = false
)

@Serializable
data class SkillScopeInfo(
  val scope: String,
  val project: String = "",
  val error: String = "",
  val skills: List<SkillInfo> = emptyList()
)

@Serializable data class SkillsData(val scopes: List<SkillScopeInfo> = emptyList())

@Serializable data class SkillResult(val name: String, val state: String, val detail: String = "")

@Serializable
data class SkillsRun(
  val scope: String,
  val project: String = "",
  val results: List<SkillResult> = emptyList(),
  val refused: Int = 0
)

data class SyncKey(val scope: String, val project: String = "")

data class SyncOutcome(
  val key: SyncKey,
  val receipt: String? = null,
  val results: List<SkillResult> = emptyList(),
  val refused: Int = 0,
  val problem: String? = null
)

data class SkillsState(
  val data: SkillsData? = null,
  val loading: Boolean = true,
  val problem: String? = null,
  val unpaired: Boolean = false,
  val canSync: Boolean = false,
  val syncing: SyncKey? = null,
  val outcome: SyncOutcome? = null
)

const val SKILLS_READ_ONLY =
  "Sync moves Hand's own skills. The user scope, third-party updates, update and propose stay on the host."

const val SKILLS_HOST_ONLY =
  "Read-only here. A paired Companion cannot change skills; use the host's Board."

const val SKILLS_NO_FEATURE = "This Board has no skills endpoints. Update Hand on the host."

fun skillSyncs(skill: SkillInfo) = skill.managed && skill.trusted && skill.state == "behind"

fun skillSyncShown(state: SkillsState, scope: SkillScopeInfo): Boolean {
  val key = SyncKey(scope.scope, scope.project)
  return state.canSync &&
    scope.error.isEmpty() &&
    scope.scope != "user" &&
    (state.syncing == key || scope.skills.any(::skillSyncs))
}

fun skillScopeTitle(scope: SkillScopeInfo) =
  when (scope.scope) {
    "fleet" -> "Fleet"
    "project" -> "Project ${scope.project}"
    "user" -> "User"
    else -> scope.scope
  }

fun skillStateWord(skill: SkillInfo) =
  when {
    !skill.managed -> "unmanaged"
    skill.state.isEmpty() -> "unknown"
    else -> skill.state
  }

fun skillTone(skill: SkillInfo) =
  when {
    !skill.managed -> Tone.Neutral
    skill.state == "current" -> Tone.Pass
    skill.state == "behind" || skill.state == "edited" -> Tone.Wait
    skill.state == "conflict" || skill.state == "error" -> Tone.Fail
    else -> Tone.Neutral
  }

fun skillTrust(skill: SkillInfo) =
  when {
    !skill.managed -> null
    skill.trusted -> "own source"
    else -> "third-party"
  }

private fun shortCommit(id: String) = id.take(12)

fun skillSource(skill: SkillInfo): String {
  val commit =
    skill.commit.takeIf(String::isNotEmpty)?.let {
      if (skill.latest.isNotEmpty() && skill.latest != skill.commit) {
        "${shortCommit(it)} → ${shortCommit(skill.latest)}"
      } else {
        shortCommit(it)
      }
    }
  return listOfNotNull(
      skill.source.takeIf(String::isNotEmpty),
      skill.ref.takeIf(String::isNotEmpty),
      commit
    )
    .joinToString(" · ")
}

fun skillNote(skill: SkillInfo) =
  when {
    skill.detail.isNotEmpty() -> skill.detail
    skill.state == "conflict" -> "Edited here and moved at the source. Nothing updates."
    skill.state == "edited" -> "Edited here while the source did not move."
    skill.state == "behind" && !skill.trusted ->
      "Third-party: read its diff on the host before it moves."
    else -> ""
  }

internal fun skillsProblem(error: BoardError): String =
  when (error) {
    BoardError.TimedOut ->
      "The Board did not answer in time. A skills read fetches each source and can take minutes; try again."
    BoardError.Lost,
    BoardError.Unreachable,
    BoardError.BoardNotRunning,
    BoardError.NoHost,
    BoardError.Busy -> "The Board could not be reached."
    is BoardError.Conflict -> error.message ?: "The Board is busy with another skills request."
    is BoardError.Forbidden -> error.message ?: "The Board refused this read."
    else -> "The Board's answer could not be read."
  }

internal fun syncProblem(error: BoardError): String =
  when (error) {
    BoardError.TimedOut ->
      "The Board did not answer in time. The sync may still be running on the host; check again in a minute."
    BoardError.Lost,
    BoardError.Unreachable,
    BoardError.BoardNotRunning,
    BoardError.NoHost,
    BoardError.Busy -> "The Board could not be reached. The sync may have run or may still be running on the host; check again in a minute."
    is BoardError.Conflict -> error.message ?: "The Board is busy with another skills request."
    is BoardError.Forbidden -> error.message ?: "The Board refused this sync."
    is BoardError.BadRequest -> error.message ?: "The Board refused this sync."
    is BoardError.Internal -> error.message ?: "The sync failed on the host."
    else -> "The Board's answer could not be read."
  }

class SkillsModel(private val fleet: String, transport: Transport) : ViewModel() {
  private val mutableState = MutableStateFlow(SkillsState())
  val state: StateFlow<SkillsState> = mutableState

  private val client = BoardClient(fleet, transport)
  private val lock = Mutex()

  @Volatile private var refused = false

  fun reload() {
    viewModelScope.launch { load() }
  }

  suspend fun load() {
    if (!lock.tryLock()) return
    try {
      withContext(NonCancellable) { read() }
    } finally {
      lock.unlock()
    }
  }

  fun sync(scope: String, project: String = "") {
    viewModelScope.launch { runSync(SyncKey(scope, project)) }
  }

  suspend fun runSync(key: SyncKey) {
    val state = mutableState.value
    if (!state.canSync || key.scope !in setOf("fleet", "project")) return
    if (!lock.tryLock()) return
    try {
      withContext(NonCancellable) {
        mutableState.update { it.copy(syncing = key, outcome = null) }
        if (post(key)) read()
      }
    } finally {
      lock.unlock()
    }
  }

  private suspend fun post(key: SyncKey): Boolean {
    val body =
      buildJsonObject {
          put("scope", key.scope)
          if (key.project.isNotEmpty()) put("project", key.project)
        }
        .toString()
    fun finish(outcome: SyncOutcome, canSync: Boolean = true) =
      mutableState.update { it.copy(syncing = null, outcome = outcome, canSync = it.canSync && canSync) }
    return try {
      when (val written = client.write("skills/sync", body, SkillsRun.serializer())) {
        is Write.Updated ->
          finish(SyncOutcome(key, written.envelope.receipt, written.envelope.data.results, written.envelope.data.refused))
        is Write.Accepted -> finish(SyncOutcome(key, written.receipt))
      }
      true
    } catch (error: BoardError.AppliedUnread) {
      finish(SyncOutcome(key, error.receipt, problem = "The sync ran, but the Board's answer could not be read."))
      true
    } catch (error: BoardError) {
      when (error) {
        BoardError.Unpaired -> mutableState.update { it.copy(syncing = null, unpaired = true) }
        is BoardError.Forbidden -> {
          refused = true
          finish(SyncOutcome(key, problem = syncProblem(error)), canSync = false)
        }
        else -> finish(SyncOutcome(key, problem = syncProblem(error)))
      }
      false
    }
  }

  private suspend fun read() {
    mutableState.update { it.copy(loading = true, problem = null) }
    try {
      val features = client.features()
      if ("skills" !in features) {
        mutableState.update { it.copy(loading = false, problem = SKILLS_NO_FEATURE, canSync = false) }
        return
      }
      val data = client.read("skills", SkillsData.serializer()).data
      mutableState.update {
        it.copy(
          data = data,
          loading = false,
          problem = null,
          unpaired = false,
          canSync = "skills_sync" in features && !refused
        )
      }
    } catch (error: BoardError) {
      mutableState.update {
        if (error == BoardError.Unpaired) it.copy(loading = false, unpaired = true)
        else it.copy(loading = false, problem = skillsProblem(error))
      }
    }
  }
}
