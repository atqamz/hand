package io.github.atqamz.handcompanion

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
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
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
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
internal fun MenuItems(items: List<Pair<String, () -> Unit>>) =
  items.forEach { (label, act) -> DropdownMenuItem({ Text(label, style = type.bodyMedium) }, act) }

@Composable
internal fun Menu(items: List<Pair<String, () -> Unit>>) {
  var open by remember { mutableStateOf(false) }
  Box {
    IconButton({ open = true }) { Icon(HandIcons.More, "Menu", Modifier.size(20.dp)) }
    DropdownMenu(open, { open = false }) {
      MenuItems(items.map { (label, act) -> label to { open = false; act() } })
    }
  }
}

@Composable
internal fun Snack(text: String, modifier: Modifier = Modifier) {
  Surface(
    modifier.safeDrawingPadding().padding(16.dp).semantics { liveRegion = LiveRegionMode.Polite },
    shape = MaterialTheme.shapes.medium,
    color = MaterialTheme.colorScheme.inverseSurface,
    shadowElevation = 6.dp
  ) {
    Text(
      text,
      Modifier.padding(16.dp, 12.dp),
      color = MaterialTheme.colorScheme.inverseOnSurface,
      style = type.bodyMedium
    )
  }
}

@Composable
internal fun ForgetDialog(asking: Forgetting, onConfirm: () -> Unit, onCancel: () -> Unit) {
  val fleets =
    if (asking.fleets == 0) "" else " and its ${asking.fleets} fleet${if (asking.fleets == 1) "" else "s"}"
  val text =
    if (asking.endpoint == null) {
      "This Companion leaves every host and all ${asking.fleets} fleets. Alerts stop. " +
        "To use it again, pair with a new code from the Board."
    } else {
      "This Companion leaves ${asking.name ?: "your host"}$fleets. Alerts stop. " +
        "To use it again, pair with a new code from the Board."
    }
  AlertDialog(
    onDismissRequest = onCancel,
    title = { Text(if (asking.endpoint == null) "Forget everything?" else "Forget this host?") },
    text = { Text(text) },
    confirmButton = {
      TextButton(onConfirm) { Text("Forget", color = LocalHand.current.ink(Tone.Fail)) }
    },
    dismissButton = { TextButton(onCancel) { Text("Cancel") } }
  )
}

@Composable
private fun Strip(host: String) {
  Row(
    Modifier.fillMaxWidth().background(LocalHand.current.panel).padding(16.dp, 10.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(8.dp)
  ) {
    Meta("Host")
    Text(host, style = HandMono)
    Spacer(Modifier.weight(1f))
    Passing("connected")
  }
}

@Composable
private fun HostHeader(group: HostGroup, onRetry: () -> Unit) {
  Column {
    Row(
      Modifier.fillMaxWidth().background(LocalHand.current.panel).padding(16.dp, 10.dp),
      verticalAlignment = Alignment.CenterVertically,
      horizontalArrangement = Arrangement.spacedBy(8.dp)
    ) {
      Meta("Host")
      Text(group.name, Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis, style = HandMono)
      when {
        group.unpaired -> Meta("Not paired anymore", LocalHand.current.ink(Tone.Fail))
        group.band == null -> Passing("connected")
      }
    }
    group.band?.let { BandView(it.copy(body = null), onRetry) }
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
private fun Fleet(
  row: FleetRow,
  stale: Boolean,
  onOpen: (String) -> Unit,
  onPairAgain: () -> Unit,
  onForgetHost: () -> Unit
) {
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
      if (!row.paired) {
        Row(Modifier.padding(top = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
          Action("Pair again", onPairAgain, Modifier.height(36.dp))
          Action("Forget this host", onForgetHost, Modifier.height(36.dp))
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
  onOpen: (String) -> Unit,
  onAsk: (Forgetting?) -> Unit,
  onForget: () -> Unit,
  onSettings: () -> Unit,
  onPhone: () -> Unit = {}
) {
  val line = LocalHand.current.line
  val grouped = state.hosts.size > 1
  Column(Modifier.fillMaxSize().safeDrawingPadding()) {
    Row(
      Modifier.fillMaxWidth().height(56.dp).padding(start = 16.dp, end = 4.dp),
      verticalAlignment = Alignment.CenterVertically,
      horizontalArrangement = Arrangement.spacedBy(8.dp)
    ) {
      Text("Hand", Modifier.weight(1f), style = type.headlineSmall)
      Action("Add fleet", onAddFleet, Modifier.height(36.dp))
      if (state.hosts.isNotEmpty()) Menu(listOf("This Companion" to onPhone))
    }
    HorizontalDivider(color = line)
    val top = state.hosts.singleOrNull()?.band ?: state.band
    when {
      top != null -> BandView(top, onRetry)
      state.hosts.size == 1 -> Strip(state.hosts[0].name)
    }
    if (state.alertsOff) {
      Box(Modifier.clickable(onClick = onSettings)) {
        BandView(Band("Alerts are off. Allow notifications in Settings.", retry = false), {})
      }
    }
    state.notice?.let {
      Text(
        it,
        Modifier.fillMaxWidth().background(LocalHand.current.fill(Tone.Neutral)).padding(16.dp, 12.dp),
        style = type.bodyMedium
      )
    }
    HorizontalDivider(color = line)
    when (fleetsBody(state)) {
      FleetsBody.Loading -> Loading()
      FleetsBody.Rows ->
        LazyColumn {
          for (group in state.hosts) {
            if (grouped) item(key = group.endpoint) { HostHeader(group, onRetry) }
            items(group.rows, key = { it.id }) {
              Fleet(
                it,
                group.stale,
                onOpen,
                onAddFleet,
                { onAsk(Forgetting(group.endpoint, group.name, group.rows.size)) }
              )
            }
          }
        }
    }
  }
  state.asking?.let { ForgetDialog(it, onForget) { onAsk(null) } }
}
