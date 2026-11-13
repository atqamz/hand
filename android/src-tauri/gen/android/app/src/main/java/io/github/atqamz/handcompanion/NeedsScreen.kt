package io.github.atqamz.handcompanion

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

private val FIXED_KEYS = listOf("enter", "esc", "up", "down", "1", "2", "3")

private fun title(need: Need) =
  need.title ?: "${need.word.lowercase().replaceFirstChar(Char::uppercase)} ${need.ref.orEmpty()}".trim()

private fun supervisorLabel(supervisor: Supervisor?) =
  supervisor?.let { s ->
    listOfNotNull(s.harness, s.model, s.effort).joinToString(" ").takeIf(String::isNotEmpty)
  }

private fun meta(entry: Entry, supervisor: Supervisor?, sent: String?): String {
  if (sent != null) return sent
  val need = entry.need
  return when (need.kind) {
    "blocked" -> listOfNotNull(need.ref, supervisorLabel(supervisor)).joinToString(" · ")
    else -> listOfNotNull(need.ref, need.task, need.createdAt?.let(::localTime)).joinToString(" · ")
  }
}

@Composable
private fun NeedRow(entry: Entry, supervisor: Supervisor?, sent: String?, onClick: (() -> Unit)?) {
  val hand = LocalHand.current
  val tone = toneOf(entry.need.tone)
  Row(
    Modifier.fillMaxWidth()
      .heightIn(min = 52.dp)
      .clickable(enabled = onClick != null) { onClick?.invoke() }
      .semantics { if (entry.opens == Opens.Card || entry.opens == Opens.Choice) stateDescription = "Collapsed" }
      .padding(16.dp, 8.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(12.dp)
  ) {
    Icon(glyph(tone), null, Modifier.size(20.dp), hand.ink(tone))
    Column(Modifier.weight(1f)) {
      Text(title(entry.need), maxLines = 1, overflow = TextOverflow.Ellipsis, style = type.bodyLarge)
      Meta(meta(entry, supervisor, sent))
    }
    if (onClick != null) {
      Icon(
        HandIcons.Chevron,
        null,
        Modifier.size(16.dp).rotate(if (entry.opens == Opens.Web) -90f else 0f),
        hand.muted
      )
    }
  }
  HorizontalDivider(color = hand.line)
}

@Composable
private fun Countdown(left: String) {
  val hand = LocalHand.current
  Row(
    Modifier.height(22.dp).background(hand.bg, CircleShape).padding(horizontal = 8.dp),
    verticalAlignment = Alignment.CenterVertically
  ) {
    Label(left, hand.ink(Tone.Fail))
  }
}

@Composable
private fun NeedCard(
  entry: Entry,
  supervisor: Supervisor?,
  sent: String?,
  countdown: String?,
  onClose: () -> Unit,
  content: @Composable ColumnScope.() -> Unit
) {
  val hand = LocalHand.current
  val need = entry.need
  val tone = toneOf(need.tone)
  val shape = MaterialTheme.shapes.large
  Column(Modifier.fillMaxWidth().padding(16.dp, 8.dp).clip(shape).border(1.dp, hand.line, shape)) {
    Row(
      Modifier.fillMaxWidth()
        .background(hand.fill(tone))
        .clickable(onClick = onClose)
        .semantics { stateDescription = "Expanded" }
        .padding(16.dp, 12.dp),
      horizontalArrangement = Arrangement.spacedBy(12.dp)
    ) {
      Icon(glyph(tone), null, Modifier.size(20.dp), hand.ink(tone))
      Column(Modifier.weight(1f)) {
        Text(title(need), style = type.titleMedium)
        Meta(meta(entry, supervisor, sent))
      }
      countdown?.let { Countdown(it) }
    }
    Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp), content = content)
  }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun KeyRow(
  keys: List<Pair<String, List<String>>>,
  enabled: Boolean,
  onPress: (List<String>) -> Unit
) {
  FlowRow(
    Modifier.fillMaxWidth(),
    horizontalArrangement = Arrangement.spacedBy(8.dp),
    verticalArrangement = Arrangement.spacedBy(8.dp)
  ) {
    keys.forEach { (label, press) -> Action(label, { onPress(press) }, enabled = enabled) }
  }
}

@Composable
private fun Misread(ref: String, onForce: () -> Unit) {
  val hand = LocalHand.current
  var open by remember(ref) { mutableStateOf(false) }
  Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
    Row(
      Modifier.fillMaxWidth()
        .clickable { open = !open }
        .semantics {
          role = Role.Button
          stateDescription = if (open) "Expanded" else "Collapsed"
        }
        .padding(vertical = 4.dp),
      verticalAlignment = Alignment.CenterVertically,
      horizontalArrangement = Arrangement.spacedBy(4.dp)
    ) {
      Icon(
        HandIcons.Chevron,
        null,
        Modifier.size(16.dp).rotate(if (open) 0f else -90f),
        hand.muted
      )
      Text("Screen misread?", color = hand.muted, style = type.bodyMedium)
    }
    if (open) {
      Meta("Types the queued messages, or else the wakes, into its prompt.")
      Action("Type queued messages anyway", onForce)
    }
  }
}

@Composable
private fun BlockedBody(
  need: Need,
  supervisor: Supervisor?,
  pressing: Boolean,
  onPress: (List<String>) -> Unit,
  onForce: () -> Unit
) {
  val hand = LocalHand.current
  need.excerpt?.let {
    Box(
      Modifier.fillMaxWidth()
        .clip(MaterialTheme.shapes.medium)
        .background(hand.screen)
        .padding(12.dp)
    ) {
      Text(it, color = hand.screenFg, style = HandMono)
    }
  }
  val ask = supervisor?.ask
  val enabled = supervisor?.keys != null && !pressing
  if (ask != null) {
    KeyRow(ask.options.map { it.label to it.keys }, enabled, onPress)
  } else {
    KeyRow(FIXED_KEYS.map { it to listOf(it) }, enabled, onPress)
  }
  Misread(need.ref.orEmpty(), onForce)
}

@Composable
private fun ReportBody(need: Need, marking: Boolean, onMarkRead: () -> Unit, onWeb: () -> Unit) {
  need.excerpt?.let { Text(it, style = type.bodyMedium) }
  Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
    Button(onMarkRead, enabled = !marking, shape = MaterialTheme.shapes.medium) {
      Text("Mark read")
    }
    if (need.cut) Action("Open web Board", onWeb, Modifier.height(40.dp))
  }
}

@Composable
private fun AllClear(state: FleetState) {
  val hand = LocalHand.current
  Column(
    Modifier.fillMaxSize().padding(32.dp),
    verticalArrangement = Arrangement.spacedBy(8.dp, Alignment.CenterVertically),
    horizontalAlignment = Alignment.CenterHorizontally
  ) {
    Icon(HandIcons.Passing, null, Modifier.size(40.dp), hand.ink(Tone.Pass))
    Text("All clear", style = type.headlineSmall)
    Meta(
      listOfNotNull(state.cleared?.let { "since $it" }, state.summary).joinToString(" · "),
    )
  }
}

@Composable
fun NeedsScreen(
  state: FleetState,
  onToggle: (String) -> Unit,
  onMarkRead: (String) -> Unit,
  onWeb: () -> Unit,
  onPress: (List<String>) -> Unit,
  onForce: () -> Unit,
  choice: @Composable (String) -> Unit
) {
  if (state.bands.isEmpty()) return AllClear(state)
  val open = state.openKey()
  LazyColumn(Modifier.fillMaxSize()) {
    for (band in state.bands) {
      item(key = band.title) {
        Text(
          band.title,
          Modifier.fillMaxWidth().semantics { heading() }.padding(16.dp, 16.dp, 16.dp, 4.dp),
          color = LocalHand.current.ink(band.tone),
          style = type.labelLarge,
          textAlign = TextAlign.Start
        )
      }
      items(band.entries, key = { it.key }) { entry ->
        val expands = entry.opens == Opens.Card || entry.opens == Opens.Choice
        val ref = entry.need.ref.orEmpty()
        val sent = state.pressed[entry.key]
        when {
          expands && entry.key == open ->
            NeedCard(
              entry,
              state.supervisor,
              sent,
              if (entry.need.kind == "blocked") state.left else null,
              { onToggle(entry.key) }
            ) {
              when {
                entry.need.kind == "blocked" ->
                  BlockedBody(
                    entry.need,
                    state.supervisor,
                    entry.key in state.pressing,
                    onPress,
                    onForce
                  )
                entry.opens == Opens.Card ->
                  ReportBody(entry.need, ref in state.marking, { onMarkRead(ref) }, onWeb)
                else -> choice(ref)
              }
            }
          expands -> NeedRow(entry, state.supervisor, sent) { onToggle(entry.key) }
          entry.opens == Opens.Web -> NeedRow(entry, state.supervisor, sent, onWeb)
          else -> NeedRow(entry, state.supervisor, sent, null)
        }
      }
    }
    if (state.more > 0) {
      item { Box(Modifier.padding(16.dp)) { Meta("${state.more} more waiting") } }
    }
  }
}
