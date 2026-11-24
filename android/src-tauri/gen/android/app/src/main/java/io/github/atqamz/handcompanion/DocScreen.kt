package io.github.atqamz.handcompanion

import androidx.compose.foundation.Image
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.withStyle
import androidx.compose.foundation.background
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

@Composable
private fun DocMessage(title: String, detail: String, tone: Tone? = null, actions: @Composable () -> Unit) {
  val hand = LocalHand.current
  Column(
    Modifier.fillMaxSize().padding(32.dp),
    verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
    horizontalAlignment = Alignment.CenterHorizontally
  ) {
    Text(
      title,
      color = tone?.let(hand::ink) ?: hand.fg,
      textAlign = TextAlign.Center,
      style = type.titleMedium
    )
    Meta(detail)
    actions()
  }
}

@Composable
private fun DocStale(text: String, onRetry: () -> Unit) {
  val hand = LocalHand.current
  Row(
    Modifier.fillMaxWidth()
      .padding(16.dp, 8.dp)
      .semantics { liveRegion = LiveRegionMode.Polite },
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(12.dp)
  ) {
    Text(text, Modifier.weight(1f), color = hand.ink(Tone.Wait), style = type.bodySmall)
    Action("Retry", onRetry)
  }
}

@Composable
private fun DocFolder(node: DocNode, onPage: (Int) -> Unit, onTarget: (ThreadTarget) -> Unit) {
  val hand = LocalHand.current
  LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 24.dp)) {
    if (node.entries.isEmpty()) {
      item(key = "empty") {
        Text("This folder is empty.", Modifier.padding(16.dp), color = hand.muted, style = type.bodyMedium)
      }
    }
    itemsIndexed(node.entries, key = { _, entry -> "entry:${entry.path}" }) { _, entry ->
      val open = entry.refused == null
      Column {
        Row(
          Modifier.fillMaxWidth()
            .clickable(enabled = open) { onTarget(docTarget(entry.path)) }
            .heightIn(min = 52.dp)
            .padding(16.dp, 8.dp),
          verticalAlignment = Alignment.CenterVertically,
          horizontalArrangement = Arrangement.spacedBy(8.dp)
        ) {
          Chip(entry.label.ifEmpty { "FILE" }, Tone.Neutral, 20)
          Column(Modifier.weight(1f)) {
            Text(
              entry.name,
              maxLines = 2,
              overflow = TextOverflow.Ellipsis,
              color = if (open) hand.fg else hand.muted,
              style = type.bodyLarge
            )
            entry.refused?.let { Meta(it) }
              ?: entry.size?.let { Meta(docSize(it)) }
          }
          if (open) RowChevron()
        }
        HorizontalDivider(color = hand.line)
      }
    }
    if (node.pages > 1) {
      item(key = "pager") {
        Row(
          Modifier.fillMaxWidth().padding(16.dp),
          verticalAlignment = Alignment.CenterVertically,
          horizontalArrangement = Arrangement.spacedBy(12.dp)
        ) {
          Action("Previous", { onPage(node.page - 1) }, enabled = node.page > 1)
          Meta("Page ${node.page} of ${node.pages}")
          Action("Next", { onPage(node.page + 1) }, enabled = node.page < node.pages)
        }
      }
    }
  }
}

@Composable
private fun highlighted(line: String, query: String, current: Boolean): AnnotatedString {
  val hand = LocalHand.current
  val matches = docMatches(line, query)
  if (matches.isEmpty()) return AnnotatedString(line)
  val tone = hand.ink(Tone.Wait).copy(alpha = if (current) 0.75f else 0.35f)
  return buildAnnotatedString {
    var at = 0
    for (range in matches) {
      append(line.substring(at, range.first))
      withStyle(SpanStyle(background = tone)) { append(line.substring(range.first, range.last + 1)) }
      at = range.last + 1
    }
    append(line.substring(at))
  }
}

@Composable
private fun DocText(state: DocState, onMore: () -> Unit, onEarlier: () -> Unit) {
  val hand = LocalHand.current
  val list = rememberLazyListState()
  val across = rememberScrollState()
  val lead = if (state.first > 1) 1 else 0
  LaunchedEffect(state.jump) {
    state.jump?.let { list.scrollToItem((it.line - state.first + lead).coerceAtLeast(0)) }
  }
  val query = state.find?.query.orEmpty()
  val current = state.find?.hits?.getOrNull(state.find.at)
  LazyColumn(Modifier.fillMaxSize(), state = list, contentPadding = PaddingValues(vertical = 8.dp)) {
    if (state.first > 1) {
      item(key = "earlier") {
        Column(Modifier.fillMaxWidth().padding(16.dp)) {
          Action(
            if (state.paging) "Loading…" else "Show earlier lines",
            onEarlier,
            Modifier.fillMaxWidth(),
            enabled = !state.paging
          )
        }
      }
    }
    if (state.lines.isEmpty()) {
      item(key = "empty") {
        Text("This file is empty.", Modifier.padding(16.dp), color = hand.muted, style = type.bodyMedium)
      }
    }
    itemsIndexed(state.lines, key = { index, _ -> "line:${state.first + index}" }) { index, line ->
      val number = state.first + index
      Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp)) {
        Text(
          "$number",
          Modifier.width(44.dp).padding(end = 8.dp),
          color = hand.muted,
          textAlign = TextAlign.End,
          style = HandMono
        )
        Text(
          highlighted(line, query, number == current),
          if (state.wrap) Modifier.weight(1f) else Modifier.weight(1f).horizontalScroll(across),
          softWrap = state.wrap,
          style = HandMono
        )
      }
    }
    if (state.next != null) {
      item(key = "more") {
        Column(
          Modifier.fillMaxWidth().padding(16.dp),
          verticalArrangement = Arrangement.spacedBy(8.dp)
        ) {
          state.moreProblem?.let { Meta(it, hand.ink(Tone.Wait)) }
          Action(
            if (state.paging) "Loading…" else if (state.moreProblem != null) "Retry" else "Show more",
            onMore,
            Modifier.fillMaxWidth(),
            enabled = !state.paging
          )
        }
      }
    }
  }
}

@Composable
private fun DocImage(state: DocState) {
  val bytes = state.image ?: return
  val bitmap = remember(bytes) { decodeBounded(bytes) }
  if (bitmap == null) {
    DocMessage("This image can't be shown here.", "It is too large to decode on this device.") {}
  } else {
    Image(
      bitmap.asImageBitmap(),
      state.node?.name,
      Modifier.fillMaxSize().padding(16.dp),
      contentScale = ContentScale.Fit
    )
  }
}

enum class DocTool {
  Find,
  GoTo
}

class DocTools(
  val find: (String) -> Unit = {},
  val step: (Boolean) -> Unit = {},
  val closeFind: () -> Unit = {},
  val goTo: (Int) -> Unit = {},
  val earlier: () -> Unit = {},
  val wrap: () -> Unit = {},
  val copy: suspend () -> String? = { null }
)

@Composable
private fun DocToolbar(
  state: DocState,
  body: DocBody,
  tool: DocTool?,
  onTool: (DocTool?) -> Unit,
  tools: DocTools
) {
  val hand = LocalHand.current
  val clipboard = LocalClipboardManager.current
  val scope = rememberCoroutineScope()
  var copied by remember { mutableStateOf(false) }
  LaunchedEffect(copied) {
    if (copied) {
      delay(1500)
      copied = false
    }
  }
  val text = body == DocBody.Text
  if (!text && body != DocBody.Markdown) return
  Row(
    Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(16.dp, 8.dp),
    horizontalArrangement = Arrangement.spacedBy(8.dp)
  ) {
    if (text) {
      Action("Find", { onTool(if (tool == DocTool.Find) null else DocTool.Find) })
      Action("Go to line", { onTool(if (tool == DocTool.GoTo) null else DocTool.GoTo) })
      Action(if (state.wrap) "Wrap: on" else "Wrap: off", tools.wrap)
    }
    Action(
      if (copied) "Copied" else "Copy contents",
      {
        scope.launch {
          tools.copy()?.let {
            clipboard.setText(AnnotatedString(it))
            copied = true
          }
        }
      }
    )
  }
  state.copyProblem?.let {
    Box(Modifier.padding(horizontal = 16.dp)) { Meta(it, hand.ink(Tone.Wait)) }
  }
}

@Composable
private fun DocPanel(state: DocState, tool: DocTool, tools: DocTools, onClose: () -> Unit) {
  val hand = LocalHand.current
  var query by rememberSaveable { mutableStateOf(state.find?.query.orEmpty()) }
  var line by rememberSaveable { mutableStateOf(state.goLine?.toString().orEmpty()) }
  Column(Modifier.fillMaxWidth().padding(16.dp, 4.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
    if (tool == DocTool.Find) {
      OutlinedTextField(
        query,
        { query = it },
        Modifier.fillMaxWidth(),
        placeholder = { Text("Find in this file", color = hand.muted) },
        singleLine = true,
        shape = MaterialTheme.shapes.large,
        keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
        keyboardActions = KeyboardActions(onSearch = { tools.find(query) })
      )
      val find = state.find
      Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Action("Find", { tools.find(query) }, enabled = query.isNotEmpty() && !state.paging)
        Action("Previous", { tools.step(false) }, enabled = find != null && find.hits.isNotEmpty())
        Action("Next", { tools.step(true) }, enabled = find != null && (find.hits.isNotEmpty() || state.next != null) && !state.paging)
        Action("Close", { tools.closeFind(); onClose() })
      }
      Meta(
        when {
          state.paging -> "Searching…"
          find == null || find.query.isEmpty() -> ""
          find.problem != null -> find.problem
          find.hits.isEmpty() && state.next != null -> "No match in the ${state.lines.size} lines read. Next keeps looking."
          find.hits.isEmpty() -> "No match."
          else -> "${find.at + 1} of ${find.hits.size}${if (state.next != null) "+" else ""}"
        }
      )
    } else {
      OutlinedTextField(
        line,
        { line = it.filter(Char::isDigit).take(9) },
        Modifier.fillMaxWidth(),
        placeholder = { Text("Line number", color = hand.muted) },
        singleLine = true,
        shape = MaterialTheme.shapes.large,
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number, imeAction = ImeAction.Go),
        keyboardActions = KeyboardActions(onGo = { line.toIntOrNull()?.let(tools.goTo) })
      )
      Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Action("Go", { line.toIntOrNull()?.let(tools.goTo) }, enabled = line.toIntOrNull() != null && !state.paging)
        Action("Close", onClose)
      }
      Meta(if (state.paging) "Reading…" else state.goProblem.orEmpty(), hand.ink(Tone.Wait))
    }
  }
}

@Composable
private fun DocMarkdown(state: DocState, fleet: String, onTarget: (ThreadTarget) -> Unit) {
  LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(vertical = 16.dp)) {
    markdown(state.blocks, fleet, onTarget)
  }
}

@Composable
fun DocScreen(
  fleet: String,
  path: String,
  state: DocState,
  onBack: () -> Unit,
  onRetry: () -> Unit,
  onMore: () -> Unit,
  onPage: (Int) -> Unit,
  onTarget: (ThreadTarget) -> Unit,
  tools: DocTools = DocTools(),
  seed: DocTool? = null
) {
  val hand = LocalHand.current
  val node = state.node
  val body = docBody(state)
  var tool by rememberSaveable { mutableStateOf(seed) }
  Column(Modifier.fillMaxSize().safeDrawingPadding()) {
    Row(
      Modifier.fillMaxWidth().height(56.dp).padding(end = 16.dp),
      verticalAlignment = Alignment.CenterVertically,
      horizontalArrangement = Arrangement.spacedBy(8.dp)
    ) {
      IconButton(onBack) { Icon(HandIcons.Back, "Back", Modifier.size(20.dp)) }
      Column(Modifier.weight(1f)) {
        Text(
          node?.name ?: path.substringAfterLast('/'),
          maxLines = 1,
          overflow = TextOverflow.Ellipsis,
          style = type.titleMedium
        )
        Text(
          path,
          maxLines = 1,
          overflow = TextOverflow.Ellipsis,
          color = hand.muted,
          style = HandMono
        )
      }
    }
    HorizontalDivider(color = hand.line)
    DocToolbar(state, body, tool, { tool = it }, tools)
    if (tool != null && body == DocBody.Text) DocPanel(state, tool!!, tools) { tool = null }
    if (node != null && state.problem != null && body != DocBody.Failed) DocStale("Showing the last read. ${state.problem}", onRetry)
    Column(Modifier.weight(1f)) {
      when (body) {
        DocBody.Loading -> Loading()
        DocBody.Unpaired ->
          DocMessage("No longer paired", "Pair this Companion again on the host's Board.", Tone.Fail) {}
        DocBody.TooOld ->
          DocMessage("This host cannot show documents", "Update Hand on the host, then try again.") {
            Action("Retry", onRetry)
          }
        DocBody.Refused ->
          DocMessage(state.refused.orEmpty(), "The Board does not show this document.") {
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
              Action("Retry", onRetry)
              Action("Back", onBack)
            }
          }
        DocBody.Failed ->
          DocMessage("Couldn't read ${path.substringAfterLast('/')}.", state.problem.orEmpty()) {
            Action("Retry", onRetry)
          }
        DocBody.Folder -> node?.let { DocFolder(it, onPage, onTarget) }
        DocBody.Markdown -> DocMarkdown(state, fleet, onTarget)
        DocBody.Text -> DocText(state, onMore, tools.earlier)
        DocBody.Image -> DocImage(state)
        DocBody.ImageTooLarge ->
          DocMessage(
            "This image is too large to show here.",
            listOfNotNull(node?.kind, node?.size?.let(::docSize)).joinToString(" · ")
          ) {}
        DocBody.Notice ->
          DocMessage(
            "This file can't be shown here.",
            listOfNotNull(node?.kind, node?.size?.let(::docSize)).joinToString(" · ")
          ) {}
      }
    }
  }
}
