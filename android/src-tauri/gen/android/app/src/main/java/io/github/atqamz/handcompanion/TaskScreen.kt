package io.github.atqamz.handcompanion

import androidx.compose.foundation.background
import androidx.compose.foundation.border
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
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

@Composable
private fun SectionHead(text: String) {
  Text(
    text,
    Modifier.fillMaxWidth().padding(16.dp, 20.dp, 16.dp, 4.dp).semantics { heading() },
    color = LocalHand.current.muted,
    style = type.labelLarge
  )
}

@Composable
private fun RowChevron() {
  Icon(
    HandIcons.Chevron,
    null,
    Modifier.size(16.dp).rotate(-90f),
    LocalHand.current.muted
  )
}

@Composable
private fun Problem(text: String) {
  Text(
    text,
    Modifier.fillMaxWidth()
      .padding(12.dp)
      .semantics { liveRegion = LiveRegionMode.Polite },
    color = LocalHand.current.ink(Tone.Fail),
    style = type.bodyMedium
  )
}

@Composable
private fun TaskHead(detail: TaskDetail, onTarget: (ThreadTarget) -> Unit) {
  val hand = LocalHand.current
  val task = detail.task
  val meta =
    listOfNotNull(
        task.project.takeIf(String::isNotEmpty),
        detail.waits?.let(::waitsLine),
        localTime(task.updatedAt)?.let { "updated $it" }
      )
      .joinToString(" · ")
  Column(Modifier.fillMaxWidth().padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
    Text(task.title, style = type.headlineMedium)
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
      val pill = detail.pill.ifEmpty { task.status }
      if (pill.isNotEmpty()) Chip(pill, stateTone(pill), 24, glyph(stateTone(pill)))
      if (meta.isNotEmpty())
        Text(
          meta,
          Modifier.weight(1f),
          color = hand.muted,
          style = type.bodySmall,
          maxLines = 2,
          overflow = TextOverflow.Ellipsis
        )
    }
    if (task.parent.isNotEmpty()) {
      Row(verticalAlignment = Alignment.CenterVertically) {
        Meta("Sub-task of ")
        Text(
          task.parent,
          Modifier.clickable { onTarget(ThreadTarget.Task(task.parent)) },
          color = hand.ink(Tone.Accent),
          style = HandMono
        )
      }
    }
    if (task.goal.isNotEmpty()) Text(task.goal, style = type.bodyLarge)
  }
  HorizontalDivider(color = hand.line)
}

@Composable
private fun Waiting(decision: TaskDecision, onTarget: (ThreadTarget) -> Unit) {
  val hand = LocalHand.current
  Row(
    Modifier.fillMaxWidth()
      .clickable { onTarget(ThreadTarget.Decision(decision.ref)) }
      .heightIn(min = 52.dp)
      .padding(16.dp, 10.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(8.dp)
  ) {
    Chip("DECISION", Tone.Wait, 20)
    Text(decision.ref, color = hand.muted, style = HandMono)
    Text(
      decision.headline,
      Modifier.weight(1f),
      maxLines = 2,
      overflow = TextOverflow.Ellipsis,
      style = type.bodyLarge
    )
    RowChevron()
  }
  HorizontalDivider(color = hand.line)
}

@Composable
private fun Waiting(report: TaskReport, task: String, fleet: String, onTarget: (ThreadTarget) -> Unit) {
  val hand = LocalHand.current
  Row(
    Modifier.fillMaxWidth()
      .clickable { onTarget(reportTarget(report, task, fleet)) }
      .heightIn(min = 52.dp)
      .padding(16.dp, 10.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(8.dp)
  ) {
    Chip("REPORT", Tone.Neutral, 20)
    Text(report.ref, color = hand.muted, style = HandMono)
    Text(
      report.summary,
      Modifier.weight(1f),
      maxLines = 2,
      overflow = TextOverflow.Ellipsis,
      style = type.bodyLarge
    )
    RowChevron()
  }
  HorizontalDivider(color = hand.line)
}

@Composable
private fun Delivered(deliverable: TaskDeliverable, fleet: String, onTarget: (ThreadTarget) -> Unit) {
  val hand = LocalHand.current
  val target = deliverableTarget(deliverable, fleet)
  val badge = if (deliverable.kind == "file") fileBadge(deliverable.ref) else deliverable.kind.uppercase()
  Row(
    Modifier.fillMaxWidth()
      .clickable(enabled = target != ThreadTarget.None) { onTarget(target) }
      .heightIn(min = 44.dp)
      .padding(16.dp, 8.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(8.dp)
  ) {
    Chip(badge, Tone.Neutral, 20)
    Column(Modifier.weight(1f)) {
      Text(
        deliverable.ref,
        maxLines = 2,
        overflow = TextOverflow.Ellipsis,
        color = if (target is ThreadTarget.Browser) hand.ink(Tone.Accent) else hand.fg,
        style = type.bodyLarge.copy(fontWeight = if (deliverable.kind == "file") FontWeight.Normal else FontWeight.SemiBold)
      )
      if (deliverable.kind == "file") Meta("Opens in the web Board")
    }
  }
}

@Composable
private fun PlanCard(plan: TaskPlan) {
  Column(
    Modifier.fillMaxWidth()
      .padding(horizontal = 16.dp)
      .background(LocalHand.current.panel, MaterialTheme.shapes.large)
      .padding(12.dp)
  ) {
    Text(plan.body, style = type.bodyMedium)
  }
}

@Composable
private fun ReportCard(
  report: TaskReport,
  task: String,
  fleet: String,
  marking: Boolean,
  error: String?,
  onMarkRead: (String) -> Unit,
  onTarget: (ThreadTarget) -> Unit
) {
  val hand = LocalHand.current
  val shape = MaterialTheme.shapes.large
  Column(
    Modifier.fillMaxWidth()
      .padding(horizontal = 16.dp, vertical = 8.dp)
      .clip(shape)
      .border(1.dp, hand.line, shape)
  ) {
    Column(
      Modifier.fillMaxWidth()
        .background(hand.fill(reportFill(report.status)))
        .padding(12.dp),
      verticalArrangement = Arrangement.spacedBy(6.dp)
    ) {
      Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(
          report.ref,
          Modifier.clickable { onTarget(reportTarget(report, task, fleet)) },
          color = hand.muted,
          style = HandMono
        )
        Text(
          report.summary,
          Modifier.weight(1f),
          maxLines = 2,
          overflow = TextOverflow.Ellipsis,
          style = type.titleMedium
        )
        if (report.ackedAt.isEmpty()) {
          Action(if (marking) "Marking…" else "Mark read", { onMarkRead(report.ref) }, enabled = !marking)
        } else {
          Meta("read by ${report.ackedBy.ifEmpty { "the Board" }}")
        }
      }
      Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Meta("from ")
        Text(
          report.attempt,
          Modifier.clickable { onTarget(refTarget(report.attempt, task, fleet)) },
          color = hand.ink(Tone.Accent),
          style = HandMono
        )
        Chip(report.status, reportTone(report.status), 20)
        localTime(report.createdAt)?.let { Meta(it) }
      }
    }
    if (report.body.isNotEmpty()) Text(report.body, Modifier.padding(12.dp), style = type.bodyMedium)
    error?.let { Problem(it) }
  }
}

@Composable
private fun DecisionRow(decision: TaskDecision, onTarget: (ThreadTarget) -> Unit) {
  val hand = LocalHand.current
  Row(
    Modifier.fillMaxWidth()
      .clickable { onTarget(ThreadTarget.Decision(decision.ref)) }
      .heightIn(min = 48.dp)
      .padding(16.dp, 10.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(8.dp)
  ) {
    Text(decision.ref, color = hand.muted, style = HandMono)
    Column(Modifier.weight(1f)) {
      Text(decision.headline, style = type.bodyLarge)
      val answer = decision.answer.lines().firstOrNull().orEmpty()
      when {
        answer.isNotEmpty() -> Meta(answer)
        decision.status == "open" -> Label("Answer", hand.ink(Tone.Accent))
      }
    }
    RowChevron()
  }
  HorizontalDivider(color = hand.line)
}

@Composable
private fun AttemptRow(attempt: TaskAttempt, task: String, fleet: String, onTarget: (ThreadTarget) -> Unit) {
  val hand = LocalHand.current
  Row(
    Modifier.fillMaxWidth()
      .clickable { onTarget(refTarget(attempt.ref, task, fleet)) }
      .heightIn(min = 52.dp)
      .padding(16.dp, 8.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(12.dp)
  ) {
    Icon(
      stateGlyph(attempt.state),
      null,
      Modifier.size(20.dp),
      hand.ink(stateTone(attempt.state))
    )
    Text(attempt.ref, color = hand.muted, style = HandMono)
    Column(Modifier.weight(1f)) {
      Text(attemptTitle(attempt), maxLines = 1, overflow = TextOverflow.Ellipsis, style = type.bodyLarge)
      Meta(attemptMeta(attempt))
    }
    RowChevron()
  }
  HorizontalDivider(color = hand.line)
}

@Composable
private fun EventRow(event: TaskEvent) {
  val hand = LocalHand.current
  Row(
    Modifier.fillMaxWidth().padding(16.dp, 8.dp),
    horizontalArrangement = Arrangement.spacedBy(12.dp)
  ) {
    Text(
      localTime(event.at) ?: event.at,
      Modifier.width(48.dp),
      color = hand.muted,
      style = HandMono
    )
    Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
      Text(eventWords(event.kind), style = type.bodyMedium.copy(fontWeight = FontWeight.SemiBold))
      if (event.detail.isNotEmpty()) Text(event.detail, color = hand.muted, style = type.bodySmall)
    }
  }
  HorizontalDivider(color = hand.line)
}

@Composable
private fun ChildRow(child: TaskRow, onTarget: (ThreadTarget) -> Unit) {
  val hand = LocalHand.current
  Row(
    Modifier.fillMaxWidth()
      .clickable { onTarget(ThreadTarget.Task(child.ref)) }
      .heightIn(min = 48.dp)
      .padding(16.dp, 8.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(12.dp)
  ) {
    Icon(stateGlyph(child.state), null, Modifier.size(20.dp), hand.ink(stateTone(child.state)))
    Text(child.ref, color = hand.muted, style = HandMono)
    Text(child.title, Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis, style = type.bodyLarge)
    if (child.status.isNotEmpty()) Meta(child.status)
    RowChevron()
  }
  HorizontalDivider(color = hand.line)
}

@Composable
private fun TaskContent(
  fleet: String,
  ref: String,
  state: TaskState,
  onMarkRead: (String) -> Unit,
  onTarget: (ThreadTarget) -> Unit
) {
  val detail = state.detail ?: return
  val hand = LocalHand.current
  LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 24.dp)) {
    item(key = "head") { TaskHead(detail, onTarget) }
    items(detail.openDecisions, key = { "decision:${it.ref}" }) { Waiting(it, onTarget) }
    items(detail.unread, key = { "unread:${it.ref}" }) { Waiting(it, ref, fleet, onTarget) }
    if (detail.delivered.isNotEmpty()) {
      item(key = "delivered-head") { SectionHead("Delivered") }
      items(detail.delivered, key = { "delivered:${it.kind}:${it.ref}" }) { Delivered(it, fleet, onTarget) }
    }
    if (detail.children.isNotEmpty()) {
      item(key = "children-head") { SectionHead("Sub-tasks") }
      items(detail.children, key = { "child:${it.ref}" }) { ChildRow(it, onTarget) }
    }
    detail.plan?.let { plan ->
      item(key = "plan-head") { SectionHead("Plan") }
      item(key = "plan") { PlanCard(plan) }
    }
    if (detail.reports.isNotEmpty()) {
      item(key = "reports-head") { SectionHead("Reports") }
      items(detail.reports, key = { "report:${it.ref}" }) { report ->
        ReportCard(
          report,
          ref,
          fleet,
          report.ref in state.marking,
          state.errors[report.ref],
          onMarkRead,
          onTarget
        )
      }
      if (detail.moreReports) item(key = "reports-more") {
        Box(Modifier.padding(16.dp, 4.dp)) { Meta("Older reports are not shown.") }
      }
    }
    if (detail.decisions.isNotEmpty()) {
      item(key = "decisions-head") { SectionHead("Decisions") }
      items(detail.decisions, key = { "decision-row:${it.ref}" }) { DecisionRow(it, onTarget) }
    }
    if (detail.attempts.isNotEmpty()) {
      item(key = "attempts-head") { SectionHead("Attempts") }
      items(detail.attempts, key = { "attempt:${it.ref}" }) { AttemptRow(it, ref, fleet, onTarget) }
    }
    if (detail.events.isNotEmpty()) {
      item(key = "events-head") { SectionHead("Events") }
      items(detail.events, key = { "event:${it.seq}" }) { EventRow(it) }
      if (detail.moreEvents) item(key = "events-more") {
        Box(Modifier.padding(16.dp, 4.dp)) { Meta("Only the latest 50 events are shown.") }
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
}

@Composable
private fun TaskFailed(ref: String, onRetry: () -> Unit) {
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
private fun TaskRefused(message: String, onBack: () -> Unit) {
  Column(
    Modifier.fillMaxSize().padding(32.dp),
    verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
    horizontalAlignment = Alignment.CenterHorizontally
  ) {
    Text(message, style = type.titleMedium)
    Meta("The Board does not have this task.")
    Action("Back", onBack)
  }
}

@Composable
fun TaskScreen(
  fleet: String,
  ref: String,
  state: TaskState,
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
          when (taskBody(state)) {
            TaskBody.Loading -> Loading()
            TaskBody.Failed -> TaskFailed(ref, onRetry)
            TaskBody.Refused -> TaskRefused(state.refused.orEmpty(), onBack)
            TaskBody.Content ->
              TaskContent(fleet, ref, state, onMarkRead, onTarget)
          }
      }
    }
  }
}
