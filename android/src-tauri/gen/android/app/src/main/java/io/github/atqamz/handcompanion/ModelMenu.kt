package io.github.atqamz.handcompanion

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp

@Composable
internal fun ModelMenu(ui: ModelsUi) {
  var open by remember { mutableStateOf(false) }
  Box {
    IconButton({ open = true }) { Icon(HandIcons.Chevron, "Models", Modifier.size(20.dp)) }
    DropdownMenu(open, { open = false }) { ModelMenuBody(ui) }
  }
}

@Composable
internal fun ModelMenuBody(ui: ModelsUi) {
  val hand = LocalHand.current
  val state = ui.state
  state.choices.forEach { choice ->
    DropdownMenuItem(
      {
        Text(
          if (state.switching == choice.name) "${choice.text} · switching…" else choice.text,
          style = type.bodyMedium
        )
      },
      { if (!choice.current) ui.onPick(choice.name) },
      Modifier.semantics { selected = choice.current },
      trailingIcon = {
        if (choice.current) Icon(HandIcons.Copied, "Current", Modifier.size(16.dp), hand.ink(Tone.Accent))
      },
      enabled = !state.busy
    )
  }
  state.pending?.let { Text("next turn: $it", Modifier.padding(horizontal = 12.dp, vertical = 4.dp), color = hand.muted, style = type.bodySmall) }
  HorizontalDivider(color = hand.line)
  DropdownMenuItem(
    { Text(if (state.reloading) "Reloading models…" else "Reload models", style = type.bodyMedium) },
    ui.onReload,
    enabled = !state.busy
  )
  state.error?.let {
    Text(
      it,
      Modifier.padding(horizontal = 12.dp, vertical = 4.dp).semantics { liveRegion = LiveRegionMode.Polite },
      color = hand.ink(Tone.Fail),
      style = type.bodySmall
    )
  }
}
