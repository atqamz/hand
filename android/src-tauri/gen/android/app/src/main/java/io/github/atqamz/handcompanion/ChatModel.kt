package io.github.atqamz.handcompanion

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.TimeZone
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

@Serializable
data class TimelineEntry(
  val key: String = "",
  val role: String,
  val at: String = "",
  val text: String = "",
  val no: Int = 0,
  val ref: String = "",
  val model: String = "",
  val delivery: String = "",
  val delivered: String = "",
  val input: Long = 0,
  val queued: Boolean = false,
  val wakes: List<TimelineEntry> = emptyList()
)

@Serializable
data class TimelineWorking(
  val ref: String,
  val since: String,
  val on: String = "",
  val from: String = "",
  val last: String = "",
  val input: Long = 0
)

@Serializable
data class TimelineData(
  val live: Boolean = false,
  val older: Int = 0,
  val note: String = "",
  val working: TimelineWorking? = null,
  val entries: List<TimelineEntry> = emptyList()
)

@Serializable
data class TimelineEvent(
  val snapshot: Boolean = false,
  val from: Int = 0,
  val to: Int = 0,
  val older: Int = 0,
  val live: Boolean = false,
  val note: String = "",
  val working: TimelineWorking? = null,
  val entries: List<TimelineEntry> = emptyList(),
  val drop: List<String> = emptyList()
)

fun entryIdentity(entry: TimelineEntry): String =
  when {
    entry.input != 0L -> "i${entry.input}"
    entry.no != 0 -> "n${entry.no}"
    else -> "${entry.role}@${entry.at}@${entry.text}"
  }

data class ChatEntry(val key: String, val entry: TimelineEntry) {
  val identity: String = entryIdentity(entry)
}

data class ChatState(
  val page: List<ChatEntry> = emptyList(),
  val older: List<ChatEntry> = emptyList(),
  val before: Int = 0,
  val to: Int? = null,
  val live: Boolean = false,
  val note: String = "",
  val working: TimelineWorking? = null,
  val loading: Boolean = true,
  val failed: Boolean = false,
  val refused: String? = null,
  val unpaired: Boolean = false,
  val olderLoading: Boolean = false,
  val olderFailed: Boolean = false,
  val aboveBottom: Boolean = false,
  val newReplies: Int = 0,
  val now: Long = 0
)

enum class ChatBody {
  Loading,
  Failed,
  Refused,
  Content
}

fun chatBody(state: ChatState) =
  when {
    state.refused != null -> ChatBody.Refused
    state.page.isNotEmpty() || state.older.isNotEmpty() -> ChatBody.Content
    state.loading -> ChatBody.Loading
    state.failed -> ChatBody.Failed
    else -> ChatBody.Content
  }

fun pageKeys(entries: List<TimelineEntry>): List<ChatEntry> {
  val keys = arrayOfNulls<String>(entries.size)
  val seen = HashMap<String, Int>()
  for (index in entries.indices.reversed()) {
    val entry = entries[index]
    val base = if (entry.input != 0L) "i${entry.input}" else "${entry.role}@${entry.at}"
    val count = (seen[base] ?: 0) + 1
    seen[base] = count
    keys[index] = if (count == 1) base else "$base#$count"
  }
  return entries.mapIndexed { index, entry -> ChatEntry(keys[index].orEmpty(), entry) }
}

fun mergeEntries(held: List<ChatEntry>, added: List<ChatEntry>): List<ChatEntry> {
  val out = held.toMutableList()
  for (entry in added) {
    val at = out.indexOfFirst { it.key == entry.key }
    if (at >= 0) {
      out[at] = entry
      continue
    }
    val when_ = epoch(entry.entry.at) ?: Long.MAX_VALUE
    val index = out.indexOfFirst { (epoch(it.entry.at) ?: 0L) < when_ }
    if (index < 0) out.add(entry) else out.add(index, entry)
  }
  return out
}

fun displayEntries(page: List<ChatEntry>, older: List<ChatEntry>) = page + older

fun chatDayKey(iso: String, zone: TimeZone): String {
  val at = epoch(iso) ?: return iso
  return SimpleDateFormat("yyyy-MM-dd", Locale.ROOT).apply { timeZone = zone }.format(Date(at))
}

fun chatDay(iso: String, zone: TimeZone): String {
  val at = epoch(iso) ?: return iso
  return SimpleDateFormat("EEE d MMM", Locale.ENGLISH).apply { timeZone = zone }.format(Date(at))
}

fun chatClock(iso: String, zone: TimeZone): String {
  val at = epoch(iso) ?: return iso
  return SimpleDateFormat("HH:mm", Locale.ROOT).apply { timeZone = zone }.format(Date(at))
}

sealed interface ChatRow {
  val key: String

  data class Day(override val key: String, val label: String) : ChatRow

  data class Hand(override val key: String, val entry: ChatEntry) : ChatRow

  data class Wakes(override val key: String, val entry: ChatEntry) : ChatRow

  data class Message(override val key: String, val entry: ChatEntry, val mine: Boolean) : ChatRow
}

fun chatRows(entries: List<ChatEntry>, zone: TimeZone = TimeZone.getDefault()): List<ChatRow> {
  val rows = ArrayList<ChatRow>(entries.size + 2)
  for (index in entries.indices) {
    val entry = entries[index]
    rows +=
      when (entry.entry.role) {
        "hand" -> ChatRow.Hand(entry.key, entry)
        "wakes" -> ChatRow.Wakes(entry.key, entry)
        "operator" -> ChatRow.Message(entry.key, entry, true)
        else -> ChatRow.Message(entry.key, entry, false)
      }
    val next = entries.getOrNull(index + 1) ?: continue
    if (chatDayKey(entry.entry.at, zone) != chatDayKey(next.entry.at, zone)) {
      rows += ChatRow.Day("day:${entry.key}", chatDay(entry.entry.at, zone))
    }
  }
  return rows
}

private val EVENT_KIND = Regex("\\b(attempt|decision|report|supervisor|task|timer)\\.([a-z]+)")
private val REF_COLON = Regex("\\b([adprst][0-9]+):\\s*(;|$)")

fun handWords(text: String): String {
  val plain = text.removePrefix("wake: ")
  val named =
    EVENT_KIND.replace(plain) { "${it.groupValues[1]} ${it.groupValues[2]}" }
  return REF_COLON.replace(named) { it.groupValues[1] + it.groupValues[2] }.trim()
}

fun wakesSpan(wakes: List<TimelineEntry>, zone: TimeZone = TimeZone.getDefault()): String {
  val first = wakes.firstOrNull()?.let { chatClock(it.at, zone) }.orEmpty()
  val last = wakes.lastOrNull()?.let { chatClock(it.at, zone) }.orEmpty()
  return "$first–$last"
}

fun wakesWords(wakes: List<TimelineEntry>, zone: TimeZone = TimeZone.getDefault()) =
  "${wakes.size} wakes ${wakesSpan(wakes, zone)}"

fun agoWords(from: Long, now: Long): String {
  val seconds = ((now - from).coerceAtLeast(0) / 1_000).toInt()
  return when {
    seconds < 60 -> "${seconds}s"
    seconds < 3_600 -> "${seconds / 60}m"
    else -> "${seconds / 3_600}h ${(seconds % 3_600) / 60}m"
  }
}

fun elapsedWords(from: Long, now: Long): String {
  val minutes = ((now - from).coerceAtLeast(0) / 60_000).toInt()
  return when {
    minutes < 1 -> "<1m"
    minutes < 60 -> "${minutes}m"
    else -> "${minutes / 60}h ${minutes % 60}m"
  }
}

fun workingWords(
  working: TimelineWorking,
  now: Long,
  zone: TimeZone = TimeZone.getDefault()
): String {
  val out = StringBuilder("${working.ref} working")
  epoch(working.since)?.let { out.append(" · ").append(elapsedWords(it, now)) }
  epoch(working.last)?.let {
    out.append(" · last output ").append(agoWords(it, now)).append(" ago")
  }
  out.append(" · ")
  if (working.input != 0L && working.from.isNotEmpty()) {
    out.append("on your ").append(chatClock(working.from, zone)).append(" message")
  } else {
    out.append(working.on.ifEmpty { "since the turn began" })
  }
  return out.toString()
}

fun aboveBottom(firstIndex: Int, offset: Int, threshold: Int) =
  firstIndex != 0 || offset > threshold

fun latestCount(current: Int, above: Boolean, added: List<TimelineEntry>) =
  if (!above) 0 else current + added.count { it.role == "supervisor" }

class ChatModel(
  private val fleet: String,
  transport: Transport,
  private val bus: LiveBus = LiveBus.shared,
  private val pollMillis: Long = POLL_MILLIS,
  private val nowMillis: () -> Long = { System.currentTimeMillis() }
) : ViewModel() {
  private val mutableState = MutableStateFlow(ChatState(now = nowMillis()))
  val state: StateFlow<ChatState> = mutableState

  private val client = BoardClient(fleet, transport)
  private val lock = Mutex()
  private val olderLock = Mutex()
  @Volatile private var cursor: String? = null
  @Volatile private var supported: Boolean? = null
  @Volatile private var pending = false
  @Volatile private var gapReading = false

  suspend fun run() {
    pending = false
    load()
    if (mutableState.value.unpaired || mutableState.value.refused != null) return
    if (!eventsSupported()) return poll()
    coroutineScope {
      launch { tick() }
      bus.events.filter { it.fleet == fleet }.collect(::reduce)
    }
  }

  suspend fun poll(): Nothing {
    while (true) {
      delay(pollMillis)
      load()
    }
  }

  private suspend fun tick() {
    while (true) {
      delay(1_000)
      if (mutableState.value.working != null) mutableState.update { it.copy(now = nowMillis()) }
    }
  }

  private suspend fun eventsSupported(): Boolean {
    if (supported == true) return true
    val events =
      try {
        "events" in client.features()
      } catch (error: BoardError) {
        if (error == BoardError.Unpaired) {
          mutableState.update { it.copy(unpaired = true, loading = false) }
        }
        false
      }
    if (events) supported = true
    return events
  }

  suspend fun load() {
    do {
      pending = false
      lock.withLock {
        try {
          apply(client.readSince("timeline", cursor, TimelineData.serializer()))
        } catch (_: BoardError.BadCursor) {
          cursor = null
          try {
            apply(client.readSince("timeline", null, TimelineData.serializer()))
          } catch (error: BoardError) {
            failed(error)
          }
        } catch (error: BoardError) {
          failed(error)
        }
      }
    } while (pending)
  }

  private fun apply(poll: Poll<TimelineData>) {
    when (poll) {
      is Poll.Changed -> {
        cursor = poll.envelope.cursor
        fill(poll.envelope.data)
      }
      Poll.Unchanged -> mutableState.update { it.copy(loading = false, failed = false) }
    }
  }

  private fun fill(data: TimelineData) {
    val page = pageKeys(data.entries)
    val held = page.mapTo(HashSet()) { it.identity }
    mutableState.update { state ->
      val older = state.older.filterNot { it.identity in held }
      state.copy(
        page = page,
        older = older,
        before = if (older.isEmpty()) data.older else state.before,
        to = data.older + data.entries.size,
        live = data.live,
        note = data.note,
        working = data.working,
        loading = false,
        failed = false,
        refused = null,
        unpaired = false,
        olderFailed = false,
        now = nowMillis()
      )
    }
  }

  private fun failed(error: BoardError) {
    if (error == BoardError.Unpaired) {
      mutableState.update {
        it.copy(unpaired = true, loading = false, failed = false, olderLoading = false)
      }
      return
    }
    if (error is BoardError.Forbidden) {
      mutableState.update {
        it.copy(
          loading = false,
          failed = false,
          refused = error.message?.takeIf(String::isNotBlank) ?: "The Board refused the Chat.",
          olderLoading = false
        )
      }
      return
    }
    mutableState.update {
      it.copy(
        loading = false,
        failed = it.page.isEmpty() && it.older.isEmpty(),
        olderLoading = false
      )
    }
  }

  internal suspend fun reduce(event: LiveEvent) {
    when (event.topic) {
      "timeline" -> timeline(event)
      "resync" -> if ("timeline" in topics(event.json)) refreshHint()
      "touched" -> if ("timeline" in topics(event.json)) refreshHint()
    }
  }

  private suspend fun timeline(event: LiveEvent) {
    val data =
      try {
        boardJson.decodeFromString<TimelineEvent>(event.json)
      } catch (_: Exception) {
        return
      }
    val held = mutableState.value.to
    if (held != null) {
      if (data.to < held) return
      if (data.to == held && !data.snapshot && data.from != held) return
    }
    if (data.snapshot) {
      replace(data)
      return
    }
    if (event.gap || (held != null && data.from != held)) {
      reread()
      return
    }
    delta(data)
  }

  private suspend fun reread() {
    if (gapReading) return
    gapReading = true
    try {
      cursor = null
      if (lock.isLocked) pending = true else load()
    } finally {
      gapReading = false
    }
  }

  private fun eventEntries(entries: List<TimelineEntry>) =
    if (entries.all { it.key.isNotEmpty() }) entries.map { ChatEntry(it.key, it) }
    else pageKeys(entries)

  private fun replace(data: TimelineEvent) {
    val page = eventEntries(data.entries)
    val held = page.mapTo(HashSet()) { it.identity }
    mutableState.update { state ->
      val older = state.older.filterNot { it.identity in held }
      val known = state.page.mapTo(HashSet()) { it.identity }
      val added = data.entries.filter { it.role == "supervisor" && entryIdentity(it) !in known }
      state.copy(
        page = page,
        older = older,
        before = if (older.isEmpty()) data.older else state.before,
        to = data.to,
        live = data.live,
        note = data.note,
        working = data.working,
        loading = false,
        failed = false,
        refused = null,
        unpaired = false,
        olderFailed = false,
        newReplies = latestCount(state.newReplies, state.aboveBottom, added),
        now = nowMillis()
      )
    }
  }

  private fun delta(data: TimelineEvent) {
    mutableState.update { state ->
      val held = state.page.mapTo(HashSet()) { it.identity }
      val added = data.entries.filter { it.role == "supervisor" && entryIdentity(it) !in held }
      val page =
        mergeEntries(state.page, eventEntries(data.entries)).filterNot { it.key in data.drop }
      state.copy(
        page = page,
        before = if (state.older.isEmpty()) data.older else state.before,
        to = data.to,
        live = data.live,
        note = data.note,
        working = data.working,
        loading = false,
        failed = false,
        refused = null,
        unpaired = false,
        newReplies = latestCount(state.newReplies, state.aboveBottom, added),
        now = nowMillis()
      )
    }
  }

  fun scrolled(above: Boolean) {
    mutableState.update {
      if (above) {
        if (it.aboveBottom) it else it.copy(aboveBottom = true)
      } else {
        if (!it.aboveBottom && it.newReplies == 0) it
        else it.copy(aboveBottom = false, newReplies = 0)
      }
    }
  }

  fun retry() {
    if (!lock.isLocked) viewModelScope.launch { load() }
  }

  fun loadOlder() {
    viewModelScope.launch { older() }
  }

  suspend fun older() =
    olderLock.withLock {
      val state = mutableState.value
      if (state.before <= 0 || state.olderLoading) return@withLock
      val before = state.before
      mutableState.update { it.copy(olderLoading = true, olderFailed = false) }
      try {
        val data = client.read("timeline?before=$before", TimelineData.serializer()).data
        val fetched =
          pageKeys(data.entries).map { it.copy(key = "b$before/${it.key}") }
        mutableState.update { held ->
          val known = displayEntries(held.page, held.older).mapTo(HashSet()) { it.identity }
          held.copy(
            older = held.older + fetched.filterNot { it.identity in known },
            before = data.older,
            olderLoading = false,
            olderFailed = false,
            unpaired = false
          )
        }
      } catch (error: BoardError) {
        if (error == BoardError.Unpaired) {
          mutableState.update { it.copy(unpaired = true, olderLoading = false) }
        } else {
          mutableState.update {
            it.copy(olderLoading = false, olderFailed = it.page.isNotEmpty() || it.older.isNotEmpty())
          }
        }
      }
    }

  fun retryOlder() {
    if (!olderLock.isLocked) loadOlder()
  }

  private suspend fun refreshHint() {
    if (lock.isLocked) {
      pending = true
      return
    }
    load()
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
}
