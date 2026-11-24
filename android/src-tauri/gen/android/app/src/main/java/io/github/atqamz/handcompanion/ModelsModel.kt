package io.github.atqamz.handcompanion

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

@Serializable data class ModelItem(val name: String, val text: String = "")

@Serializable data class ModelGroup(val models: List<ModelItem> = emptyList())

@Serializable data class HarnessModels(val name: String, val groups: List<ModelGroup> = emptyList())

@Serializable
data class ModelsData(
  val harnesses: List<HarnessModels> = emptyList(),
  val current: String? = null,
  val model: String? = null,
  val switching: String? = null,
  val switchable: Boolean = false
)

data class ModelChoice(val name: String, val text: String, val current: Boolean)

data class ModelsState(
  val supported: Boolean? = null,
  val choices: List<ModelChoice> = emptyList(),
  val switchable: Boolean = false,
  val pending: String? = null,
  val switching: String? = null,
  val refreshing: Boolean = false,
  val error: String? = null
) {
  val visible: Boolean
    get() = supported == true && switchable && choices.isNotEmpty()

  val busy: Boolean
    get() = switching != null || refreshing
}

class ModelsUi(
  val state: ModelsState,
  val onPick: (String) -> Unit,
  val onRefresh: () -> Unit
)

fun modelChoices(data: ModelsData): List<ModelChoice> =
  data.harnesses
    .firstOrNull { it.name == data.current }
    ?.groups
    ?.flatMap { it.models }
    ?.map { ModelChoice(it.name, it.text.ifEmpty { it.name }, it.name == data.model) }
    .orEmpty()

fun switchBody(ref: String, model: String): String =
  buildJsonObject {
    put("ref", ref)
    put("model", model)
  }.toString()

class ModelsModel(fleet: String, transport: Transport) : ViewModel() {
  private val mutableState = MutableStateFlow(ModelsState())
  val state: StateFlow<ModelsState> = mutableState

  private val client = BoardClient(fleet, transport)

  fun reload() {
    viewModelScope.launch { load() }
  }

  fun refresh() {
    viewModelScope.launch { refreshNow() }
  }

  fun pick(model: String, ref: String?, after: suspend () -> Unit) {
    viewModelScope.launch { switchNow(model, ref, after) }
  }

  suspend fun load(): Boolean {
    try {
      val data = client.read("models", ModelsData.serializer()).data
      mutableState.update {
        it.copy(
          supported = true,
          choices = modelChoices(data),
          switchable = data.switchable,
          pending = data.switching?.takeIf(String::isNotEmpty)
        )
      }
      return true
    } catch (error: BoardError) {
      mutableState.update {
        when {
          error == BoardError.Unpaired -> it
          routeMissing(error) -> it.copy(supported = false)
          else -> it.copy(error = headFailure(HeadControl.Stop, error))
        }
      }
      return false
    }
  }

  suspend fun refreshNow() {
    if (mutableState.value.busy) return
    mutableState.update { it.copy(refreshing = true, error = null) }
    try {
      load()
    } finally {
      mutableState.update { it.copy(refreshing = false) }
    }
  }

  suspend fun switchNow(model: String, ref: String?, after: suspend () -> Unit) {
    if (mutableState.value.busy || ref.isNullOrEmpty()) return
    mutableState.update { it.copy(switching = model, error = null) }
    try {
      client.write("supervisor/switch", switchBody(ref, model), Status.serializer())
      after()
      load()
    } catch (error: BoardError.AppliedUnread) {
      after()
      load()
    } catch (error: BoardError) {
      if (error != BoardError.Unpaired) {
        mutableState.update { it.copy(error = headFailure(HeadControl.Stop, error)) }
      }
    } finally {
      mutableState.update { it.copy(switching = null) }
    }
  }
}
