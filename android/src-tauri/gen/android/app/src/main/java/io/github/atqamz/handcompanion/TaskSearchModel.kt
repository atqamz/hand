package io.github.atqamz.handcompanion

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class TaskSearchRow(
  val ref: String,
  val title: String,
  val state: String,
  val project: String = "",
  val parent: String = "",
  val waits: TaskWaits? = null,
  @SerialName("open_decisions") val openDecisions: Int = 0
)

@Serializable
data class TaskSearchGroup(val name: String, val count: Int, val tasks: List<TaskSearchRow>)

@Serializable
data class TaskSearchData(
  val page: Int = 1,
  val total: Int = 0,
  val pages: Int = 0,
  val groups: List<TaskSearchGroup> = emptyList()
)

@Serializable data class ProjectNames(val projects: List<String> = emptyList())

data class TaskSearchForm(
  val q: String = "",
  val status: String = "",
  val project: String = "",
  val since: String = "",
  val until: String = ""
)

data class TaskSearchQuery(
  val q: String = "",
  val status: String = "",
  val project: String = "",
  val since: String = "",
  val until: String = "",
  val page: Int = 1
) {
  val blank: Boolean
    get() = q.isEmpty() && status.isEmpty() && project.isEmpty() && since.isEmpty() && until.isEmpty()

  fun path(): String {
    val pairs =
      listOf(
        "page" to if (page > 1) page.toString() else "",
        "project" to project,
        "q" to q,
        "since" to since,
        "status" to status,
        "until" to until
      )
    val query = pairs.filter { it.second.isNotEmpty() }.joinToString("&") { "${it.first}=${queryEscape(it.second)}" }
    return if (query.isEmpty()) "tasks/search" else "tasks/search?$query"
  }
}

private fun goSpace(c: Char) =
  c in '\t'..'\r' || c == ' ' || c == '\u0085' || c == '\u00A0' || c == '\u1680' ||
    c in '\u2000'..'\u200A' || c == '\u2028' || c == '\u2029' || c == '\u202F' || c == '\u205F' || c == '\u3000'

fun taskSearchQuery(form: TaskSearchForm, page: Int = 1) =
  TaskSearchQuery(
    form.q.trim(::goSpace),
    form.status,
    form.project,
    form.since.trim(::goSpace),
    form.until.trim(::goSpace),
    maxOf(page, 1)
  )

private const val HEX = "0123456789ABCDEF"

fun queryEscape(value: String): String {
  val out = StringBuilder()
  for (byte in value.toByteArray(Charsets.UTF_8)) {
    val b = byte.toInt() and 0xff
    val c = b.toChar()
    when {
      c in 'a'..'z' || c in 'A'..'Z' || c in '0'..'9' || c == '-' || c == '_' || c == '.' || c == '~' -> out.append(c)
      c == ' ' -> out.append('+')
      else -> out.append('%').append(HEX[b shr 4]).append(HEX[b and 15])
    }
  }
  return out.toString()
}

data class TaskSearchState(
  val form: TaskSearchForm = TaskSearchForm(),
  val supported: Boolean? = null,
  val projects: List<String> = emptyList(),
  val applied: TaskSearchQuery? = null,
  val result: TaskSearchData? = null,
  val busy: Boolean = false,
  val error: String? = null,
  val retriable: Boolean = true,
  val stale: String? = null,
  val unpaired: Boolean = false
)

enum class SearchBody {
  Legacy,
  Loading,
  Failed,
  Empty,
  Rows
}

fun searchBody(state: TaskSearchState) =
  when {
    state.supported != true -> SearchBody.Legacy
    state.applied == null -> SearchBody.Legacy
    state.error != null -> SearchBody.Failed
    state.result == null -> SearchBody.Loading
    state.result.groups.none { it.tasks.isNotEmpty() } -> SearchBody.Empty
    else -> SearchBody.Rows
  }

fun searchProblem(error: BoardError): String =
  when (error) {
    is BoardError.BadRequest -> error.message ?: "The Board refused this search."
    is BoardError.Forbidden -> error.message ?: "The Board refused this search."
    BoardError.Unreachable,
    BoardError.BoardNotRunning,
    BoardError.NoHost,
    BoardError.Lost,
    BoardError.Busy -> "The Board could not be reached."
    BoardError.TimedOut -> "The Board did not answer in time."
    else -> "The Board's answer could not be read."
  }

internal fun searchRetriable(error: BoardError) =
  when (error) {
    is BoardError.BadRequest,
    is BoardError.BadCursor,
    is BoardError.Unauthorized,
    is BoardError.Forbidden,
    is BoardError.CsrfRequired,
    is BoardError.NotFound,
    is BoardError.MethodNotAllowed,
    is BoardError.Conflict -> false
    is BoardError.Other -> error.status < 400 || error.status >= 500 || error.status == 408 || error.status == 429
    else -> true
  }

internal fun routeMissing(error: BoardError) =
  error is BoardError.NotFound ||
    error is BoardError.MethodNotAllowed ||
    error is BoardError.Other && (error.status == 404 || error.status == 405)

class TaskSearchModel(
  fleet: String,
  transport: Transport,
  private val pollMillis: Long = POLL_MILLIS
) : ViewModel() {
  private val mutableState = MutableStateFlow(TaskSearchState())
  val state: StateFlow<TaskSearchState> = mutableState

  private val client = BoardClient(fleet, transport)
  private var generation = 0

  fun open() {
    if (mutableState.value.supported == true && mutableState.value.projects.isNotEmpty()) return
    viewModelScope.launch { loadProjects() }
  }

  suspend fun loadProjects() {
    try {
      val names = client.read("projects", ProjectNames.serializer()).data.projects
      mutableState.update { it.copy(supported = true, projects = names, unpaired = false) }
    } catch (error: BoardError) {
      mutableState.update {
        when {
          error == BoardError.Unpaired -> it.copy(unpaired = true)
          routeMissing(error) -> it.copy(supported = false)
          else -> it
        }
      }
    }
  }

  fun edit(form: (TaskSearchForm) -> TaskSearchForm) = mutableState.update { it.copy(form = form(it.form)) }

  fun search() {
    val query = taskSearchQuery(mutableState.value.form)
    if (query.blank) return clear()
    viewModelScope.launch { run(query, quiet = false) }
  }

  fun clear() {
    generation++
    mutableState.update {
      it.copy(form = TaskSearchForm(), applied = null, result = null, busy = false, error = null, retriable = true, stale = null)
    }
  }

  fun page(page: Int) {
    val applied = mutableState.value.applied ?: return
    if (page < 1) return
    viewModelScope.launch { run(applied.copy(page = page), quiet = false) }
  }

  fun retry() {
    val applied = mutableState.value.applied ?: return
    viewModelScope.launch { run(applied, quiet = false) }
  }

  suspend fun watch(): Nothing {
    while (true) {
      delay(pollMillis)
      refresh()
    }
  }

  suspend fun refresh() {
    val now = mutableState.value
    val applied = now.applied ?: return
    if (now.busy) return
    run(applied, quiet = true)
  }

  suspend fun run(query: TaskSearchQuery, quiet: Boolean) {
    val mine = ++generation
    if (!quiet) {
      mutableState.update {
        it.copy(applied = query, result = null, busy = true, error = null, retriable = true, stale = null)
      }
    }
    try {
      val data = client.read(query.path(), TaskSearchData.serializer()).data
      if (mine != generation) return
      mutableState.update {
        it.copy(applied = query, result = data, busy = false, error = null, retriable = true, stale = null, unpaired = false)
      }
    } catch (error: BoardError) {
      if (mine != generation) return
      mutableState.update {
        when {
          error == BoardError.Unpaired -> it.copy(busy = false, unpaired = true)
          routeMissing(error) ->
            it.copy(supported = false, applied = null, result = null, busy = false, error = null, stale = null)
          quiet ->
            it.copy(busy = false, stale = if (searchRetriable(error)) searchProblem(error) else it.stale)
          else -> it.copy(busy = false, result = null, error = searchProblem(error), retriable = searchRetriable(error))
        }
      }
    } finally {
      if (mine == generation && mutableState.value.busy) mutableState.update { it.copy(busy = false) }
    }
  }
}
