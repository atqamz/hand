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
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

const val NAME_MAX = 40
const val UNREAD =
  "The phone could not read the laptop's answer. If the fleet is missing, show a new code on your laptop and try again."

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

class Current(val laptop: String?, val fleets: List<String>)

class Confirm(val laptop: String, val notice: String?) {
  val action get() = if (notice == null) "Pair" else "Replace and pair"
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

private fun joined(names: List<String>) =
  if (names.size < 2) names.joinToString() else names.dropLast(1).joinToString() + " and " + names.last()

fun replaceNotice(current: Current): String {
  val who = current.laptop ?: "your current laptop"
  val fleets = current.fleets
  val lost =
    if (fleets.isEmpty()) "" else
      " and its ${fleets.size} fleet${if (fleets.size == 1) "" else "s"}: ${joined(fleets)}. " +
        (if (fleets.size == 1) "Its" else "Their") + " alerts stop"
  return "Once paired, this phone leaves $who$lost. If pairing fails, nothing changes."
}

private val json = Json { ignoreUnknownKeys = true }

private fun parse(text: String?): JsonObject =
  try {
    json.parseToJsonElement(text.orEmpty()).jsonObject
  } catch (_: Exception) {
    JsonObject(emptyMap())
  }

private fun JsonObject.field(name: String) = get(name)?.jsonPrimitive?.content

class PairModel(
  private val core: Pairing,
  phone: String,
  private val current: () -> Current,
  private val io: CoroutineDispatcher = Dispatchers.IO
) : ViewModel() {
  private val mutableState = MutableStateFlow(PairState(name = cleanName(phone)))
  val state: StateFlow<PairState> = mutableState

  fun edit(text: String) = mutableState.update { it.copy(link = text, error = null) }

  fun rename(text: String) = mutableState.update { it.copy(name = cleanName(text)) }

  fun offer(text: String) {
    if (mutableState.value.busy) return
    val answer = parse(core.offer(text))
    val laptop = answer.field("laptop")
    mutableState.update {
      if (laptop == null) {
        it.copy(link = text, error = answer.field("message") ?: UNREAD)
      } else {
        val replaces = answer["replaces"]?.jsonPrimitive?.boolean == true
        it.copy(
          link = "",
          error = null,
          confirm = Confirm(laptop, if (replaces) replaceNotice(current()) else null)
        )
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
