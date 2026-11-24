package io.github.atqamz.handcompanion

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.serialization.Serializable

const val HISTORY_PAGE = 20
const val HISTORY_MAX = 50

@Serializable
data class HistoryHit(
  val ref: String,
  val who: String = "",
  val at: String = "",
  val text: String = "",
  val supervisor: String = "",
  val task: String = ""
)

@Serializable
data class HistorySearch(
  val q: String = "",
  val total: Int = 0,
  val more: Boolean = false,
  val hits: List<HistoryHit> = emptyList()
)

@Serializable
data class HistoryEntry(
  val ref: String,
  val who: String = "",
  val at: String = "",
  val supervisor: String = "",
  val task: String = "",
  val text: String = "",
  val truncated: Boolean = false
)

data class HistoryOpen(
  val hit: HistoryHit,
  val entry: HistoryEntry? = null,
  val loading: Boolean = true,
  val problem: String? = null
)

data class HistoryState(
  val query: String = "",
  val searched: String = "",
  val searching: Boolean = false,
  val hits: List<HistoryHit> = emptyList(),
  val total: Int = 0,
  val more: Boolean = false,
  val limit: Int = HISTORY_PAGE,
  val problem: String? = null,
  val unpaired: Boolean = false,
  val open: HistoryOpen? = null
)

internal val HISTORY_REF = Regex("^[iyrdt][0-9]+$")

fun historyWho(who: String) =
  when (who) {
    "operator" -> "You"
    "supervisor" -> "Supervisor"
    "report" -> "Report"
    "decision" -> "Decision"
    "task" -> "Task"
    else -> who
  }

fun historyTarget(ref: String): ThreadTarget =
  when (ref.firstOrNull()) {
    'r' -> ThreadTarget.Report(ref)
    'd' -> ThreadTarget.Decision(ref)
    't' -> ThreadTarget.Task(ref)
    else -> ThreadTarget.None
  }

fun historySnippet(text: String): List<Pair<String, Boolean>> {
  val parts = ArrayList<Pair<String, Boolean>>()
  var rest = text
  while (rest.isNotEmpty()) {
    val open = rest.indexOf('«')
    val close = if (open < 0) -1 else rest.indexOf('»', open + 1)
    if (open < 0 || close < 0) {
      parts += rest to false
      break
    }
    if (open > 0) parts += rest.substring(0, open) to false
    parts += rest.substring(open + 1, close) to true
    rest = rest.substring(close + 1)
  }
  return parts
}

fun historyProblem(error: BoardError): String =
  when (error) {
    BoardError.TimedOut -> "The Board did not answer in time."
    BoardError.Lost,
    BoardError.Unreachable,
    BoardError.BoardNotRunning,
    BoardError.NoHost,
    BoardError.Busy -> "The Board could not be reached."
    is BoardError.UnsupportedVersion,
    is BoardError.AppliedUnread,
    BoardError.Malformed,
    BoardError.TooLarge,
    BoardError.Unpaired -> "The Board's answer could not be read."
    else -> error.message?.takeIf(String::isNotBlank) ?: "The Board's answer could not be read."
  }

class HistoryModel(
  fleet: String,
  transport: Transport,
  io: CoroutineDispatcher = Dispatchers.IO
) : ViewModel() {
  private val mutableState = MutableStateFlow(HistoryState())
  val state: StateFlow<HistoryState> = mutableState

  private val client = BoardClient(fleet, transport, io)
  private val lock = Mutex()

  fun edit(text: String) {
    mutableState.update { it.copy(query = text) }
  }

  fun search() {
    val query = mutableState.value.query.trim()
    if (query.isEmpty()) {
      mutableState.update { HistoryState(query = it.query) }
      return
    }
    viewModelScope.launch { read(query, HISTORY_PAGE) }
  }

  fun retry() {
    val held = mutableState.value
    val query = held.searched.ifEmpty { held.query.trim() }
    if (query.isNotEmpty()) viewModelScope.launch { read(query, held.limit) }
  }

  fun more() {
    val held = mutableState.value
    if (!held.more || held.limit >= HISTORY_MAX || held.searching) return
    viewModelScope.launch { read(held.searched, HISTORY_MAX) }
  }

  fun open(hit: HistoryHit) {
    mutableState.update { it.copy(open = HistoryOpen(hit)) }
    viewModelScope.launch { fetch(hit) }
  }

  fun retryOpen() {
    val hit = mutableState.value.open?.hit ?: return
    mutableState.update { it.copy(open = HistoryOpen(hit)) }
    viewModelScope.launch { fetch(hit) }
  }

  fun close() {
    mutableState.update { it.copy(open = null) }
  }

  suspend fun read(query: String, limit: Int) {
    if (!lock.tryLock()) return
    try {
      val held = mutableState.value
      val again = held.searched == query
      mutableState.update {
        it.copy(
          searching = true,
          problem = null,
          limit = limit,
          hits = if (again) it.hits else emptyList(),
          total = if (again) it.total else 0,
          more = if (again) it.more else false
        )
      }
      try {
        val data = client.historySearch(query, limit).data
        mutableState.update {
          it.copy(
            searched = query,
            searching = false,
            hits = data.hits,
            total = data.total,
            more = data.more,
            unpaired = false
          )
        }
      } catch (error: BoardError) {
        mutableState.update {
          if (error == BoardError.Unpaired) it.copy(searching = false, unpaired = true)
          else it.copy(searching = false, searched = query, problem = historyProblem(error))
        }
      }
    } finally {
      lock.unlock()
    }
  }

  private suspend fun fetch(hit: HistoryHit) {
    try {
      val entry = client.historyEntry(hit.ref).data
      mutableState.update {
        if (it.open?.hit != hit) it else it.copy(open = HistoryOpen(hit, entry, loading = false))
      }
    } catch (error: BoardError) {
      mutableState.update {
        when {
          it.open?.hit != hit -> it
          error == BoardError.Unpaired -> it.copy(open = null, unpaired = true)
          else -> it.copy(open = HistoryOpen(hit, loading = false, problem = historyProblem(error)))
        }
      }
    }
  }
}
