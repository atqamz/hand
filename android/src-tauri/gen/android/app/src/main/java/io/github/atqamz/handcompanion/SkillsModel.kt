package io.github.atqamz.handcompanion

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

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

data class SkillsState(
  val data: SkillsData? = null,
  val loading: Boolean = true,
  val problem: String? = null,
  val unpaired: Boolean = false
)

const val SKILLS_READ_ONLY =
  "Read-only here. A paired Companion cannot change skills; use the host's Board."

const val SKILLS_NO_FEATURE = "This Board has no skills endpoints. Update Hand on the host."

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
      "The Board did not answer in 30 seconds. A skills read fetches each source and can take longer; try again."
    BoardError.Lost,
    BoardError.Unreachable,
    BoardError.BoardNotRunning,
    BoardError.NoHost,
    BoardError.Busy -> "The Board could not be reached."
    is BoardError.Conflict -> error.message ?: "The Board is busy with another skills request."
    is BoardError.Forbidden -> error.message ?: "The Board refused this read."
    else -> "The Board's answer could not be read."
  }

class SkillsModel(private val fleet: String, transport: Transport) : ViewModel() {
  private val mutableState = MutableStateFlow(SkillsState())
  val state: StateFlow<SkillsState> = mutableState

  private val client = BoardClient(fleet, transport)
  private val lock = Mutex()

  fun reload() {
    viewModelScope.launch { load() }
  }

  suspend fun load() {
    if (!lock.tryLock()) return
    try {
      mutableState.update { it.copy(loading = true, problem = null) }
      try {
        if ("skills" !in client.features()) {
          mutableState.update { it.copy(loading = false, problem = SKILLS_NO_FEATURE) }
          return
        }
        val data = client.read("skills", SkillsData.serializer()).data
        mutableState.update { it.copy(data = data, loading = false, problem = null, unpaired = false) }
      } catch (error: BoardError) {
        mutableState.update {
          if (error == BoardError.Unpaired) it.copy(loading = false, unpaired = true)
          else it.copy(loading = false, problem = skillsProblem(error))
        }
      }
    } finally {
      lock.unlock()
    }
  }
}
