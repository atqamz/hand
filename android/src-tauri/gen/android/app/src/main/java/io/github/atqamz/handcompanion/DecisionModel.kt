package io.github.atqamz.handcompanion

import android.content.SharedPreferences
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

@Serializable
data class ChoiceOption(
  val key: String,
  val label: String,
  val note: String = "",
  val value: String,
  val recommended: Boolean = false,
  val other: Boolean = false
)

@Serializable
data class DecisionInfo(
  val status: String,
  val headline: String,
  val answer: String = "",
  @SerialName("answered_by") val answeredBy: String = "",
  @SerialName("created_at") val createdAt: String? = null
)

@Serializable data class DecisionTask(val ref: String, val title: String)

@Serializable
data class DecisionDetail(
  val decision: DecisionInfo,
  val task: DecisionTask,
  val body: String,
  val options: List<ChoiceOption> = emptyList()
)

data class DecisionState(
  val detail: DecisionDetail? = null,
  val picked: String? = null,
  val note: String = "",
  val sending: Boolean = false,
  val message: String? = null,
  val unpaired: Boolean = false,
  val failed: Boolean = false
) {
  val answered = detail != null && detail.decision.status != "open"
}

interface Drafts {
  fun read(fleet: String, ref: String): String

  fun write(fleet: String, ref: String, note: String)
}

class PrefsDrafts(private val prefs: SharedPreferences) : Drafts {
  override fun read(fleet: String, ref: String) = prefs.getString("$fleet/$ref", null).orEmpty()

  override fun write(fleet: String, ref: String, note: String) =
    prefs.edit().apply { if (note.isEmpty()) remove("$fleet/$ref") else putString("$fleet/$ref", note) }.apply()
}

private val optionLine = Regex("""^\s*(\d+|[A-Z])[.)]\s+(.+)$""")

fun prose(body: String, options: List<ChoiceOption>): String {
  if (options.isEmpty()) return body
  val keys = options.map { it.key }.toMutableSet()
  var fenced = false
  return body
    .lines()
    .filter { line ->
      val fence = line.trim().startsWith("```")
      if (fence) fenced = !fenced
      fence || fenced || optionLine.find(line)?.groupValues?.get(1)?.let(keys::remove) != true
    }
    .joinToString("\n")
    .trim()
}

fun chosenKey(options: List<ChoiceOption>, answer: String): String? =
  optionLine.find(answer.lineSequence().first())?.groupValues?.get(1)?.takeIf { key ->
    options.any { it.key == key }
  }

fun answerNote(options: List<ChoiceOption>, answer: String) =
  if (chosenKey(options, answer) == null) answer.trim() else answer.substringAfter('\n', "").trim()

private fun sendRefusal(ref: String, error: BoardError) =
  when (error) {
    BoardError.Lost,
    BoardError.TimedOut -> "Not sure $ref was sent. The answer may have been sent."
    BoardError.Busy -> "Your laptop is busy. Try again."
    else -> "Could not send $ref."
  }

class DecisionModel(
  private val fleet: String,
  private val ref: String,
  transport: Transport,
  private val drafts: Drafts
) : ViewModel() {
  private val mutableState = MutableStateFlow(DecisionState(note = drafts.read(fleet, ref)))
  val state: StateFlow<DecisionState> = mutableState

  private val client = BoardClient(fleet, transport)

  fun pick(key: String) = mutableState.update { it.copy(picked = key, message = null) }

  fun edit(note: String) {
    drafts.write(fleet, ref, note)
    mutableState.update { it.copy(note = note) }
  }

  fun reload() {
    viewModelScope.launch { load() }
  }

  fun send() {
    viewModelScope.launch { answer() }
  }

  suspend fun load() {
    try {
      val detail = client.read("decision/$ref", DecisionDetail.serializer()).data
      val open = detail.decision.status == "open"
      if (!open) drafts.write(fleet, ref, "")
      mutableState.update {
        if (open) it.copy(detail = detail, failed = false, unpaired = false)
        else it.copy(detail = detail, failed = false, unpaired = false, note = "", message = null)
      }
    } catch (error: BoardError) {
      mutableState.update {
        if (error == BoardError.Unpaired) it.copy(unpaired = true) else it.copy(failed = true)
      }
    }
  }

  suspend fun answer() {
    val now = mutableState.value
    val detail = now.detail ?: return
    if (now.sending || now.answered) return
    val pick = detail.options.firstOrNull { it.key == now.picked }
    val note = now.note.trim()
    if ((pick == null || pick.other) && note.isEmpty()) {
      val message =
        if (pick == null && detail.options.isNotEmpty()) "Pick an option or write an answer."
        else "Write your answer."
      mutableState.update { it.copy(message = message) }
      return
    }
    mutableState.update { it.copy(sending = true, message = null) }
    val body = buildJsonObject {
      put("choice", pick?.value.orEmpty())
      put("note", note)
    }
    try {
      when (val sent = client.write("decision/$ref/answer", body.toString(), DecisionDetail.serializer())) {
        is Write.Updated -> mutableState.update { it.copy(detail = sent.envelope.data) }
        Write.Accepted -> load()
      }
      sentAnswer()
    } catch (_: BoardError.AppliedUnread) {
      sentAnswer()
      load()
    } catch (_: BoardError.Conflict) {
      load()
      mutableState.update {
        val status = it.detail?.decision?.status?.takeIf { status -> status != "open" }
        it.copy(message = if (status == null) "$ref is no longer open." else "$ref is already $status.")
      }
    } catch (error: BoardError) {
      mutableState.update {
        if (error == BoardError.Unpaired) it.copy(unpaired = true) else it.copy(message = sendRefusal(ref, error))
      }
      if (error == BoardError.Lost || error == BoardError.TimedOut) load()
    } finally {
      mutableState.update { it.copy(sending = false) }
    }
  }

  private fun sentAnswer() {
    drafts.write(fleet, ref, "")
    mutableState.update { it.copy(note = "") }
  }
}
