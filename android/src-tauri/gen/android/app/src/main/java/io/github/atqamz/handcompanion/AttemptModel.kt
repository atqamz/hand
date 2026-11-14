package io.github.atqamz.handcompanion

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

@Serializable
data class AttemptTurn(
  val role: String,
  val text: String,
  val at: String = "",
  val queued: Boolean = false
)

@Serializable
data class AttemptTranscript(
  val shown: Boolean = false,
  val more: Boolean = false,
  val note: String = "",
  val model: String = "",
  val answered: String = "",
  val turns: List<AttemptTurn> = emptyList()
)

@Serializable
data class AttemptRecord(
  val ref: String,
  val task: String,
  val harness: String,
  val model: String = "",
  val effort: String = "",
  val status: String,
  val reason: String = "",
  val branch: String = "",
  @SerialName("created_at") val createdAt: String,
  @SerialName("ended_at") val endedAt: String = ""
)

@Serializable
data class AttemptDetail(
  val attempt: AttemptRecord,
  val task: TaskInfo,
  val state: String,
  val brief: String = "",
  @SerialName("model_note") val modelNote: String = "",
  val duration: String = "",
  val reports: List<TaskReport> = emptyList(),
  @SerialName("more_reports") val moreReports: Boolean = false,
  val events: List<TaskEvent> = emptyList(),
  @SerialName("more_events") val moreEvents: Boolean = false,
  val delivered: List<TaskDeliverable> = emptyList(),
  val transcript: AttemptTranscript = AttemptTranscript()
)

data class AttemptState(
  val detail: AttemptDetail? = null,
  val loading: Boolean = true,
  val failed: Boolean = false,
  val refused: String? = null,
  val unpaired: Boolean = false,
  val marking: Set<String> = emptySet(),
  val errors: Map<String, String> = emptyMap(),
  val notice: String? = null,
  val briefOpen: Boolean = false
)

enum class AttemptBody {
  Loading,
  Failed,
  Refused,
  Content
}

fun attemptBody(state: AttemptState) =
  when {
    state.refused != null -> AttemptBody.Refused
    state.detail != null -> AttemptBody.Content
    state.failed -> AttemptBody.Failed
    else -> AttemptBody.Loading
  }

fun attemptFacts(detail: AttemptDetail) =
  listOfNotNull(
      detail.duration.takeIf(String::isNotEmpty),
      detail.attempt.branch.takeIf(String::isNotEmpty)?.let { "branch $it" }
    )
    .joinToString(" · ")

const val TRANSCRIPT_MORE = "Only the latest 50 entries are shown."

fun transcriptEmpty(transcript: AttemptTranscript) =
  transcript.note.takeIf(String::isNotEmpty) ?: "Nothing in the transcript yet."

fun reportsMore(attempt: String) =
  "Older reports are not shown; run hand report list --attempt $attempt --limit 500."

const val EVENTS_MORE = "Only the latest 50 events are shown."

private fun readRefusal(ref: String, error: BoardError) =
  when (error) {
    is BoardError.Conflict -> "$ref is already read."
    BoardError.Lost,
    BoardError.TimedOut -> "Not sure $ref was marked read."
    else -> "Could not mark $ref read."
  }

private fun readError(error: BoardError) =
  when (error) {
    BoardError.Lost,
    BoardError.TimedOut -> "The Board stopped answering."
    BoardError.Busy -> "Your laptop is busy. Try again."
    else -> "The Board's answer could not be read."
  }

class AttemptModel(
  private val fleet: String,
  private val ref: String,
  transport: Transport,
  private val bus: LiveBus = LiveBus.shared,
  private val pollMillis: Long = POLL_MILLIS
) : ViewModel() {
  private val mutableState = MutableStateFlow(AttemptState())
  val state: StateFlow<AttemptState> = mutableState

  private val client = BoardClient(fleet, transport)
  private val lock = Mutex()
  @Volatile private var cursor: String? = null
  @Volatile private var supported: Boolean? = null
  @Volatile private var pending = false

  suspend fun run() {
    pending = false
    load()
    if (mutableState.value.unpaired || mutableState.value.refused != null) return
    if (!eventsSupported()) return follow()
    coroutineScope {
      launch { follow() }
      bus.events.filter { it.fleet == fleet }.collect(::reduce)
    }
  }

  private fun live() =
    mutableState.value.detail?.state.let { it == null || it == "running" }

  private suspend fun follow() {
    while (live()) {
      delay(pollMillis)
      if (live()) load()
    }
  }

  suspend fun load() {
    do {
      pending = false
      lock.withLock {
        try {
          apply(client.readSince("attempt/$ref", cursor, AttemptDetail.serializer()))
        } catch (_: BoardError.BadCursor) {
          cursor = null
          try {
            apply(client.readSince("attempt/$ref", null, AttemptDetail.serializer()))
          } catch (error: BoardError) {
            failed(error)
          }
        } catch (error: BoardError) {
          failed(error)
        }
      }
    } while (pending)
  }

  private fun apply(poll: Poll<AttemptDetail>) {
    when (poll) {
      is Poll.Changed -> {
        cursor = poll.envelope.cursor
        mutableState.update {
          it.copy(
            detail = poll.envelope.data,
            loading = false,
            failed = false,
            refused = null,
            unpaired = false,
            notice = null
          )
        }
      }
      Poll.Unchanged ->
        mutableState.update { it.copy(loading = false, failed = false, notice = null) }
    }
  }

  private fun failed(error: BoardError) {
    if (error == BoardError.Unpaired) {
      mutableState.update { it.copy(unpaired = true, loading = false, failed = false, notice = null) }
      return
    }
    if (error is BoardError.NotFound) {
      mutableState.update {
        it.copy(
          loading = false,
          failed = false,
          refused = error.message?.takeIf(String::isNotBlank) ?: "No attempt $ref."
        )
      }
      return
    }
    mutableState.update {
      it.copy(
        loading = false,
        failed = true,
        notice = if (it.detail != null) readError(error) else it.notice
      )
    }
  }

  private suspend fun eventsSupported(): Boolean {
    if (supported == true) return true
    val events =
      try {
        "events" in client.features()
      } catch (error: BoardError) {
        if (error == BoardError.Unpaired) mutableState.update { it.copy(unpaired = true) }
        false
      }
    if (events) supported = true
    return events
  }

  internal suspend fun reduce(event: LiveEvent) {
    when (event.topic) {
      "touched" -> if (touched(event.json)) refreshHint()
      "resync" -> if ("attempts" in topics(event.json)) refreshHint()
    }
  }

  private suspend fun refreshHint() {
    if (lock.isLocked) {
      pending = true
      return
    }
    load()
  }

  private fun touched(json: String): Boolean {
    val attempts = refs(json)["attempts"].orEmpty()
    return ref in attempts || ("attempts" in topics(json) && attempts.isEmpty())
  }

  private fun topics(json: String): List<String> =
    try {
      boardJson
        .parseToJsonElement(json)
        .jsonObject["topics"]
        ?.jsonArray
        ?.mapNotNull { it.jsonPrimitive.content }
        ?: emptyList()
    } catch (_: Exception) {
      emptyList()
    }

  private fun refs(json: String): Map<String, List<String>> =
    try {
      boardJson
        .parseToJsonElement(json)
        .jsonObject
        .mapNotNull { (name, value) ->
          if (value is JsonArray) name to value.mapNotNull { it.jsonPrimitive.content } else null
        }
        .toMap()
    } catch (_: Exception) {
      emptyMap()
    }

  fun retry() {
    if (!lock.isLocked) viewModelScope.launch { load() }
  }

  fun toggleBrief() {
    mutableState.update { it.copy(briefOpen = !it.briefOpen) }
  }

  fun markRead(report: String) {
    viewModelScope.launch { acknowledge(report) }
  }

  suspend fun acknowledge(report: String): String? {
    if (report in mutableState.value.marking) return null
    mutableState.update {
      it.copy(marking = it.marking + report, errors = it.errors - report)
    }
    return try {
      when (val written = client.write("report/$report/ack", null, ReportDetail.serializer())) {
        is Write.Updated -> {
          applyRead(written.envelope.data.report)
          null
        }
        Write.Accepted -> {
          load()
          null
        }
      }
    } catch (_: BoardError.AppliedUnread) {
      load()
      val acked = mutableState.value.detail?.reports?.firstOrNull { it.ref == report }?.ackedAt
      if (!acked.isNullOrEmpty()) {
        null
      } else {
        val message = "Not sure $report was marked read."
        mutableState.update { it.copy(errors = it.errors + (report to message)) }
        message
      }
    } catch (error: BoardError) {
      if (error == BoardError.Unpaired) {
        mutableState.update { it.copy(unpaired = true) }
        null
      } else {
        val message = readRefusal(report, error)
        mutableState.update { it.copy(errors = it.errors + (report to message)) }
        message
      }
    } finally {
      mutableState.update { it.copy(marking = it.marking - report) }
    }
  }

  private fun applyRead(report: TaskReport) {
    mutableState.update { state ->
      val detail = state.detail ?: return@update state
      state.copy(
        detail =
          detail.copy(
            reports = detail.reports.map { if (it.ref == report.ref) report else it }
          )
      )
    }
  }
}
