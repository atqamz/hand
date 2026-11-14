package io.github.atqamz.handcompanion

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

data class ReportState(
  val detail: ReportDetail? = null,
  val loading: Boolean = true,
  val failed: Boolean = false,
  val refused: String? = null,
  val unpaired: Boolean = false,
  val marking: Boolean = false,
  val error: String? = null,
  val notice: String? = null
)

enum class ReportBody {
  Loading,
  Failed,
  Refused,
  Content
}

fun reportBody(state: ReportState) =
  when {
    state.refused != null -> ReportBody.Refused
    state.detail != null -> ReportBody.Content
    state.failed -> ReportBody.Failed
    else -> ReportBody.Loading
  }

fun reportRead(report: TaskReport): String? =
  report.ackedAt.takeIf(String::isNotEmpty)?.let {
    "read by ${report.ackedBy.ifEmpty { "the Board" }}"
  }

fun reportRest(body: String): String {
  val text = body.trim()
  val at = text.indexOf('\n')
  return if (at < 0) "" else text.substring(at + 1).trim()
}

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

class ReportModel(
  private val fleet: String,
  private val ref: String,
  transport: Transport,
  private val bus: LiveBus = LiveBus.shared,
  private val pollMillis: Long = POLL_MILLIS
) : ViewModel() {
  private val mutableState = MutableStateFlow(ReportState())
  val state: StateFlow<ReportState> = mutableState

  private val client = BoardClient(fleet, transport)
  private val lock = Mutex()
  @Volatile private var cursor: String? = null
  @Volatile private var supported: Boolean? = null
  @Volatile private var pending = false

  suspend fun run() {
    pending = false
    load()
    if (mutableState.value.unpaired || mutableState.value.refused != null) return
    if (!eventsSupported()) return poll()
    bus.events.filter { it.fleet == fleet }.collect(::reduce)
  }

  suspend fun poll(): Nothing {
    while (true) {
      delay(pollMillis)
      load()
    }
  }

  suspend fun load() {
    do {
      pending = false
      lock.withLock {
        try {
          apply(client.readSince("report/$ref", cursor, ReportDetail.serializer()))
        } catch (_: BoardError.BadCursor) {
          cursor = null
          try {
            apply(client.readSince("report/$ref", null, ReportDetail.serializer()))
          } catch (error: BoardError) {
            failed(error)
          }
        } catch (error: BoardError) {
          failed(error)
        }
      }
    } while (pending)
  }

  private fun apply(poll: Poll<ReportDetail>) {
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
          refused = error.message?.takeIf(String::isNotBlank) ?: "No report $ref."
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
      "resync" -> if ("reports" in topics(event.json)) refreshHint()
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
    val reports = refs(json)["reports"].orEmpty()
    return ref in reports || ("reports" in topics(json) && reports.isEmpty())
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

  fun markRead() {
    viewModelScope.launch { acknowledge() }
  }

  suspend fun acknowledge(): String? {
    if (mutableState.value.marking) return null
    mutableState.update { it.copy(marking = true, error = null) }
    return try {
      when (val written = client.write("report/$ref/ack", null, ReportDetail.serializer())) {
        is Write.Updated -> {
          mutableState.update {
            it.copy(
              detail = written.envelope.data,
              loading = false,
              failed = false,
              refused = null,
              unpaired = false,
              notice = null
            )
          }
          null
        }
        Write.Accepted -> {
          load()
          null
        }
      }
    } catch (_: BoardError.AppliedUnread) {
      load()
      if (!mutableState.value.detail?.report?.ackedAt.isNullOrEmpty()) {
        null
      } else {
        val message = "Not sure $ref was marked read."
        mutableState.update { it.copy(error = message) }
        message
      }
    } catch (error: BoardError) {
      if (error == BoardError.Unpaired) {
        mutableState.update { it.copy(unpaired = true) }
        null
      } else {
        val message = readRefusal(ref, error)
        mutableState.update { it.copy(error = message) }
        message
      }
    } finally {
      mutableState.update { it.copy(marking = false) }
    }
  }
}
