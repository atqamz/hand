package io.github.atqamz.handcompanion

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

const val NAME_MAX = 40
const val UNREAD =
  "This Companion could not read the host's answer. If the fleet is missing, show a new code on the Board and try again."

interface Pairing {
  fun offer(link: String): String?

  fun pair(name: String): String?

  fun cancel()
}

object CorePairing : Pairing {
  override fun offer(link: String) = Core.offer(link)

  override fun pair(name: String) = Core.pair(name)

  override fun cancel() = Core.cancel()
}

class Confirm(val host: String, val known: String?) {
  val title get() = if (known == null) "Add $host?" else "Add fleets to $known?"
}

data class PairState(
  val link: String = "",
  val name: String = "",
  val error: String? = null,
  val confirm: Confirm? = null,
  val busy: Boolean = false,
  val done: Boolean = false
)

fun cleanName(text: String) = text.filterNot(Character::isISOControl).take(NAME_MAX)

private val json = Json { ignoreUnknownKeys = true }

internal fun parse(text: String?): JsonObject =
  try {
    json.parseToJsonElement(text.orEmpty()).jsonObject
  } catch (_: Exception) {
    JsonObject(emptyMap())
  }

internal fun JsonObject.field(name: String) = get(name)?.jsonPrimitive?.content

class PairModel(
  private val core: Pairing,
  phone: String,
  private val held: (String) -> String?,
  private val io: CoroutineDispatcher = Dispatchers.IO
) : ViewModel() {
  private val mutableState = MutableStateFlow(PairState(name = cleanName(phone)))
  val state: StateFlow<PairState> = mutableState

  fun edit(text: String) = mutableState.update { it.copy(link = text, error = null) }

  fun rename(text: String) = mutableState.update { it.copy(name = cleanName(text)) }

  fun offer(text: String) {
    if (mutableState.value.busy) return
    val answer = parse(core.offer(text))
    val host = answer.field("host")
    mutableState.update {
      if (host == null) {
        it.copy(link = text, error = answer.field("message") ?: UNREAD)
      } else {
        it.copy(link = "", error = null, confirm = Confirm(host, held(host)))
      }
    }
  }

  fun cancel() {
    if (mutableState.value.busy) return
    core.cancel()
    mutableState.update { PairState(name = it.name) }
  }

  fun finish() = mutableState.update { PairState(name = it.name) }

  fun pair() {
    viewModelScope.launch { submit() }
  }

  suspend fun submit() {
    val state = mutableState.value
    if (state.confirm == null || state.busy) return
    mutableState.update { it.copy(busy = true, error = null) }
    val answer = parse(withContext(io) { core.pair(state.name) })
    mutableState.update {
      when {
        answer["fleets"] != null -> PairState(name = it.name, done = true)
        else -> it.copy(busy = false, error = answer.field("message") ?: UNREAD)
      }
    }
  }
}
