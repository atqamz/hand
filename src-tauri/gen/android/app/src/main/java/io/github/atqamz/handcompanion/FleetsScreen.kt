package io.github.atqamz.handcompanion

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

internal val type
  @Composable get() = MaterialTheme.typography

internal fun glyph(tone: Tone) =
  when (tone) {
    Tone.Fail -> HandIcons.Failure
    Tone.Accent -> HandIcons.Running
    Tone.Pass -> HandIcons.Passing
    Tone.Wait -> HandIcons.Waiting
    else -> HandIcons.Idle
  }

@Composable
internal fun Label(text: String, color: Color) = Text(text, color = color, style = type.labelMedium)

@Composable
internal fun Meta(text: String, color: Color = LocalHand.current.muted) =
  Text(text, color = color, style = type.bodySmall)

@Composable
internal fun Chip(text: String, tone: Tone, height: Int, icon: ImageVector? = null) {
  val hand = LocalHand.current
  Row(
    Modifier.height(height.dp).background(hand.fill(tone), CircleShape).padding(horizontal = 8.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(4.dp)
  ) {
    if (icon != null) Icon(icon, null, Modifier.size(12.dp), hand.ink(tone))
    Label(text, hand.ink(tone))
  }
}

@Composable
internal fun Passing(text: String) {
  val pass = LocalHand.current.ink(Tone.Pass)
  Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(4.dp)) {
    Icon(HandIcons.Passing, null, Modifier.size(12.dp), pass)
    Label(text, pass)
  }
}

@Composable
internal fun Action(
  label: String,
  onClick: () -> Unit,
  modifier: Modifier = Modifier,
  enabled: Boolean = true
) {
  val hand = LocalHand.current
  OutlinedButton(
    onClick,
    modifier,
    enabled = enabled,
    shape = MaterialTheme.shapes.medium,
    border = BorderStroke(1.dp, hand.lineStrong),
    contentPadding = PaddingValues(horizontal = 14.dp)
  ) {
    Text(label, color = hand.fg, style = type.bodyMedium)
  }
}

@Composable
private fun Strip(laptop: String) {
  Row(
    Modifier.fillMaxWidth().background(LocalHand.current.panel).padding(16.dp, 10.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(8.dp)
  ) {
    Meta("Laptop")
    Text(laptop, style = HandMono)
    Spacer(Modifier.weight(1f))
    Passing("connected")
  }
}

@Composable
internal fun BandView(band: Band, onRetry: () -> Unit) {
  Row(
    Modifier.fillMaxWidth().background(LocalHand.current.fill(Tone.Wait)).padding(16.dp, 12.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(12.dp)
  ) {
    Column(Modifier.weight(1f)) {
      Text(band.title, style = type.bodyMedium.copy(fontWeight = FontWeight.SemiBold))
      band.body?.let { Text(it, style = type.bodyMedium) }
    }
    if (band.retry) Action("Retry", onRetry)
  }
}

private fun past(row: FleetRow, seen: String) =
  if (row.clear) "All clear at $seen"
  else listOfNotNull(row.badge?.text, "${row.open} open").joinToString(" · ") + " at $seen"

@Composable
private fun Fleet(row: FleetRow, stale: Boolean, onOpen: (String) -> Unit) {
  val hand = LocalHand.current
  Row(
    Modifier.fillMaxWidth()
      .heightIn(min = 76.dp)
      .clickable(enabled = row.paired) { onOpen(row.id) }
      .padding(16.dp, 12.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(12.dp)
  ) {
    Icon(
      HandIcons.Mark,
      null,
      Modifier.size(22.dp).alpha(if (stale) 0.5f else 1f),
      hand.tints[tint(row.id)]
    )
    Column(Modifier.weight(1f)) {
      Text(row.name, maxLines = 1, overflow = TextOverflow.Ellipsis, style = type.titleMedium)
      Row(
        Modifier.padding(top = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp)
      ) {
        when {
          !row.paired -> Meta("No longer paired", hand.ink(Tone.Fail))
          stale -> row.seen?.let { Meta(past(row, it)) }
          row.unreadable -> Meta("The Board's answer could not be read.", hand.ink(Tone.Fail))
          row.clear -> {
            Passing("All clear")
            row.since?.let { Meta("since $it") }
          }
          else -> {
            row.badge?.let { Chip(it.text, it.tone, 20) }
            Meta("${row.open} open")
          }
        }
      }
    }
    when {
      !row.paired -> Unit
      stale -> Chip("unknown", Tone.Neutral, 24, HandIcons.Idle)
      else -> row.pill?.let { Chip(it.text, it.tone, 24, glyph(it.tone)) }
    }
  }
  HorizontalDivider(color = hand.line)
}

@Composable
fun FleetsScreen(
  state: FleetsState,
  onRetry: () -> Unit,
  onAddFleet: () -> Unit,
  onOpen: (String) -> Unit
) {
  val line = LocalHand.current.line
  Column(Modifier.fillMaxSize().safeDrawingPadding()) {
    Row(
      Modifier.fillMaxWidth().height(56.dp).padding(start = 16.dp, end = 4.dp),
      verticalAlignment = Alignment.CenterVertically,
      horizontalArrangement = Arrangement.spacedBy(8.dp)
    ) {
      Text("Hand", Modifier.weight(1f), style = type.headlineSmall)
      Action("Add fleet", onAddFleet, Modifier.height(36.dp))
      IconButton({}) { Icon(HandIcons.More, "Menu", Modifier.size(20.dp)) }
    }
    HorizontalDivider(color = line)
    when {
      state.band != null -> BandView(state.band, onRetry)
      state.laptop != null -> Strip(state.laptop)
    }
    HorizontalDivider(color = line)
    LazyColumn {
      items(state.rows, key = { it.id }) { Fleet(it, state.band?.retry == true, onOpen) }
    }
  }
}
