package io.github.atqamz.handcompanion

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.Badge
import androidx.compose.material3.BadgedBox
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

internal val TABS_HEIGHT = 80.dp

@Composable
private fun TopBar(id: String, state: FleetState, onBack: () -> Unit, onWeb: () -> Unit) {
  val hand = LocalHand.current
  val mark = hand.tints[tint(id)]
  Box(Modifier.fillMaxWidth().height(3.dp).background(mark))
  Row(
    Modifier.fillMaxWidth().height(56.dp).padding(end = 4.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(8.dp)
  ) {
    IconButton(onBack) { Icon(HandIcons.Back, "Back", Modifier.size(20.dp)) }
    Icon(HandIcons.Mark, null, Modifier.size(22.dp), mark)
    Text(
      state.name ?: id,
      Modifier.weight(1f),
      maxLines = 1,
      overflow = TextOverflow.Ellipsis,
      style = type.headlineSmall
    )
    state.pill?.let { Chip(it.text, it.tone, 24, glyph(it.tone)) }
    Menu(listOf("Open web Board" to onWeb))
  }
  HorizontalDivider(color = hand.line)
}

fun tabLabel(tab: Tab, state: FleetState) =
  when {
    tab == Tab.Needs && state.waiting > 0 -> "Needs, ${state.waiting} waiting"
    tab == Tab.Chat && state.working -> "Chat, supervisor working"
    else -> tab.name
  }

@Composable
private fun RowScope.TabItem(
  tab: Tab,
  state: FleetState,
  onTab: (Tab) -> Unit,
  badge: (@Composable () -> Unit)?
) {
  val icon =
    when (tab) {
      Tab.Needs -> HandIcons.Needs
      Tab.Tasks -> HandIcons.Tasks
      Tab.Chat -> HandIcons.Chat
    }
  NavigationBarItem(
    selected = (state.tab ?: Tab.Chat) == tab,
    onClick = { onTab(tab) },
    modifier = Modifier.semantics { contentDescription = tabLabel(tab, state) },
    icon = {
      if (badge == null) Icon(icon, null, Modifier.size(24.dp))
      else BadgedBox({ badge() }) { Icon(icon, null, Modifier.size(24.dp)) }
    },
    label = { Text(tab.name) }
  )
}

@Composable
private fun Tabs(state: FleetState, onTab: (Tab) -> Unit) {
  val hand = LocalHand.current
  NavigationBar {
    TabItem(
      Tab.Needs,
      state,
      onTab,
      if (state.waiting > 0) {
        {
          Badge(containerColor = hand.fill(state.tone), contentColor = hand.ink(state.tone)) {
            Text("${state.waiting}")
          }
        }
      } else null
    )
    TabItem(Tab.Tasks, state, onTab, null)
    TabItem(
      Tab.Chat,
      state,
      onTab,
      if (state.working) {
        { Badge(containerColor = hand.ink(Tone.Accent)) }
      } else null
    )
  }
}

@Composable
internal fun Loading() {
  Column(
    Modifier.fillMaxSize().padding(32.dp),
    verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
    horizontalAlignment = Alignment.CenterHorizontally
  ) {
    Text("Loading…", style = type.titleMedium)
  }
}

@Composable
private fun Placeholder(tab: Tab, onWeb: () -> Unit) {
  Column(
    Modifier.fillMaxSize().padding(32.dp),
    verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
    horizontalAlignment = Alignment.CenterHorizontally
  ) {
    Text(tab.name, style = type.headlineSmall)
    Meta("Not built here yet. The web Board has it.")
    Action("Open web Board", onWeb)
  }
}

@Composable
fun FleetScreen(
  id: String,
  state: FleetState,
  onBack: () -> Unit,
  onRetry: () -> Unit,
  onTab: (Tab) -> Unit,
  onToggle: (String) -> Unit,
  onMarkRead: (String) -> Unit,
  onWeb: () -> Unit,
  onTask: (String) -> Unit,
  onShowAll: () -> Unit,
  onPress: (List<String>) -> Unit,
  onForce: () -> Unit,
  choice: @Composable (String) -> Unit
) {
  Column(Modifier.fillMaxSize().safeDrawingPadding()) {
    TopBar(id, state, onBack, onWeb)
    state.band?.let { BandView(it, onRetry) }
    Box(Modifier.weight(1f)) {
      when (body(state)) {
        Body.Unpaired ->
          Column(Modifier.fillMaxSize().padding(32.dp), Arrangement.Center, Alignment.CenterHorizontally) {
            Text("No longer paired", color = LocalHand.current.ink(Tone.Fail), style = type.titleMedium)
            Meta("Pair this phone again on the laptop's Board.")
          }
        Body.Loading -> Loading()
        Body.Needs -> NeedsScreen(state, onToggle, onMarkRead, onWeb, onPress, onForce, choice)
        Body.Tasks -> TasksScreen(state, onTask, onShowAll, onRetry)
        Body.Placeholder -> Placeholder(state.tab ?: Tab.Chat, onWeb)
      }
    }
    state.notice?.let {
      Text(
        it.text,
        Modifier.fillMaxWidth()
          .background(LocalHand.current.fill(Tone.Neutral))
          .semantics { liveRegion = LiveRegionMode.Polite }
          .padding(16.dp, 12.dp),
        style = type.bodyMedium
      )
    }
    Tabs(state, onTab)
  }
}
