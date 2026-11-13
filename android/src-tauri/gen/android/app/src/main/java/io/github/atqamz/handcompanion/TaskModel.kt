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
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

@Serializable
data class TaskInfo(
  val ref: String,
  val title: String,
  val goal: String = "",
  val project: String = "",
  val status: String = "",
  val parent: String = "",
  @SerialName("created_at") val createdAt: String = "",
  @SerialName("updated_at") val updatedAt: String = "",
  @SerialName("finished_at") val finishedAt: String = ""
)

@Serializable
data class TaskPlan(
  val ref: String,
  val revision: Int,
  val body: String,
  @SerialName("created_at") val createdAt: String = ""
)

@Serializable data class TaskDeliverable(val ref: String, val kind: String, val href: String = "")

@Serializable
data class TaskDecision(
  val ref: String,
  val task: String,
  val status: String,
  val pill: String = "",
  val headline: String,
  val question: String = "",
  val answer: String = "",
  @SerialName("answered_by") val answeredBy: String = "",
  @SerialName("created_at") val createdAt: String = "",
  @SerialName("closed_at") val closedAt: String = ""
)

@Serializable
data class TaskReport(
  val ref: String,
  val task: String,
  val attempt: String,
  val status: String,
  val summary: String,
  val body: String,
  @SerialName("created_at") val createdAt: String,
  @SerialName("acked_at") val ackedAt: String = "",
  @SerialName("acked_by") val ackedBy: String = ""
)

@Serializable
data class TaskAttempt(
  val ref: String,
  val task: String,
  val harness: String,
  val model: String = "",
  val effort: String = "",
  val status: String,
  val state: String,
  val reason: String = "",
  val branch: String = "",
  @SerialName("created_at") val createdAt: String,
  @SerialName("ended_at") val endedAt: String = ""
)

@Serializable
data class TaskEvent(
  val seq: Long,
  val at: String,
  val kind: String,
  val detail: String = "",
  val task: String = ""
)

@Serializable
data class TaskDetail(
  val task: TaskInfo,
  val pill: String,
  val plan: TaskPlan? = null,
  val delivered: List<TaskDeliverable> = emptyList(),
  val waits: TaskWaits? = null,
  @SerialName("open_decisions") val openDecisions: List<TaskDecision> = emptyList(),
  val decisions: List<TaskDecision> = emptyList(),
  val unread: List<TaskReport> = emptyList(),
  val reports: List<TaskReport> = emptyList(),
  @SerialName("more_reports") val moreReports: Boolean = false,
  val attempts: List<TaskAttempt> = emptyList(),
  val events: List<TaskEvent> = emptyList(),
  @SerialName("more_events") val moreEvents: Boolean = false,
  val children: List<TaskRow> = emptyList()
)

@Serializable data class ReportDetail(val report: TaskReport, val task: TaskInfo)

data class TaskState(
  val detail: TaskDetail? = null,
  val loading: Boolean = true,
  val failed: Boolean = false,
  val refused: String? = null,
  val unpaired: Boolean = false,
  val marking: Set<String> = emptySet(),
  val errors: Map<String, String> = emptyMap(),
  val notice: String? = null
)

enum class TaskBody {
  Loading,
  Failed,
  Refused,
  Content
}

fun taskBody(state: TaskState) =
  when {
    state.refused != null -> TaskBody.Refused
    state.detail != null -> TaskBody.Content
    state.failed -> TaskBody.Failed
    else -> TaskBody.Loading
  }

fun reportFill(status: String) =
  when (status) {
    "blocked",
    "failed",
    "failing" -> Tone.Fail
    "done",
    "passing" -> Tone.Pass
    else -> Tone.Neutral
  }

fun reportTone(status: String) =
  when (status) {
    "blocked",
    "failed",
    "failing" -> Tone.Fail
    "progress",
    "waiting" -> Tone.Wait
    "done",
    "passing" -> Tone.Pass
    else -> Tone.Neutral
  }

fun eventWords(kind: String) = kind.replace('.', ' ').replace('_', ' ')

fun attemptTitle(attempt: TaskAttempt) =
  listOf(attempt.harness, attempt.model, attempt.effort)
    .filter(String::isNotEmpty)
    .joinToString(" · ")

fun attemptMeta(attempt: TaskAttempt) =
  listOfNotNull(attempt.status.takeIf(String::isNotEmpty), localTime(attempt.createdAt))
    .joinToString(" · ")

fun fileBadge(name: String): String {
  val extension = name.substringAfterLast('.', "")
  return extension.uppercase().takeIf { it.isNotEmpty() && it.length <= 4 } ?: "FILE"
}

sealed interface ThreadTarget {
  data class Task(val ref: String) : ThreadTarget

  data class Decision(val ref: String) : ThreadTarget

  data class Attempt(val ref: String) : ThreadTarget

  data class Web(val path: String) : ThreadTarget

  data class Browser(val url: String) : ThreadTarget

  data object None : ThreadTarget
}

private fun isRef(ref: String, letter: Char) =
  ref.length > 1 && ref[0] == letter && ref.drop(1).all(Char::isDigit)

fun refTarget(ref: String, task: String, fleet: String): ThreadTarget =
  when {
    isRef(ref, 't') -> ThreadTarget.Task(ref)
    isRef(ref, 'd') -> ThreadTarget.Decision(ref)
    isRef(ref, 'a') -> ThreadTarget.Attempt(ref)
    isRef(ref, 'r') -> ThreadTarget.Web("/$fleet/task/$task#$ref")
    else -> ThreadTarget.None
  }

fun reportTarget(report: TaskReport, task: String, fleet: String) =
  refTarget(report.ref, report.task.ifEmpty { task }, fleet)

fun deliverableTarget(deliverable: TaskDeliverable, fleet: String): ThreadTarget =
  when (deliverable.kind) {
    "file" ->
      if (deliverable.href.isEmpty()) ThreadTarget.None
      else ThreadTarget.Web("/$fleet${deliverable.href.removePrefix("/").let { "/$it" }}")
    else -> if (deliverable.ref.isEmpty()) ThreadTarget.None else ThreadTarget.Browser(deliverable.ref)
  }

fun pushTask(path: String, ref: String) = if (path.isEmpty()) ref else "$path\n$ref"

fun popTask(path: String) = path.substringBeforeLast('\n', "")

fun currentTask(path: String) = path.substringAfterLast('\n').takeIf(String::isNotEmpty)

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


class TaskModel(
  private val fleet: String,
  private val ref: String,
  transport: Transport,
  private val bus: LiveBus = LiveBus.shared,
  private val pollMillis: Long = POLL_MILLIS
) : ViewModel() {
  private val mutableState = MutableStateFlow(TaskState())
  val state: StateFlow<TaskState> = mutableState

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

  suspend fun load() =
    lock.withLock {
      try {
        apply(client.readSince("task/$ref", cursor, TaskDetail.serializer()))
      } catch (_: BoardError.BadCursor) {
        cursor = null
        try {
          apply(client.readSince("task/$ref", null, TaskDetail.serializer()))
        } catch (error: BoardError) {
          failed(error)
        }
      } catch (error: BoardError) {
        failed(error)
      }
    }

  private fun apply(poll: Poll<TaskDetail>) {
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
      Poll.Unchanged -> mutableState.update { it.copy(loading = false, failed = false) }
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
          refused = error.message?.takeIf(String::isNotBlank) ?: "No task $ref."
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
      "resync" -> if ("tasks" in topics(event.json)) refreshHint()
    }
  }

  private suspend fun refreshHint() {
    if (lock.isLocked) {
      pending = true
      return
    }
    load()
    while (pending) {
      pending = false
      load()
    }
  }

  private fun touched(json: String): Boolean {
    val topics = topics(json)
    val refs = refs(json)
    val tasks = refs["tasks"].orEmpty()
    val detail = mutableState.value.detail ?: return "tasks" in topics && tasks.isEmpty()
    val watchedTasks = setOf(detail.task.ref) + detail.children.map { it.ref }
    val watchedReports = (detail.reports + detail.unread).map { it.ref }.toSet()
    val watchedDecisions = (detail.decisions + detail.openDecisions).map { it.ref }.toSet()
    val watchedAttempts = detail.attempts.map { it.ref }.toSet()
    return tasks.any { it in watchedTasks } ||
      ("tasks" in topics && tasks.isEmpty()) ||
      refs["reports"].orEmpty().any { it in watchedReports } ||
      refs["decisions"].orEmpty().any { it in watchedDecisions } ||
      refs["attempts"].orEmpty().any { it in watchedAttempts }
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
            reports = detail.reports.map { if (it.ref == report.ref) report else it },
            unread = detail.unread.filterNot { it.ref == report.ref }
          )
      )
    }
  }
}
