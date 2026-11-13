package io.github.atqamz.handcompanion

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

private fun stateGlyph(state: String) =
  when (state) {
    "failing" -> HandIcons.Failure
    "waiting" -> HandIcons.Waiting
    "running" -> HandIcons.Running
    "passing" -> HandIcons.Passing
    else -> HandIcons.Idle
  }

fun taskMeta(line: TaskLine): String =
  listOfNotNull(
    line.task.state.takeIf(String::isNotEmpty),
    line.under?.let { "under $it" },
    line.task.waits?.let(::waitsLine)
  ).joinToString(" · ")

@Composable
private fun GroupHead(group: TaskGroup) {
  Row(
    Modifier.fillMaxWidth().padding(16.dp, 16.dp, 16.dp, 4.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(8.dp)
  ) {
    Text(
      group.name,
      Modifier.semantics { heading() },
      color = LocalHand.current.muted,
      style = type.labelLarge
    )
    Chip("${group.count}", Tone.Neutral, 20)
  }
}

@Composable
private fun TaskLineRow(line: TaskLine, onOpen: (String) -> Unit) {
  val hand = LocalHand.current
  Row(
    Modifier.fillMaxWidth()
      .heightIn(min = 48.dp)
      .clickable { onOpen(line.task.ref) }
      .padding(start = (16 + line.depth * 20).dp, end = 16.dp, top = 6.dp, bottom = 6.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(12.dp)
  ) {
    Icon(
      stateGlyph(line.task.state),
      null,
      Modifier.size(20.dp),
      hand.ink(stateTone(line.task.state))
    )
    Text(line.task.ref, color = hand.muted, style = HandMono)
    Column(Modifier.weight(1f)) {
      Text(line.task.title, maxLines = 1, overflow = TextOverflow.Ellipsis, style = type.bodyLarge)
      val meta = taskMeta(line)
      if (meta.isNotEmpty()) {
        Text(
          meta,
          color = hand.muted,
          style = type.bodySmall,
          maxLines = 1,
          overflow = TextOverflow.Ellipsis
        )
      }
    }
  }
}

@Composable
private fun TasksFailed(onRetry: () -> Unit) {
  Column(
    Modifier.fillMaxSize().padding(32.dp),
    verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
    horizontalAlignment = Alignment.CenterHorizontally
  ) {
    Text("Couldn't load tasks.", style = type.titleMedium)
    Meta("The Board's answer was not read.")
    Action("Retry", onRetry)
  }
}

@Composable
private fun TasksEmpty() {
  Box(Modifier.fillMaxSize().padding(32.dp), contentAlignment = Alignment.Center) {
    Meta("No tasks yet.")
  }
}

@Composable
private fun TaskList(tasks: TasksState, onOpen: (String) -> Unit, onShowAll: () -> Unit) {
  LazyColumn(Modifier.fillMaxSize()) {
    for (group in tasks.groups) {
      item(key = "group:${group.name}") { GroupHead(group) }
      items(lines(group), key = { "task:${it.task.ref}" }) { TaskLineRow(it, onOpen) }
    }
    tasksHidden(tasks)?.let { label ->
      item(key = "tasks-hidden") { Box(Modifier.padding(16.dp, 16.dp, 16.dp, 4.dp)) { Meta(label) } }
    }
    tasksFooter(tasks)?.let { label ->
      item(key = "tasks-footer") { Action(label, onShowAll, Modifier.padding(16.dp)) }
    }
  }
}

@Composable
fun TasksScreen(state: FleetState, onOpen: (String) -> Unit, onShowAll: () -> Unit, onRetry: () -> Unit) {
  val tasks = state.tasks
  when (tasksBody(tasks)) {
    TasksBody.Loading -> Loading()
    TasksBody.Failed -> TasksFailed(onRetry)
    TasksBody.Empty -> TasksEmpty()
    TasksBody.Rows -> TaskList(tasks, onOpen, onShowAll)
  }
}
