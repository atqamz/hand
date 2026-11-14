package io.github.atqamz.handcompanion

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
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

@Composable
private fun ReportHead(detail: ReportDetail, fleet: String, onTarget: (ThreadTarget) -> Unit) {
  val hand = LocalHand.current
  val report = detail.report
  val task = detail.task
  val tone = reportTone(report.status)
  Column(Modifier.fillMaxWidth().padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
    Text(annotated(markdownInline(shown(report.summary)), fleet, onTarget), style = type.headlineMedium)
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
      Chip(report.status.ifEmpty { "report" }, tone, 24, glyph(tone))
      Meta("from ")
      Text(
        report.attempt,
        Modifier.clickable { onTarget(refTarget(report.attempt)) },
        color = hand.ink(Tone.Accent),
        style = HandMono
      )
      localTime(report.createdAt)?.let { Meta("· $it") }
    }
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
      Meta("for ")
      Text(
        task.ref,
        Modifier.clickable { onTarget(ThreadTarget.Task(task.ref)) },
        color = hand.ink(Tone.Accent),
        style = HandMono
      )
      Text(
        task.title,
        Modifier.weight(1f),
        maxLines = 2,
        overflow = TextOverflow.Ellipsis,
        style = type.bodyMedium
      )
    }
  }
  HorizontalDivider(color = hand.line)
}

@Composable
private fun ReportDock(report: TaskReport, marking: Boolean, error: String?, onMarkRead: () -> Unit) {
  val hand = LocalHand.current
  val read = reportRead(report)
  Column(
    Modifier.fillMaxWidth()
      .background(hand.bg)
      .padding(16.dp, 10.dp),
    verticalArrangement = Arrangement.spacedBy(8.dp)
  ) {
    HorizontalDivider(color = hand.line)
    error?.let {
      Text(
        it,
        Modifier.fillMaxWidth().semantics { liveRegion = LiveRegionMode.Polite },
        color = hand.ink(Tone.Fail),
        style = type.bodyMedium
      )
    }
    if (read != null) {
      Row(
        Modifier.fillMaxWidth().heightIn(min = 40.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp)
      ) {
        Icon(HandIcons.Passing, null, Modifier.size(16.dp), hand.ink(Tone.Pass))
        Meta(read)
      }
    } else {
      Button(
        onMarkRead,
        Modifier.fillMaxWidth().height(48.dp),
        enabled = !marking,
        shape = MaterialTheme.shapes.medium
      ) {
        Text(if (marking) "Marking…" else "Mark read", style = type.titleMedium)
      }
    }
  }
}

@Composable
private fun ReportContent(
  fleet: String,
  state: ReportState,
  onMarkRead: () -> Unit,
  onTarget: (ThreadTarget) -> Unit
) {
  val detail = state.detail ?: return
  val hand = LocalHand.current
  val report = detail.report
  val blocks = remember(report.body) { markdown(reportRest(report.body)) }
  Column(Modifier.fillMaxSize()) {
    LazyColumn(Modifier.weight(1f), contentPadding = PaddingValues(bottom = 24.dp)) {
      item(key = "head") { ReportHead(detail, fleet, onTarget) }
      markdown(blocks, fleet, onTarget)
      if (detail.delivered.isNotEmpty()) {
        item(key = "delivered-head") { SectionHead("Delivered") }
        items(detail.delivered, key = { "delivered:${it.kind}:${it.ref}" }) {
          Delivered(it, fleet, onTarget)
        }
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
    ReportDock(report, state.marking, state.error, onMarkRead)
  }
}

@Composable
private fun ReportFailed(ref: String, onRetry: () -> Unit) {
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
private fun ReportRefused(message: String, onBack: () -> Unit) {
  Column(
    Modifier.fillMaxSize().padding(32.dp),
    verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
    horizontalAlignment = Alignment.CenterHorizontally
  ) {
    Text(message, style = type.titleMedium)
    Meta("The Board does not have this report.")
    Action("Back", onBack)
  }
}

@Composable
fun ReportScreen(
  fleet: String,
  ref: String,
  state: ReportState,
  onBack: () -> Unit,
  onRetry: () -> Unit,
  onMarkRead: () -> Unit,
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
          when (reportBody(state)) {
            ReportBody.Loading -> Loading()
            ReportBody.Failed -> ReportFailed(ref, onRetry)
            ReportBody.Refused -> ReportRefused(state.refused.orEmpty(), onBack)
            ReportBody.Content -> ReportContent(fleet, state, onMarkRead, onTarget)
          }
      }
    }
  }
}
