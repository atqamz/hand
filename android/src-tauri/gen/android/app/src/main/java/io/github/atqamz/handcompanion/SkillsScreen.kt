package io.github.atqamz.handcompanion

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

@Composable
private fun SkillRow(skill: SkillInfo) {
  val hand = LocalHand.current
  val tone = skillTone(skill)
  val word = skillStateWord(skill)
  val source = skillSource(skill)
  val note = skillNote(skill)
  Column(
    Modifier.fillMaxWidth()
      .padding(16.dp, 10.dp)
      .semantics(mergeDescendants = true) { contentDescription = "${skill.name}, $word" },
    verticalArrangement = Arrangement.spacedBy(4.dp)
  ) {
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
      Text(skill.name, Modifier.weight(1f, fill = false), style = HandMono)
      Chip(word, tone, 24, glyph(tone))
      skillTrust(skill)?.let { Meta(it) }
    }
    if (source.isNotEmpty()) Text(source, color = hand.muted, style = HandMono)
    if (note.isNotEmpty()) Meta(note, if (tone == Tone.Fail) hand.ink(Tone.Fail) else hand.muted)
  }
  HorizontalDivider(color = hand.line)
}

@Composable
private fun SyncOutcomeView(outcome: SyncOutcome) {
  val hand = LocalHand.current
  Column(
    Modifier.fillMaxWidth().padding(16.dp, 8.dp).semantics { liveRegion = LiveRegionMode.Polite },
    verticalArrangement = Arrangement.spacedBy(4.dp)
  ) {
    outcome.problem?.let { Text(it, color = hand.ink(Tone.Fail), style = type.bodyMedium) }
    outcome.receipt?.let { Text(it, style = type.bodyMedium) }
    outcome.results.forEach { result ->
      Text(
        listOf(result.name, result.state, result.detail).filter(String::isNotEmpty).joinToString(" · "),
        color = hand.muted,
        style = HandMono
      )
    }
    if (outcome.refused > 0) Meta("${outcome.refused} refused")
  }
}

@Composable
private fun SkillsList(state: SkillsState, data: SkillsData, onSync: (SyncKey) -> Unit) {
  val hand = LocalHand.current
  LazyColumn(Modifier.fillMaxSize()) {
    item {
      Text(
        if (state.canSync) SKILLS_READ_ONLY else SKILLS_HOST_ONLY,
        Modifier.padding(16.dp, 12.dp, 16.dp, 0.dp),
        color = hand.muted,
        style = type.bodySmall
      )
    }
    data.scopes.forEach { scope ->
      val key = SyncKey(scope.scope, scope.project)
      item { SectionHead(skillScopeTitle(scope)) }
      if (skillSyncShown(state, scope)) {
        item {
          val running = state.syncing == key
          Action(
            if (running) "Syncing…" else "Sync",
            { onSync(key) },
            Modifier.padding(16.dp, 4.dp),
            enabled = state.syncing == null && !state.loading
          )
        }
      }
      state.outcome?.takeIf { it.key == key }?.let { outcome -> item { SyncOutcomeView(outcome) } }
      if (scope.error.isNotEmpty()) {
        item {
          Text(
            scope.error,
            Modifier.fillMaxWidth().padding(16.dp, 8.dp).semantics { liveRegion = LiveRegionMode.Polite },
            color = hand.ink(Tone.Fail),
            style = type.bodyMedium
          )
        }
      } else if (scope.skills.isEmpty()) {
        item { Text("No skills in this scope.", Modifier.padding(16.dp, 4.dp), color = hand.muted, style = type.bodySmall) }
      }
      scope.skills.forEach { skill -> item { SkillRow(skill) } }
    }
  }
}

@Composable
private fun SkillsProblem(text: String, onRetry: () -> Unit) {
  Column(
    Modifier.fillMaxSize().padding(32.dp),
    verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
    horizontalAlignment = Alignment.CenterHorizontally
  ) {
    Text("Couldn't read the skills.", style = type.titleMedium)
    Meta(text)
    Action("Retry", onRetry)
  }
}

@Composable
fun SkillsScreen(
  state: SkillsState,
  onBack: () -> Unit,
  onRetry: () -> Unit,
  onSync: (SyncKey) -> Unit = {}
) {
  val hand = LocalHand.current
  Column(Modifier.fillMaxSize().safeDrawingPadding()) {
    Row(
      Modifier.fillMaxWidth().height(56.dp).padding(end = 4.dp),
      verticalAlignment = Alignment.CenterVertically,
      horizontalArrangement = Arrangement.spacedBy(8.dp)
    ) {
      IconButton(onBack) { Icon(HandIcons.Back, "Back", Modifier.size(20.dp)) }
      Text("Skills", Modifier.weight(1f), style = type.headlineSmall)
      if (state.data != null && !state.loading && state.syncing == null) Action("Check again", onRetry)
    }
    HorizontalDivider(color = hand.line)
    Box(Modifier.weight(1f)) {
      when {
        state.unpaired ->
          Column(Modifier.fillMaxSize().padding(32.dp), Arrangement.Center, Alignment.CenterHorizontally) {
            Text("No longer paired", color = hand.ink(Tone.Fail), style = type.titleMedium)
            Meta("Pair this Companion again on the host's Board.")
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
            SkillsList(state, state.data, onSync)
          }
        state.problem != null -> SkillsProblem(state.problem, onRetry)
        else -> Loading()
      }
    }
  }
}
