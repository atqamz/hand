package io.github.atqamz.handcompanion

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import java.util.TimeZone

const val HISTORY_HINT =
  "Search what was said: your messages, the supervisor's replies, reports, decisions and task titles."

fun historyWhen(at: String, zone: TimeZone) = "${chatDay(at, zone)} ${chatClock(at, zone)}"

fun historyCount(state: HistoryState) =
  when {
    state.total == 1 -> "1 match"
    state.more && state.limit >= HISTORY_MAX -> "Showing the first ${state.hits.size} of ${state.total}. Narrow the search."
    state.more -> "Showing ${state.hits.size} of ${state.total}"
    else -> "${state.total} matches"
  }

@Composable
private fun Snippet(text: String) {
  val hand = LocalHand.current
  Text(
    buildAnnotatedString {
      historySnippet(text).forEach { (part, matched) ->
        if (matched) {
          withStyle(SpanStyle(fontWeight = FontWeight.Bold, background = hand.fill(Tone.Accent))) {
            append(part)
          }
        } else {
          append(part)
        }
      }
    },
    maxLines = 4,
    style = type.bodyMedium
  )
}

@Composable
private fun HitRow(hit: HistoryHit, zone: TimeZone, onOpen: (HistoryHit) -> Unit) {
  val hand = LocalHand.current
  val who = historyWho(hit.who)
  Column(
    Modifier.fillMaxWidth()
      .clickable { onOpen(hit) }
      .padding(16.dp, 10.dp)
      .semantics(mergeDescendants = true) { contentDescription = "$who ${hit.ref}, ${historyWhen(hit.at, zone)}" },
    verticalArrangement = Arrangement.spacedBy(4.dp)
  ) {
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
      Text(who, style = type.labelMedium)
      Text(hit.ref, color = hand.muted, style = HandMono)
      Meta(historyWhen(hit.at, zone))
    }
    Snippet(hit.text)
  }
  HorizontalDivider(color = hand.line)
}

@Composable
private fun Notice(title: String, detail: String? = null, action: (@Composable () -> Unit)? = null) {
  Column(
    Modifier.fillMaxSize().padding(32.dp),
    verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
    horizontalAlignment = Alignment.CenterHorizontally
  ) {
    Text(title, style = type.titleMedium)
    detail?.let { Meta(it) }
    action?.invoke()
  }
}

@Composable
private fun Hits(state: HistoryState, zone: TimeZone, onMore: () -> Unit, onOpen: (HistoryHit) -> Unit) {
  val hand = LocalHand.current
  LazyColumn(Modifier.fillMaxSize()) {
    item {
      Box(Modifier.padding(16.dp, 8.dp).semantics { liveRegion = LiveRegionMode.Polite }) {
        Meta(historyCount(state))
      }
    }
    state.problem?.let {
      item {
        Text(
          "Stale · $it",
          Modifier.fillMaxWidth().padding(16.dp, 4.dp).semantics { liveRegion = LiveRegionMode.Polite },
          color = hand.ink(Tone.Wait),
          style = type.bodySmall
        )
      }
    }
    items(state.hits, key = { it.ref }) { HitRow(it, zone, onOpen) }
    if (state.more && state.limit < HISTORY_MAX) {
      item {
        Box(Modifier.fillMaxWidth().padding(16.dp), contentAlignment = Alignment.Center) {
          Action("More matches", onMore, enabled = !state.searching)
        }
      }
    }
  }
}

@Composable
private fun SearchBody(
  state: HistoryState,
  zone: TimeZone,
  onRetry: () -> Unit,
  onMore: () -> Unit,
  onOpen: (HistoryHit) -> Unit
) {
  when {
    state.unpaired -> Notice("No longer paired", "Pair this Companion again on the host's Board.")
    state.searching && state.hits.isEmpty() -> Loading()
    state.problem != null && state.hits.isEmpty() ->
      Notice("Couldn't search.", state.problem) { Action("Retry", onRetry) }
    state.searched.isEmpty() -> Notice("Search the conversation", HISTORY_HINT)
    state.hits.isEmpty() -> Notice("No matches", "Nothing said here matches “${state.searched}”.")
    else -> Hits(state, zone, onMore, onOpen)
  }
}

@Composable
private fun EntryBody(
  open: HistoryOpen,
  zone: TimeZone,
  onRetry: () -> Unit,
  onShow: (HistoryHit) -> Unit,
  onTarget: (ThreadTarget) -> Unit
) {
  val hand = LocalHand.current
  val entry = open.entry
  when {
    open.loading -> Loading()
    entry == null -> Notice("Couldn't read the entry.", open.problem) { Action("Retry", onRetry) }
    else ->
      Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp)
      ) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
          Text(historyWho(entry.who), style = type.labelMedium)
          Text(entry.ref, color = hand.muted, style = HandMono)
          Meta(historyWhen(entry.at, zone))
        }
        if (entry.task.isNotEmpty()) Meta("Task ${entry.task}")
        SelectionContainer { Text(entry.text, style = type.bodyMedium) }
        if (entry.truncated) Meta("Cut at 16 KiB.")
        when (entry.ref.firstOrNull()) {
          'i',
          'y' -> Action("Show in conversation", { onShow(open.hit) })
          else ->
            Action(
              "Open ${historyWho(entry.who).lowercase()}",
              { onTarget(historyTarget(entry.ref)) }
            )
        }
      }
  }
}

@Composable
fun HistoryScreen(
  state: HistoryState,
  onBack: () -> Unit,
  onEdit: (String) -> Unit,
  onSearch: () -> Unit,
  onRetry: () -> Unit,
  onMore: () -> Unit,
  onOpen: (HistoryHit) -> Unit,
  onCloseEntry: () -> Unit,
  onRetryEntry: () -> Unit,
  onShow: (HistoryHit) -> Unit,
  onTarget: (ThreadTarget) -> Unit,
  zone: TimeZone = TimeZone.getDefault()
) {
  val hand = LocalHand.current
  val open = state.open
  Column(Modifier.fillMaxSize().safeDrawingPadding()) {
    Row(
      Modifier.fillMaxWidth().height(56.dp).padding(end = 4.dp),
      verticalAlignment = Alignment.CenterVertically,
      horizontalArrangement = Arrangement.spacedBy(8.dp)
    ) {
      IconButton(if (open != null) onCloseEntry else onBack) {
        Icon(HandIcons.Back, "Back", Modifier.size(20.dp))
      }
      Text(if (open != null) open.hit.ref else "Search", Modifier.weight(1f), style = type.headlineSmall)
    }
    HorizontalDivider(color = hand.line)
    if (open != null) {
      Box(Modifier.weight(1f)) { EntryBody(open, zone, onRetryEntry, onShow, onTarget) }
    } else {
      OutlinedTextField(
        state.query,
        onEdit,
        Modifier.fillMaxWidth().padding(16.dp, 12.dp),
        placeholder = { Text("Search the conversation", color = hand.muted) },
        trailingIcon = {
          IconButton(onSearch, enabled = state.query.isNotBlank()) {
            Icon(HandIcons.Search, "Search", Modifier.size(20.dp))
          }
        },
        singleLine = true,
        shape = MaterialTheme.shapes.large,
        keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
        keyboardActions = KeyboardActions(onSearch = { onSearch() })
      )
      HorizontalDivider(color = hand.line)
      Box(Modifier.weight(1f)) { SearchBody(state, zone, onRetry, onMore, onOpen) }
    }
  }
}
