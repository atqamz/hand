package io.github.atqamz.handcompanion

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicBoolean
import java.text.ParseException
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext

const val POLL_MILLIS = 5_000L

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

data class HostGroup(
  val endpoint: String,
  val id: String,
  val name: String,
  val rows: List<FleetRow> = emptyList(),
  val band: Band? = null
) {
  val unpaired get() = rows.isNotEmpty() && rows.none(FleetRow::paired)

  val stale get() = band?.retry == true
}

data class Forgetting(val endpoint: String?, val name: String?, val fleets: Int)

data class FleetsState(
  val hosts: List<HostGroup> = emptyList(),
  val band: Band? = null,
  val alertsOff: Boolean = false,
  val asking: Forgetting? = null,
  val notice: String? = null,
  val loaded: Boolean = false,
  val phone: PhoneInfo? = null
) {
  val rows get() = hosts.flatMap(HostGroup::rows)

  fun hostOf(fleet: String) = hosts.firstOrNull { host -> host.rows.any { it.id == fleet } }
}

enum class FleetsBody {
  Loading,
  Rows
}

fun fleetsBody(state: FleetsState) =
  if (state.loaded) FleetsBody.Rows else FleetsBody.Loading

const val FORGET_FAILED = "Can't clear the saved login. Try again."

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
  setOf(BoardError.Unreachable, BoardError.NoHost, BoardError.Lost, BoardError.TimedOut)

val HOST_LEVEL: Set<BoardError> =
  UNREACHABLE + setOf(BoardError.Busy, BoardError.BoardNotRunning)

fun band(error: BoardError, host: String?, since: String?): Band =
  when (error) {
    BoardError.Busy -> Band("Your host is busy. Trying again.", retry = false)
    BoardError.BoardNotRunning ->
      Band("Your host answers, but the Board is not running.", "Start it on the host.")
    else ->
      Band(
        "Can't reach ${host ?: "your host"} since $since",
        "Check that the host is on, that Hand's Board is running, " +
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
  private val phone: () -> PhoneInfo?,
  private val unsaved: () -> Boolean = { false },
  private val forgetter: () -> Boolean = { true },
  private val hostForgetter: (String) -> Boolean = { true },
  private val clock: () -> String = { hhmm(Date()) }
) : ViewModel() {
  private val mutableState = MutableStateFlow(FleetsState())
  val state: StateFlow<FleetsState> = mutableState

  private val clients = ConcurrentHashMap<String, BoardClient>()
  private val lock = Mutex()
  private val downSince = ConcurrentHashMap<String, String>()
  private val retrying = AtomicBoolean(false)

  suspend fun poll(): Nothing {
    while (true) {
      refresh()
      delay(POLL_MILLIS)
    }
  }

  fun retry() {
    if (!retrying.compareAndSet(false, true)) return
    viewModelScope.launch {
      lock.withLock {
        retrying.set(false)
        round()
      }
    }
  }

  fun alerts(off: Boolean) = mutableState.update { it.copy(alertsOff = off) }

  fun say(text: String?) = mutableState.update { it.copy(notice = text, asking = null) }

  fun ask(forgetting: Forgetting?) = mutableState.update { it.copy(asking = forgetting, notice = null) }

  fun forget() {
    val target = mutableState.value.asking ?: return
    viewModelScope.launch { forget(target) }
  }

  suspend fun forget(target: Forgetting) {
    mutableState.update { it.copy(asking = null, notice = null) }
    val forgotten =
      withContext(Dispatchers.IO) {
        if (target.endpoint == null) forgetter() else hostForgetter(target.endpoint)
      }
    refresh()
    if (forgotten && mutableState.value.hosts.isEmpty()) {
      mutableState.update { it.copy(alertsOff = false) }
    }
    if (!forgotten) say(FORGET_FAILED)
  }

  suspend fun refresh() = lock.withLock { round() }

  private suspend fun round() {
    val info = withContext(Dispatchers.IO) { phone() }
    val unsaved = withContext(Dispatchers.IO) { unsaved() }
    val now = clock()
    val known = mutableState.value.rows.associateBy { it.id }
    val hosts = info?.hosts.orEmpty()
    val order = hosts.map(PairedHost::endpoint)
    clients.keys.retainAll(info?.fleets.orEmpty().map(PairedFleet::id).toSet())
    downSince.keys.retainAll(order.toSet())
    mutableState.update {
      it.copy(
        hosts = it.hosts.filter { group -> group.endpoint in order },
        loaded = it.loaded || hosts.isEmpty(),
        phone = info,
        band = UNSAVED.takeIf { unsaved }
      )
    }
    coroutineScope {
      hosts.forEach { host ->
        launch {
          val group = group(host, known, now)
          mutableState.update { state ->
            val landed = state.hosts.associateBy(HostGroup::endpoint) + (group.endpoint to group)
            state.copy(hosts = order.mapNotNull(landed::get), loaded = true)
          }
        }
      }
    }
  }

  private suspend fun group(host: PairedHost, known: Map<String, FleetRow>, now: String): HostGroup {
    var failure: BoardError? = null
    val rows =
      host.fleets.map { fleet ->
        val before = known[fleet.id] ?: FleetRow(fleet.id, fleet.name.ifBlank { fleet.id })
        if (failure != null) return@map before
        try {
          fetch(fleet.id, before, now)
        } catch (error: BoardError) {
          when (error) {
            BoardError.Unpaired -> {
              clients.remove(fleet.id)
              before.copy(paired = false)
            }
            in HOST_LEVEL -> {
              failure = error
              before
            }
            else -> before.copy(unreadable = true)
          }
        }
      }
    val since =
      if (failure in UNREACHABLE) downSince.getOrPut(host.endpoint) { now }
      else downSince.remove(host.endpoint)
    return HostGroup(host.endpoint, host.id, host.name, rows, failure?.let { band(it, host.name, since) })
  }

  private suspend fun fetch(id: String, before: FleetRow, now: String): FleetRow {
    val client = clients.getOrPut(id) { BoardClient(id, transport) }
    return when (val poll = client.status()) {
      is Poll.Changed -> row(poll.envelope, now)
      Poll.Unchanged -> before.copy(seen = now, unreadable = false)
    }
  }
}
