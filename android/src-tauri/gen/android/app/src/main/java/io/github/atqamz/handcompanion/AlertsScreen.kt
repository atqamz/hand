package io.github.atqamz.handcompanion

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import java.util.TimeZone

@Composable
private fun StatCell(label: String, value: String, tone: Tone) {
  val hand = LocalHand.current
  Row(
    Modifier.semantics(mergeDescendants = true) { contentDescription = "$value $label" },
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(6.dp)
  ) {
    Text(value, color = hand.ink(tone), style = HandMono)
    Meta(label)
  }
}

@OptIn(androidx.compose.foundation.layout.ExperimentalLayoutApi::class)
@Composable
private fun Stats(stats: AlertStats) {
  FlowRow(
    Modifier.fillMaxWidth().padding(16.dp, 8.dp),
    horizontalArrangement = Arrangement.spacedBy(16.dp),
    verticalArrangement = Arrangement.spacedBy(6.dp)
  ) {
    alertStatCells(stats).forEach { (label, value) ->
      val tone =
        when {
          label == "not acknowledged" && stats.unacked > 0 -> Tone.Wait
          label == "slow" && stats.slow > 0 -> Tone.Wait
          else -> Tone.Neutral
        }
      StatCell(label, value, tone)
    }
  }
}

@Composable
private fun AlertRow(line: AlertLine) {
  val hand = LocalHand.current
  Column(
    Modifier.fillMaxWidth()
      .padding(16.dp, 10.dp)
      .semantics(mergeDescendants = true) {
        contentDescription =
          "${line.kind} ${line.ref}, ${line.raised}, ${line.delivered}, ${line.acknowledged}, ${line.delay}" +
            if (line.slow) ", slow" else ""
      },
    verticalArrangement = Arrangement.spacedBy(4.dp)
  ) {
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
      Text(line.kind, Modifier.weight(1f, fill = false), style = HandMono)
      if (line.ref.isNotEmpty()) Meta(line.ref)
      if (line.slow) Chip("slow", Tone.Wait, 24, glyph(Tone.Wait))
    }
    Text(line.text, style = type.bodyMedium)
    Meta("Raised ${line.raised}")
    Meta("Delivered ${line.delivered}")
    Meta("Acknowledged ${line.acknowledged}")
    Meta("Delay ${line.delay}", if (line.slow) hand.ink(Tone.Wait) else hand.muted)
  }
  HorizontalDivider(color = hand.line)
}

@Composable
private fun AlertsList(data: AlertsData, zone: TimeZone) {
  val hand = LocalHand.current
  LazyColumn(Modifier.fillMaxSize()) {
    item { Text(ALERTS_NOTE, Modifier.padding(16.dp, 12.dp, 16.dp, 0.dp), color = hand.muted, style = type.bodySmall) }
    item { Stats(data.stats) }
    item { HorizontalDivider(color = hand.line) }
    if (data.alerts.isEmpty()) {
      item { Text(ALERTS_NONE, Modifier.padding(16.dp), color = hand.muted, style = type.bodyMedium) }
    }
    items(data.alerts, key = { it.seq }) { AlertRow(alertLine(it, zone)) }
  }
}

@Composable
private fun AlertsProblem(text: String, onRetry: () -> Unit) {
  Column(
    Modifier.fillMaxSize().padding(32.dp),
    verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
    horizontalAlignment = Alignment.CenterHorizontally
  ) {
    Text("Couldn't read the alerts.", style = type.titleMedium)
    Meta(text)
    Action("Retry", onRetry)
  }
}

@Composable
fun AlertsScreen(
  state: AlertsState,
  onBack: () -> Unit,
  onRetry: () -> Unit,
  zone: TimeZone = TimeZone.getDefault()
) {
  val hand = LocalHand.current
  Column(Modifier.fillMaxSize().safeDrawingPadding()) {
    Row(
      Modifier.fillMaxWidth().height(56.dp).padding(end = 4.dp),
      verticalAlignment = Alignment.CenterVertically,
      horizontalArrangement = Arrangement.spacedBy(8.dp)
    ) {
      IconButton(onBack) { Icon(HandIcons.Back, "Back", Modifier.size(20.dp)) }
      Text("Alerts", Modifier.weight(1f), style = type.headlineSmall)
      if (state.data != null && !state.loading) Action("Refresh", onRetry)
    }
    HorizontalDivider(color = hand.line)
    Box(Modifier.weight(1f)) {
      when {
        state.unpaired ->
          Column(Modifier.fillMaxSize().padding(32.dp), Arrangement.Center, Alignment.CenterHorizontally) {
            Text("No longer paired", color = hand.ink(Tone.Fail), style = type.titleMedium)
            Meta("Pair this Companion again on the host's Board.")
          }
        state.tooOld ->
          Column(Modifier.fillMaxSize().padding(32.dp), Arrangement.Center, Alignment.CenterHorizontally) {
            Meta(ALERTS_TOO_OLD)
          }
        state.data != null ->
          Column(Modifier.fillMaxSize()) {
            if (state.problem != null) {
              Text(
                "Stale · ${state.problem}",
                Modifier.fillMaxWidth().padding(16.dp, 8.dp).semantics { liveRegion = LiveRegionMode.Polite },
                color = hand.ink(Tone.Wait),
                style = type.bodySmall
              )
            }
            AlertsList(state.data, zone)
          }
        state.problem != null -> AlertsProblem(state.problem, onRetry)
        else -> Loading()
      }
    }
  }
}
