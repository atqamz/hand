package io.github.atqamz.handcompanion

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import java.io.File
import java.io.IOException
import java.text.ParseException
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext

const val POLL_MILLIS = 5_000L
private val FLEET_ID = Regex("f[0-9a-f]{12}")

data class Badge(val text: String, val tone: Tone)

data class FleetRow(
  val id: String,
  val name: String = id,
  val badge: Badge? = null,
  val open: Int = 0,
  val clear: Boolean = false,
  val since: String? = null,
  val pill: Badge? = null,
  val seen: String? = null,
  val paired: Boolean = true,
  val unreadable: Boolean = false
)

data class Band(
  val title: String,
  val body: String? = null,
  val retry: Boolean = true,
  val live: Boolean = false
)

data class FleetsState(
  val laptop: String? = null,
  val rows: List<FleetRow> = emptyList(),
  val band: Band? = null,
  val alertsOff: Boolean = false,
  val asking: Boolean = false,
  val notice: String? = null,
  val loaded: Boolean = false
)

enum class FleetsBody {
  Loading,
  Rows
}

fun fleetsBody(state: FleetsState) =
  if (state.loaded) FleetsBody.Rows else FleetsBody.Loading

const val FORGET_FAILED = "Can't clear the saved login. Try again."

fun pairedFleets(ids: String?): List<String> =
  ids.orEmpty().split(' ').filter(FLEET_ID::matches).distinct()

fun laptopId(file: File): String? =
  try {
    file.readText().trim().takeIf { it.length >= 8 }?.let { "${it.take(4)}…${it.takeLast(4)}" }
  } catch (_: IOException) {
    null
  }

fun hhmm(date: Date) = SimpleDateFormat("HH:mm", Locale.getDefault()).format(date)

fun localTime(iso: String): String? = epoch(iso)?.let { hhmm(Date(it)) }

fun epoch(iso: String?): Long? =
  try {
    iso?.let {
      SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ssXXX", Locale.ROOT)
        .parse(it.replace(Regex("\\.\\d+"), ""))
        ?.time
    }
  } catch (_: ParseException) {
    null
  }

fun toneOf(name: String?) =
  when (name) {
    "fail" -> Tone.Fail
    "wait" -> Tone.Wait
    "pass" -> Tone.Pass
    "accent",
    "link" -> Tone.Accent
    else -> Tone.Neutral
  }

fun pill(status: Status) =
  Badge(
    status.line,
    when (status.pill) {
      "working" -> Tone.Accent
      "ready" -> Tone.Pass
      "failing" -> Tone.Fail
      else -> Tone.Neutral
    }
  )

private fun row(envelope: Envelope<Status>, seen: String): FleetRow {
  val status = envelope.data
  val needs = status.needs
  return FleetRow(
    id = envelope.fleet.id,
    name = envelope.fleet.name,
    badge =
      needs.worstWord?.takeIf { needs.waiting > 0 }?.let {
        Badge("${needs.worstCount} $it", toneOf(needs.items.firstOrNull()?.tone))
      },
    open = needs.waiting,
    clear = needs.waiting == 0,
    since = needs.cleared?.let(::localTime),
    pill = pill(status),
    seen = seen
  )
}

val UNREACHABLE: Set<BoardError> =
  setOf(BoardError.Unreachable, BoardError.NoLaptop, BoardError.Lost, BoardError.TimedOut)

val LAPTOP_LEVEL: Set<BoardError> =
  UNREACHABLE + setOf(BoardError.Busy, BoardError.BoardNotRunning)

fun band(error: BoardError, laptop: String?, since: String?): Band =
  when (error) {
    BoardError.Busy -> Band("Your laptop is busy. Trying again.", retry = false)
    BoardError.BoardNotRunning ->
      Band("Your laptop answers, but the Board is not running.", "Start it on the laptop.")
    else ->
      Band(
        "Can't reach ${laptop ?: "your laptop"} since $since",
        "Check that the laptop is on, that Hand's Board is running, " +
          "and that this phone has internet."
      )
  }

val UNSAVED =
  Band(
    "The phone could not save its pairings",
    "They work until the app closes. Check the phone's storage.",
    retry = false
  )

class FleetsModel(
  private val transport: Transport,
  private val fleets: () -> List<String>,
  private val laptop: () -> String?,
  private val unsaved: () -> Boolean = { false },
  private val forgetter: () -> Boolean = { true },
  private val clock: () -> String = { hhmm(Date()) }
) : ViewModel() {
  private val mutableState = MutableStateFlow(FleetsState())
  val state: StateFlow<FleetsState> = mutableState

  private val clients = mutableMapOf<String, BoardClient>()
  private val lock = Mutex()
  private var downSince: String? = null

  suspend fun poll(): Nothing {
    while (true) {
      refresh()
      delay(POLL_MILLIS)
    }
  }

  fun retry() {
    if (!lock.isLocked) viewModelScope.launch { refresh() }
  }

  fun alerts(off: Boolean) = mutableState.update { it.copy(alertsOff = off) }

  fun say(text: String?) = mutableState.update { it.copy(notice = text, asking = false) }

  fun ask(on: Boolean) = mutableState.update { it.copy(asking = on, notice = null) }

  fun forget() {
    viewModelScope.launch { forgetLaptop() }
  }

  suspend fun forgetLaptop() {
    mutableState.update { it.copy(asking = false, notice = null) }
    val forgotten = withContext(Dispatchers.IO) { forgetter() }
    if (forgotten) mutableState.update { it.copy(alertsOff = false) }
    refresh()
    if (!forgotten) say(FORGET_FAILED)
  }

  suspend fun refresh() =
    lock.withLock {
      val (ids, who) = withContext(Dispatchers.IO) { fleets() to laptop() }
      val unsaved = withContext(Dispatchers.IO) { unsaved() }
      val now = clock()
      val known = mutableState.value.rows.associateBy { it.id }
      val rows = mutableListOf<FleetRow>()
      var failure: BoardError? = null
      for (id in ids) {
        val before = known[id] ?: FleetRow(id)
        if (failure != null) {
          rows += before
          continue
        }
        rows +=
          try {
            fetch(id, before, now)
          } catch (error: BoardError) {
            when (error) {
              BoardError.Unpaired -> {
                clients.remove(id)
                before.copy(paired = false)
              }
              in LAPTOP_LEVEL -> {
                failure = error
                before
              }
              else -> before.copy(unreadable = true)
            }
          }
      }
      clients.keys.retainAll(ids.toSet())
      downSince = if (failure in UNREACHABLE) downSince ?: now else null
      mutableState.update {
        it.copy(
          laptop = who,
          rows = rows,
          loaded = true,
          band = failure?.let { band(it, who, downSince) } ?: UNSAVED.takeIf { unsaved }
        )
      }
    }

  private suspend fun fetch(id: String, before: FleetRow, now: String): FleetRow {
    val client = clients.getOrPut(id) { BoardClient(id, transport) }
    return when (val poll = client.status()) {
      is Poll.Changed -> row(poll.envelope, now)
      Poll.Unchanged -> before.copy(seen = now, unreadable = false)
    }
  }
}
