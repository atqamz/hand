package io.github.atqamz.handcompanion

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.TimeZone
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put

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
  val band: Band? = null,
  val supervisor: Supervisor? = null,
  val pressed: Map<String, String> = emptyMap(),
  val pressing: Set<String> = emptySet(),
  val deadline: Long? = null,
  val left: String? = null,
  val tasks: TasksState = TasksState()
)

private fun opens(need: Need) =
  when (need.kind) {
    "report",
    "blocked" -> Opens.Card
    "decision" -> Opens.Choice
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

private fun pressRefusal(ref: String, error: BoardError) =
  when (error) {
    BoardError.Lost,
    BoardError.TimedOut -> "Not sure the key reached $ref."
    BoardError.Busy -> "Your laptop is busy. Try again."
    else -> "Could not press a key on $ref."
  }

data class TasksState(
  val loaded: Boolean = false,
  val all: Boolean = false,
  val groups: List<TaskGroup> = emptyList(),
  val hidden: Int = 0,
  val finished: Int = 0,
  val failed: Boolean = false
)

data class TaskLine(val task: TaskRow, val depth: Int, val under: String? = null)

fun lines(group: TaskGroup): List<TaskLine> {
  val seen = mutableSetOf<String>()
  return group.tasks.map { task ->
    val parent = task.parent.takeIf { it.isNotEmpty() && it != task.ref }
    val nested = parent != null && parent in seen
    seen += task.ref
    TaskLine(task, if (nested) 1 else 0, parent.takeIf { !nested })
  }
}

enum class TasksBody {
  Loading,
  Failed,
  Empty,
  Rows
}

fun tasksBody(tasks: TasksState) =
  when {
    tasks.failed && tasks.groups.isEmpty() -> TasksBody.Failed
    !tasks.loaded -> TasksBody.Loading
    tasks.groups.isEmpty() -> TasksBody.Empty
    else -> TasksBody.Rows
  }

fun tasksFooter(tasks: TasksState): String? =
  "Show all finished tasks (${tasks.finished})".takeIf { !tasks.all && tasks.finished > 0 }

fun tasksHidden(tasks: TasksState): String? =
  tasks.hidden.takeIf { it > 0 }?.let {
    "$it more ${if (it == 1) "task is" else "tasks are"} not shown"
  }

fun dueDay(due: String): String =
  epoch(due)?.let {
    SimpleDateFormat("MMM d", Locale.ENGLISH)
      .apply { timeZone = TimeZone.getTimeZone("UTC") }
      .format(Date(it))
  } ?: due

fun waitsLine(waits: TaskWaits): String {
  val due = "Waits until ${dueDay(waits.due)}"
  return if (waits.note.isEmpty()) due else "$due: ${waits.note}"
}

fun stateTone(state: String) =
  when (state) {
    "failing" -> Tone.Fail
    "waiting" -> Tone.Wait
    "running",
    "passing" -> Tone.Pass
    else -> Tone.Neutral
  }

enum class Body {
  Loading,
  Unpaired,
  Needs,
  Tasks,
  Placeholder
}

fun body(state: FleetState) =
  when {
    state.unpaired -> Body.Unpaired
    !state.loaded -> Body.Loading
    state.tab == Tab.Needs -> Body.Needs
    state.tab == Tab.Tasks -> Body.Tasks
    else -> Body.Placeholder
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

fun FleetState.deadline(): Long? {
  val entry = bands.flatMap { it.entries }.firstOrNull { it.key == openKey() }
  return if (entry?.need?.kind == "blocked") epoch(entry.need.denyAt) else null
}

fun remaining(deadline: Long, now: Long): String? {
  val left = deadline - now
  if (left <= 0) return null
  val seconds = (left + 999) / 1000
  return "${seconds / 60}:${(seconds % 60).toString().padStart(2, '0')}"
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
    band = null,
    supervisor = status.supervisor
  )
}

private const val NOTICE_MILLIS = 4_000L
private const val SCREEN_MOVED = "The screen moved; nothing was sent."
private const val SCREEN_DENIED = "The screen was denied."
private const val LINK_SLOW_MILLIS = 5_000L
private const val LIVE_FAILURES = 3

private data class Receipt(val notice: Notice, val entry: String)

private fun FleetState.noticed(notice: Notice?): FleetState =
  copy(notice = notice, pressed = emptyMap())

class FleetModel(
  private val fleet: String,
  name: String?,
  transport: Transport,
  private val laptop: () -> String?,
  private val clock: () -> String = { hhmm(Date()) },
  private val noticeMillis: Long = NOTICE_MILLIS,
  private val nowMillis: () -> Long = { System.currentTimeMillis() },
  private val bus: LiveBus = LiveBus.shared,
  private val pollMillis: Long = POLL_MILLIS,
  private val startLive: (String) -> Unit = { Core.live(it) },
  private val stopLive: () -> Unit = { Core.unlive() }
) : ViewModel() {
  private val mutableState = MutableStateFlow(FleetState(name = name))
  val state: StateFlow<FleetState> = mutableState

  private val client = BoardClient(fleet, transport)
  private val lock = Mutex()
  private val tasksLock = Mutex()
  private var downSince: String? = null
  private var denied: Long? = null
  private var supported: Boolean? = null
  private var slow: Job? = null
  private var clockOffset: Long = 0
  internal val liveFailures = AtomicInteger(0)

  suspend fun run() {
    refresh()
    if (state.value.unpaired) return
    if (!eventsSupported()) return poll()
    if (live()) poll()
  }

  suspend fun poll(): Nothing {
    while (true) {
      refresh()
      delay(pollMillis)
    }
  }

  private suspend fun eventsSupported(): Boolean {
    if (supported == true) return true
    val events =
      try {
        "events" in client.features()
      } catch (error: BoardError) {
        if (error == BoardError.Unpaired) {
          mutableState.update { it.copy(unpaired = true, band = null) }
        }
        false
      }
    if (events) supported = true
    return events
  }

  private suspend fun live(): Boolean {
    liveFailures.set(0)
    var fallback = false
    coroutineScope {
      val stopped = CompletableDeferred<Unit>()
      val links = launch {
        bus.links.collect { link ->
          when (link.state) {
            "live" -> {
              slow?.cancel()
              mutableState.update { if (it.band?.live == true) it.copy(band = null) else it }
            }
            "reconnecting" -> {
              val count = liveFailures.incrementAndGet()
              mutableState.update {
                it.copy(band = Band("Reconnecting…", retry = false, live = true))
              }
              slow?.cancel()
              slow = launch {
                delay(LINK_SLOW_MILLIS)
                mutableState.update { state ->
                  if (state.band?.live != true) state
                  else
                    state.copy(
                      band =
                        Band(
                          "Can't reach ${laptop() ?: "your laptop"} since " +
                            hhmm(Date(link.since.coerceAtLeast(1))),
                          "Check that the laptop is on, that Hand's Board is running, " +
                            "and that this phone has internet.",
                          retry = false,
                          live = true
                        )
                    )
                }
              }
              if (count >= LIVE_FAILURES) stopped.complete(Unit)
            }
            "unpaired" -> {
              mutableState.update { it.copy(unpaired = true, band = null) }
              stopped.complete(Unit)
            }
            "stopped" -> stopped.complete(Unit)
          }
        }
      }
      val events = launch {
        bus.events.filter { it.fleet == fleet }.collect {
          if (it.topic == "status" || (it.topic == "resync" && "status" in topics(it.json))) {
            liveFailures.set(0)
          }
          reduce(it)
        }
      }
      try {
        withContext(Dispatchers.IO) { startLive(fleet) }
        stopped.await()
        fallback = !state.value.unpaired
      } finally {
        links.cancel()
        events.cancel()
        slow?.cancel()
        slow = null
        withContext(NonCancellable + Dispatchers.IO) { stopLive() }
        bus.reset()
      }
    }
    return fallback
  }

  internal suspend fun reduce(event: LiveEvent) {
    when (event.topic) {
      "hello" -> {
        trackClock(event.json)
        val name =
          try {
            boardJson
              .parseToJsonElement(event.json)
              .jsonObject["fleet"]
              ?.jsonObject
              ?.get("name")
              ?.jsonPrimitive
              ?.content
          } catch (_: Exception) {
            null
          }
        if (name != null) mutableState.update { it.copy(name = name) }
      }
      "status" -> {
        trackClock(event.json)
        val status =
          try {
            boardJson.decodeFromString<Status>(event.json)
          } catch (_: Exception) {
            return
          }
        val known = state.value.name ?: fleet
        mutableState.update { it.filled(status, known) }
        arm()
      }
      "touched" -> if ("tasks" in topics(event.json)) reloadTasks()
      "resync" -> {
        val moved = topics(event.json)
        if ("status" in moved) refresh()
        if ("tasks" in moved && "status" !in moved) reloadTasks()
      }
    }
  }

  private fun trackClock(json: String) {
    val now =
      try {
        boardJson.parseToJsonElement(json).jsonObject["now"]?.jsonPrimitive?.content
      } catch (_: Exception) {
        null
      }
    epoch(now)?.let { clockOffset = it - nowMillis() }
  }

  private fun boardNow() = nowMillis() + clockOffset

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

  private suspend fun reloadTasks() {
    if (mutableState.value.tab == Tab.Tasks && !tasksLock.isLocked) loadTasks()
  }

  fun retry() {
    if (!lock.isLocked && !tasksLock.isLocked) viewModelScope.launch { refresh() }
  }

  fun select(tab: Tab) = mutableState.update { it.copy(tab = tab) }

  fun openTasks() {
    if (mutableState.value.tasks.loaded || tasksLock.isLocked) return
    viewModelScope.launch { loadTasks() }
  }

  fun showAll() {
    if (tasksLock.isLocked) return
    viewModelScope.launch { loadTasks(all = true) }
  }

  suspend fun loadTasks(all: Boolean = mutableState.value.tasks.all) =
    tasksLock.withLock {
      try {
        val tasks = client.tasks(all).data
        mutableState.update {
          it.copy(
            unpaired = false,
            tasks =
              TasksState(
                loaded = true,
                all = tasks.all,
                groups = tasks.groups,
                hidden = tasks.hidden,
                finished = tasks.finishedCount
              )
          )
        }
      } catch (error: BoardError) {
        mutableState.update {
          it.copy(
            unpaired = it.unpaired || error == BoardError.Unpaired,
            band =
              if (it.tasks.groups.isNotEmpty()) Band("The Board's answer could not be read.")
              else it.band,
            tasks = it.tasks.copy(failed = true)
          )
        }
      }
    }

  fun enter() =
    mutableState.update { if (it.tab == Tab.Chat && it.waiting > 0) it.copy(tab = Tab.Needs) else it }

  fun toggle(key: String) =
    mutableState.update { it.copy(open = if (it.openKey() == key) "" else key) }

  fun dismiss(notice: Notice) =
    mutableState.update { if (it.notice === notice) it.noticed(null) else it }

  fun markRead(ref: String) {
    viewModelScope.launch { acknowledgeAndExpire(ref) }
  }

  suspend fun acknowledgeAndExpire(ref: String) {
    acknowledge(ref)?.let { expire(it) }
  }

  private suspend fun expire(notice: Notice, entry: String? = null) {
    delay(noticeMillis)
    mutableState.update {
      val stale = it.notice !== notice
      it.copy(
        notice = if (stale) it.notice else null,
        pressed = if (entry == null || stale) it.pressed else it.pressed - entry
      )
    }
  }

  fun press(keys: List<String>) {
    viewModelScope.launch { pressAndExpire(keys) }
  }

  suspend fun pressAndExpire(keys: List<String>) {
    send(keys)?.let { expire(it.notice, it.entry) }
  }

  suspend fun pressNow(keys: List<String>): Notice? = send(keys)?.notice

  private suspend fun send(keys: List<String>): Receipt? {
    val state = mutableState.value
    val supervisor = state.supervisor ?: return null
    val guard = supervisor.keys ?: return null
    val entry =
      state.bands.flatMap { it.entries }.firstOrNull {
        it.key == state.openKey() && it.need.kind == "blocked"
      } ?: return null
    if (entry.key in state.pressing) return null
    mutableState.update { it.noticed(null).copy(pressing = it.pressing + entry.key) }
    val body = buildJsonObject {
      put("keys", JsonArray(keys.map(::JsonPrimitive)))
      put("revision", guard.revision)
      put("digest", guard.digest)
      put("after", guard.pressed)
      put("ref", supervisor.ref)
    }
    try {
      when (val sent = client.write("supervisor/keys", body.toString(), Status.serializer())) {
        is Write.Updated -> apply(sent.envelope)
        Write.Accepted -> refresh()
      }
      return pressed(entry, keys, supervisor.ref)
    } catch (_: BoardError.AppliedUnread) {
      refresh()
      return pressed(entry, keys, supervisor.ref)
    } catch (error: BoardError) {
      if (error == BoardError.Unpaired) {
        mutableState.update { it.copy(unpaired = true) }
        return null
      }
      val notice =
        if (error is BoardError.Conflict) Notice(supervisor.ref, SCREEN_MOVED)
        else Notice(supervisor.ref, pressRefusal(supervisor.ref, error))
      if (error is BoardError.Conflict) refresh()
      mutableState.update { it.noticed(notice).copy(open = entry.key) }
      return Receipt(notice, entry.key)
    } finally {
      mutableState.update { it.copy(pressing = it.pressing - entry.key) }
    }
  }

  private fun pressed(entry: Entry, keys: List<String>, ref: String): Receipt {
    val keysText = keys.joinToString(" ")
    val notice = Notice(ref, "Sent $keysText to $ref")
    mutableState.update {
      it.copy(
        open = "",
        pressed = mapOf(entry.key to "Sent $keysText · waiting…"),
        notice = notice
      )
    }
    return Receipt(notice, entry.key)
  }

  fun force() {
    viewModelScope.launch { forceNow() }
  }

  suspend fun forceNow(): Notice? {
    val supervisor = mutableState.value.supervisor ?: return null
    mutableState.update { it.noticed(null) }
    val body = buildJsonObject { put("ref", supervisor.ref) }
    val typed = Notice(supervisor.ref, "Typed the queued messages to ${supervisor.ref}.")
    val notice =
      try {
        when (val sent = client.write("supervisor/force", body.toString(), Status.serializer())) {
          is Write.Updated -> apply(sent.envelope)
          Write.Accepted -> refresh()
        }
        typed
      } catch (_: BoardError.AppliedUnread) {
        refresh()
        typed
      } catch (error: BoardError) {
        if (error == BoardError.Unpaired) mutableState.update { it.copy(unpaired = true) }
        Notice(
          supervisor.ref,
          when (error) {
            is BoardError.Conflict -> "${supervisor.ref} moved; nothing was typed."
            BoardError.Lost,
            BoardError.TimedOut -> "Not sure the messages reached ${supervisor.ref}."
            else -> "Could not type the queued messages."
          }
        )
      }
    mutableState.update { it.noticed(notice) }
    return notice
  }

  fun tick() {
    viewModelScope.launch { tickOnce()?.let { expire(it) } }
  }

  suspend fun tickOnce(): Notice? {
    val state = mutableState.value
    val deadline = state.deadline() ?: return null
    val left = remaining(deadline, boardNow())
    if (left != null) {
      mutableState.update { it.copy(left = left) }
      return null
    }
    denied = deadline
    val ref =
      state.supervisor?.ref
        ?: state.bands.flatMap { it.entries }.firstOrNull { it.need.kind == "blocked" }?.need?.ref
    val notice = Notice(ref.orEmpty(), SCREEN_DENIED)
    mutableState.update { it.noticed(notice).copy(deadline = null, left = null) }
    refresh()
    return notice
  }

  private fun arm() {
    val deadline = mutableState.value.deadline()?.takeIf { it != denied }
    val left = deadline?.let { remaining(it, boardNow()) }
    if (deadline != null && left == null) denied = deadline
    mutableState.update { it.copy(deadline = if (left == null) null else deadline, left = left) }
  }

  private fun apply(envelope: Envelope<Status>) {
    mutableState.update { it.filled(envelope.data, it.name ?: envelope.fleet.name) }
    arm()
  }

  suspend fun acknowledge(ref: String): Notice? {
    if (mutableState.value.marking.contains(ref)) return null
    mutableState.update { it.noticed(null).copy(marking = it.marking + ref) }
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
    mutableState.update { it.noticed(notice) }
    return notice
  }

  suspend fun refresh() =
    lock.withLock {
      val now = clock()
      var reachable = false
      try {
        when (val poll = client.status()) {
          is Poll.Changed -> {
            mutableState.update { it.filled(poll.envelope.data, poll.envelope.fleet.name) }
            arm()
          }
          Poll.Unchanged -> mutableState.update { it.copy(unpaired = false, band = null) }
        }
        downSince = null
        reachable = true
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
      if (reachable && mutableState.value.tab == Tab.Tasks && !tasksLock.isLocked) loadTasks()
    }
}
