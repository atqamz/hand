package io.github.atqamz.handcompanion

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

internal fun stateGlyph(state: String) =
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

private fun LazyListScope.taskItems(tasks: TasksState, onOpen: (String) -> Unit, onShowAll: () -> Unit) {
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

@Composable
private fun TaskList(tasks: TasksState, onOpen: (String) -> Unit, onShowAll: () -> Unit) {
  LazyColumn(Modifier.fillMaxSize()) { taskItems(tasks, onOpen, onShowAll) }
}

private val STATUS_CHOICES =
  listOf(
    "" to "Default",
    "finished" to "Done and abandoned",
    "all" to "Every status",
    "done" to "Done",
    "abandoned" to "Abandoned",
    "active" to "Active",
    "inbox" to "Inbox"
  )

internal fun statusLabel(value: String) = STATUS_CHOICES.firstOrNull { it.first == value }?.second ?: value

class TaskSearchUi(
  val state: TaskSearchState,
  val onQ: (String) -> Unit = {},
  val onStatus: (String) -> Unit = {},
  val onProject: (String) -> Unit = {},
  val onSince: (String) -> Unit = {},
  val onUntil: (String) -> Unit = {},
  val onSearch: () -> Unit = {},
  val onClear: () -> Unit = {},
  val onPage: (Int) -> Unit = {},
  val onRetry: () -> Unit = {}
)

internal fun searchMeta(row: TaskSearchRow, projects: Int): String =
  listOfNotNull(
    row.state.takeIf(String::isNotEmpty),
    row.parent.takeIf(String::isNotEmpty)?.let { "under $it" },
    row.waits?.let(::waitsLine),
    row.project.takeIf { projects > 1 && it.isNotEmpty() },
    row.openDecisions.takeIf { it > 0 }?.let { "$it open ${if (it == 1) "decision" else "decisions"}" }
  ).joinToString(" · ")

@Composable
private fun Choice(label: String, value: String, options: List<Pair<String, String>>, onPick: (String) -> Unit, modifier: Modifier) {
  var open by remember { mutableStateOf(false) }
  Box(modifier) {
    Column(Modifier.fillMaxWidth()) {
      Meta(label)
      Action(value, { open = true }, Modifier.fillMaxWidth().heightIn(min = 48.dp))
    }
    DropdownMenu(open, { open = false }) {
      for ((key, text) in options) {
        DropdownMenuItem(
          { Text(text) },
          {
            open = false
            onPick(key)
          }
        )
      }
    }
  }
}

@Composable
private fun DateField(label: String, value: String, onChange: (String) -> Unit, modifier: Modifier) {
  OutlinedTextField(
    value,
    onChange,
    modifier,
    label = { Text(label) },
    placeholder = { Text("YYYY-MM-DD") },
    singleLine = true,
    shape = MaterialTheme.shapes.medium
  )
}

@Composable
private fun TaskFilters(ui: TaskSearchUi) {
  val form = ui.state.form
  Column(Modifier.fillMaxWidth().padding(16.dp, 12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
    OutlinedTextField(
      form.q,
      ui.onQ,
      Modifier.fillMaxWidth(),
      label = { Text("Search tasks") },
      placeholder = { Text("Title, goal or ref") },
      singleLine = true,
      keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
      keyboardActions = KeyboardActions(onSearch = { ui.onSearch() }),
      shape = MaterialTheme.shapes.medium
    )
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
      Choice("Status", statusLabel(form.status), STATUS_CHOICES, ui.onStatus, Modifier.weight(1f))
      if (ui.state.projects.size > 1) {
        Choice(
          "Project",
          form.project.ifEmpty { "All projects" },
          listOf("" to "All projects") + ui.state.projects.map { it to it },
          ui.onProject,
          Modifier.weight(1f)
        )
      }
    }
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
      DateField("Since", form.since, ui.onSince, Modifier.weight(1f))
      DateField("Until", form.until, ui.onUntil, Modifier.weight(1f))
    }
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
      Action(if (ui.state.busy) "Searching…" else "Search", ui.onSearch, Modifier.height(48.dp), enabled = !ui.state.busy)
      Action(
        "Clear",
        ui.onClear,
        Modifier.height(48.dp),
        enabled = ui.state.applied != null || form != TaskSearchForm()
      )
    }
  }
}

@Composable
private fun SearchSummary(state: TaskSearchState) {
  val result = state.result ?: return
  Row(
    Modifier.fillMaxWidth().padding(16.dp, 4.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(12.dp)
  ) {
    Meta("${result.total} ${if (result.total == 1) "task" else "tasks"}")
    state.stale?.let {
      Text(
        "Stale · $it",
        Modifier.semantics { liveRegion = LiveRegionMode.Polite },
        color = LocalHand.current.ink(Tone.Wait),
        style = type.bodySmall
      )
    }
  }
}

@Composable
private fun SearchRow(row: TaskSearchRow, projects: Int, onOpen: (String) -> Unit) {
  val hand = LocalHand.current
  Row(
    Modifier.fillMaxWidth().heightIn(min = 48.dp).clickable { onOpen(row.ref) }.padding(16.dp, 6.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(12.dp)
  ) {
    Icon(stateGlyph(row.state), null, Modifier.size(20.dp), hand.ink(stateTone(row.state)))
    Text(row.ref, color = hand.muted, style = HandMono)
    Column(Modifier.weight(1f)) {
      Text(row.title, maxLines = 1, overflow = TextOverflow.Ellipsis, style = type.bodyLarge)
      val meta = searchMeta(row, projects)
      if (meta.isNotEmpty()) {
        Text(meta, color = hand.muted, style = type.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
      }
    }
  }
}

@Composable
private fun SearchPager(ui: TaskSearchUi) {
  val result = ui.state.result ?: return
  if (result.pages <= 1 && result.page <= 1) return
  Row(
    Modifier.fillMaxWidth().padding(16.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(12.dp)
  ) {
    Action("Previous", { ui.onPage(result.page - 1) }, enabled = result.page > 1 && !ui.state.busy)
    Text("Page ${result.page} of ${result.pages}", Modifier.weight(1f), color = LocalHand.current.muted, style = type.bodySmall)
    Action("Next", { ui.onPage(result.page + 1) }, enabled = result.page < result.pages && !ui.state.busy)
  }
}

@Composable
private fun SearchFailed(message: String, onRetry: (() -> Unit)?) {
  Column(
    Modifier.fillMaxWidth().padding(32.dp),
    verticalArrangement = Arrangement.spacedBy(12.dp),
    horizontalAlignment = Alignment.CenterHorizontally
  ) {
    Text("The tasks could not be searched", style = type.titleMedium)
    Meta(message)
    if (onRetry != null) Action("Retry", onRetry)
  }
}

@Composable
private fun SearchTasks(
  state: FleetState,
  ui: TaskSearchUi,
  onOpen: (String) -> Unit,
  onShowAll: () -> Unit,
  onRetry: () -> Unit
) {
  val search = ui.state
  LazyColumn(Modifier.fillMaxSize()) {
    item(key = "filters") { TaskFilters(ui) }
    when (searchBody(search)) {
      SearchBody.Legacy ->
        when (tasksBody(state.tasks)) {
          TasksBody.Loading -> item(key = "loading") { Loading() }
          TasksBody.Failed -> item(key = "failed") { TasksFailed(onRetry) }
          TasksBody.Empty -> item(key = "empty") { TasksEmpty() }
          TasksBody.Rows -> taskItems(state.tasks, onOpen, onShowAll)
        }
      SearchBody.Loading -> item(key = "searching") { Box(Modifier.padding(16.dp)) { Meta("Searching…") } }
      SearchBody.Failed -> item(key = "search-failed") { SearchFailed(search.error.orEmpty(), ui.onRetry.takeIf { search.retriable }) }
      SearchBody.Empty -> {
        item(key = "summary") { SearchSummary(search) }
        item(key = "none") { Box(Modifier.padding(16.dp)) { Meta("No tasks match.") } }
        item(key = "pager") { SearchPager(ui) }
      }
      SearchBody.Rows -> {
        item(key = "summary") { SearchSummary(search) }
        for (group in search.result?.groups.orEmpty()) {
          if (group.tasks.isEmpty()) continue
          item(key = "group:${group.name}") { SearchGroupHead(group) }
          items(group.tasks, key = { "found:${it.ref}" }) { SearchRow(it, search.projects.size, onOpen) }
        }
        item(key = "pager") { SearchPager(ui) }
      }
    }
  }
}

@Composable
private fun SearchGroupHead(group: TaskSearchGroup) {
  Row(
    Modifier.fillMaxWidth().padding(16.dp, 16.dp, 16.dp, 4.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(8.dp)
  ) {
    Text(group.name, Modifier.semantics { heading() }, color = LocalHand.current.muted, style = type.labelLarge)
    Chip("${group.count}", Tone.Neutral, 20)
  }
}

@Composable
fun TasksScreen(
  state: FleetState,
  onOpen: (String) -> Unit,
  onShowAll: () -> Unit,
  onRetry: () -> Unit,
  search: TaskSearchUi? = null
) {
  if (search?.state?.supported == true) return SearchTasks(state, search, onOpen, onShowAll, onRetry)
  val tasks = state.tasks
  when (tasksBody(tasks)) {
    TasksBody.Loading -> Loading()
    TasksBody.Failed -> TasksFailed(onRetry)
    TasksBody.Empty -> TasksEmpty()
    TasksBody.Rows -> TaskList(tasks, onOpen, onShowAll)
  }
}
