package io.github.atqamz.handcompanion

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.focusable
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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

private val BRIEF_HEIGHT = 108.dp
private val BRIEF_OPEN_HEIGHT = 360.dp

@Composable
private fun AttemptHead(detail: AttemptDetail, onTarget: (ThreadTarget) -> Unit) {
  val hand = LocalHand.current
  val attempt = detail.attempt
  val tone = stateTone(detail.state)
  val facts = attemptFacts(detail)
  Column(Modifier.fillMaxWidth().padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
    Text(attemptTitle(attempt), style = type.headlineMedium)
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
      Chip(attempt.status.ifEmpty { detail.state }, tone, 24, glyph(tone))
      if (facts.isNotEmpty())
        Text(
          facts,
          Modifier.weight(1f),
          color = hand.muted,
          style = type.bodySmall,
          maxLines = 2,
          overflow = TextOverflow.Ellipsis
        )
    }
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
      Meta("for ")
      Text(
        detail.task.ref,
        Modifier.clickable { onTarget(ThreadTarget.Task(detail.task.ref)) },
        color = hand.ink(Tone.Accent),
        style = HandMono
      )
      Text(
        detail.task.title,
        Modifier.weight(1f),
        maxLines = 2,
        overflow = TextOverflow.Ellipsis,
        style = type.bodyMedium
      )
    }
    if (detail.modelNote.isNotEmpty()) Meta(detail.modelNote)
  }
  HorizontalDivider(color = hand.line)
}

@Composable
private fun BriefBox(text: String, open: Boolean, onToggle: () -> Unit) {
  val hand = LocalHand.current
  val shape = MaterialTheme.shapes.large
  Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp)) {
    Box(
      Modifier.fillMaxWidth()
        .clip(shape)
        .border(1.dp, hand.line, shape)
        .background(hand.panel)
        .then(
          if (open) Modifier.heightIn(min = BRIEF_HEIGHT, max = BRIEF_OPEN_HEIGHT).verticalScroll(rememberScrollState())
          else Modifier.height(BRIEF_HEIGHT)
        )
        .focusable()
        .padding(12.dp)
    ) {
      Text(shown(text), style = HandMono)
    }
    Text(
      if (open) "Show less" else "Show the whole brief",
      Modifier.clickable(onClick = onToggle)
        .semantics { role = Role.Button }
        .padding(vertical = 10.dp),
      color = hand.ink(Tone.Accent),
      style = type.bodyMedium
    )
  }
}

@Composable
private fun TurnRow(turn: AttemptTurn) {
  val hand = LocalHand.current
  Column(Modifier.fillMaxWidth().padding(16.dp, 8.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
      Text(
        turn.role,
        color = hand.fg,
        style = HandMono.copy(fontWeight = FontWeight.SemiBold)
      )
      localTime(turn.at)?.let { Meta(it) }
      if (turn.queued) Chip("queued", Tone.Neutral, 20)
    }
    Text(shown(turn.text), style = type.bodyLarge)
  }
  HorizontalDivider(color = hand.line)
}

@Composable
private fun TranscriptSection(transcript: AttemptTranscript) {
  SectionHead("Transcript")
  if (transcript.model.isNotEmpty()) {
    Box(Modifier.padding(16.dp, 4.dp)) { Meta(transcript.model) }
  }
  if (transcript.turns.isEmpty()) {
    Box(Modifier.padding(16.dp, 4.dp)) { Meta(transcriptEmpty(transcript)) }
  }
}

@Composable
private fun AttemptContent(
  fleet: String,
  ref: String,
  state: AttemptState,
  onBrief: () -> Unit,
  onMarkRead: (String) -> Unit,
  onTarget: (ThreadTarget) -> Unit
) {
  val detail = state.detail ?: return
  val hand = LocalHand.current
  LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 24.dp)) {
    item(key = "head") { AttemptHead(detail, onTarget) }
    item(key = "brief-head") { SectionHead("Brief") }
    item(key = "brief") {
      if (detail.brief.isEmpty()) {
        Box(Modifier.padding(16.dp, 4.dp)) { Meta("No brief is recorded for this attempt.") }
      } else {
        BriefBox(detail.brief, state.briefOpen, onBrief)
      }
    }
    if (detail.transcript.shown) {
      item(key = "transcript-head") { TranscriptSection(detail.transcript) }
      items(detail.transcript.turns) { TurnRow(it) }
      if (detail.transcript.more) item(key = "transcript-more") {
        Box(Modifier.padding(16.dp, 4.dp)) { Meta(TRANSCRIPT_MORE) }
      }
    }
    item(key = "reports-head") { SectionHead("Reports") }
    if (detail.reports.isEmpty()) {
      item(key = "reports-empty") {
        Box(Modifier.padding(16.dp, 4.dp)) { Meta("No reports yet.") }
      }
    } else {
      items(detail.reports, key = { "report:${it.ref}" }) { report ->
        ReportCard(
          report,
          detail.task.ref,
          fleet,
          report.ref in state.marking,
          state.errors[report.ref],
          onMarkRead,
          onTarget
        )
      }
      if (detail.moreReports) item(key = "reports-more") {
        Box(Modifier.padding(16.dp, 4.dp)) { Meta(reportsMore(ref)) }
      }
    }
    if (detail.delivered.isNotEmpty()) {
      item(key = "delivered-head") { SectionHead("Delivered") }
      items(detail.delivered, key = { "delivered:${it.kind}:${it.ref}" }) { Delivered(it, fleet, onTarget) }
    }
    item(key = "events-head") { SectionHead("Events") }
    items(detail.events, key = { "event:${it.seq}" }) { EventRow(it) }
    if (detail.moreEvents) item(key = "events-more") {
      Box(Modifier.padding(16.dp, 4.dp)) { Meta(EVENTS_MORE) }
    }
    state.notice?.let { text ->
      item(key = "notice") {
        Text(
          text,
          Modifier.fillMaxWidth()
            .padding(16.dp)
            .semantics { liveRegion = LiveRegionMode.Polite },
          color = hand.muted,
          style = type.bodyMedium
        )
      }
    }
  }
}

@Composable
private fun AttemptFailed(ref: String, onRetry: () -> Unit) {
  Column(
    Modifier.fillMaxSize().padding(32.dp),
    verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
    horizontalAlignment = Alignment.CenterHorizontally
  ) {
    Text("Couldn't read $ref.", style = type.titleMedium)
    Meta("The Board's answer was not read.")
    Action("Retry", onRetry)
  }
}

@Composable
private fun AttemptRefused(message: String, onBack: () -> Unit) {
  Column(
    Modifier.fillMaxSize().padding(32.dp),
    verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
    horizontalAlignment = Alignment.CenterHorizontally
  ) {
    Text(message, style = type.titleMedium)
    Meta("The Board does not have this attempt.")
    Action("Back", onBack)
  }
}

@Composable
fun AttemptScreen(
  fleet: String,
  ref: String,
  state: AttemptState,
  onBrief: () -> Unit,
  onBack: () -> Unit,
  onRetry: () -> Unit,
  onMarkRead: (String) -> Unit,
  onTarget: (ThreadTarget) -> Unit,
  onWeb: () -> Unit
) {
  val hand = LocalHand.current
  Column(Modifier.fillMaxSize().safeDrawingPadding()) {
    Row(
      Modifier.fillMaxWidth().height(56.dp).padding(end = 4.dp),
      verticalAlignment = Alignment.CenterVertically,
      horizontalArrangement = Arrangement.spacedBy(8.dp)
    ) {
      IconButton(onBack) { Icon(HandIcons.Back, "Back", Modifier.size(20.dp)) }
      Text(ref, color = hand.muted, style = type.titleMedium)
      Spacer(Modifier.weight(1f))
      Menu(listOf("Open web Board" to onWeb))
    }
    HorizontalDivider(color = hand.line)
    Box(Modifier.weight(1f)) {
      when {
        state.unpaired ->
          Column(Modifier.fillMaxSize().padding(32.dp), Arrangement.Center, Alignment.CenterHorizontally) {
            Text("No longer paired", color = hand.ink(Tone.Fail), style = type.titleMedium)
            Meta("Pair this phone again on the laptop's Board.")
          }
        else ->
          when (attemptBody(state)) {
            AttemptBody.Loading -> Loading()
            AttemptBody.Failed -> AttemptFailed(ref, onRetry)
            AttemptBody.Refused -> AttemptRefused(state.refused.orEmpty(), onBack)
            AttemptBody.Content -> AttemptContent(fleet, ref, state, onBrief, onMarkRead, onTarget)
          }
      }
    }
  }
}
