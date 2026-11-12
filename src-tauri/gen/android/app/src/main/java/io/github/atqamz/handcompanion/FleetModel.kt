package io.github.atqamz.handcompanion

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import java.util.Date
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.JsonElement

enum class Tab {
  Needs,
  Tasks,
  Chat
}

enum class Opens {
  Card,
  Choice,
  Row,
  Web
}

data class Entry(val need: Need, val opens: Opens) {
  val key = "${need.kind}:${need.ref}"
}

data class NeedsBand(val title: String, val tone: Tone, val entries: List<Entry>)

data class Notice(val ref: String, val text: String)

data class FleetState(
  val name: String? = null,
  val pill: Badge? = null,
  val waiting: Int = 0,
  val tone: Tone = Tone.Neutral,
  val working: Boolean = false,
  val loaded: Boolean = false,
  val tab: Tab? = null,
  val bands: List<NeedsBand> = emptyList(),
  val more: Int = 0,
  val open: String? = null,
  val marking: Set<String> = emptySet(),
  val cleared: String? = null,
  val summary: String? = null,
  val notice: Notice? = null,
  val unpaired: Boolean = false,
  val band: Band? = null
)

private fun opens(need: Need) =
  when (need.kind) {
    "report" -> Opens.Card
    "decision" -> Opens.Choice
    "blocked",
    "failure" -> Opens.Row
    else -> Opens.Web
  }

fun bands(items: List<Need>): List<NeedsBand> {
  val bands = mutableListOf<NeedsBand>()
  for (need in items) {
    val title = need.word.lowercase().replaceFirstChar(Char::uppercase)
    val entry = Entry(need, opens(need))
    val last = bands.lastOrNull()
    if (last?.title == title) bands[bands.lastIndex] = last.copy(entries = last.entries + entry)
    else bands += NeedsBand(title, toneOf(need.tone), listOf(entry))
  }
  return bands
}

private fun refusal(ref: String, error: BoardError) =
  when (error) {
    is BoardError.Conflict -> "$ref is already read."
    BoardError.Lost,
    BoardError.TimedOut -> "Not sure $ref was marked read."
    else -> "Could not mark $ref read."
  }

fun FleetState.openKey(): String? {
  val entries = bands.flatMap { it.entries }
  val cards = entries.filter { it.opens == Opens.Card }.map { it.key }
  return when (open) {
    "" -> null
    in cards -> open
    in entries.filter { it.opens == Opens.Choice }.map { it.key } -> open
    else -> cards.firstOrNull()
  }
}

private fun FleetState.filled(status: Status, name: String): FleetState {
  val needs = status.needs
  val first = if (needs.waiting > 0) Tab.Needs else Tab.Chat
  val refs = needs.items.mapNotNull { it.report }.toSet()
  return copy(
    name = name,
    pill = pill(status),
    waiting = needs.waiting,
    tone = toneOf(needs.items.firstOrNull()?.tone),
    working = status.agentState == "working",
    loaded = true,
    tab = tab ?: first,
    bands = bands(needs.items),
    more = needs.more,
    marking = marking intersect refs,
    cleared = needs.cleared?.let(::localTime),
    summary = needs.summary,
    unpaired = false,
    band = null
  )
}

private const val NOTICE_MILLIS = 4_000L

class FleetModel(
  fleet: String,
  name: String?,
  transport: Transport,
  private val laptop: () -> String?,
  private val clock: () -> String = { hhmm(Date()) },
  private val noticeMillis: Long = NOTICE_MILLIS
) : ViewModel() {
  private val mutableState = MutableStateFlow(FleetState(name = name))
  val state: StateFlow<FleetState> = mutableState

  private val client = BoardClient(fleet, transport)
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

  fun select(tab: Tab) = mutableState.update { it.copy(tab = tab) }

  fun enter() =
    mutableState.update { if (it.tab == Tab.Chat && it.waiting > 0) it.copy(tab = Tab.Needs) else it }

  fun toggle(key: String) =
    mutableState.update { it.copy(open = if (it.openKey() == key) "" else key) }

  fun dismiss(notice: Notice) =
    mutableState.update { if (it.notice === notice) it.copy(notice = null) else it }

  fun markRead(ref: String) {
    viewModelScope.launch { acknowledgeAndExpire(ref) }
  }

  suspend fun acknowledgeAndExpire(ref: String) {
    acknowledge(ref)?.let {
      delay(noticeMillis)
      dismiss(it)
    }
  }

  suspend fun acknowledge(ref: String): Notice? {
    if (mutableState.value.marking.contains(ref)) return null
    mutableState.update { it.copy(notice = null, marking = it.marking + ref) }
    val notice =
      try {
        client.write("report/$ref/ack", null, JsonElement.serializer())
        null
      } catch (_: BoardError.AppliedUnread) {
        null
      } catch (error: BoardError) {
        if (error == BoardError.Unpaired) {
          mutableState.update { it.copy(unpaired = true, marking = it.marking - ref) }
          return null
        }
        Notice(ref, refusal(ref, error))
      }
    if (notice != null) mutableState.update { it.copy(marking = it.marking - ref) }
    refresh()
    mutableState.update { it.copy(notice = notice) }
    return notice
  }

  suspend fun refresh() =
    lock.withLock {
      val now = clock()
      try {
        when (val poll = client.status()) {
          is Poll.Changed ->
            mutableState.update { it.filled(poll.envelope.data, poll.envelope.fleet.name) }
          Poll.Unchanged -> mutableState.update { it.copy(unpaired = false, band = null) }
        }
        downSince = null
      } catch (error: BoardError) {
        when (error) {
          BoardError.Unpaired -> mutableState.update { it.copy(unpaired = true, band = null) }
          in LAPTOP_LEVEL -> {
            downSince = if (error in UNREACHABLE) downSince ?: now else null
            mutableState.update { it.copy(band = band(error, laptop(), downSince)) }
          }
          else -> mutableState.update { it.copy(band = Band("The Board's answer could not be read.")) }
        }
      }
    }
}
