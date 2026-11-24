package io.github.atqamz.handcompanion

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
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
            .clickable(enabled = open) { onTarget(ThreadTarget.Doc(entry.path)) }
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
private fun DocText(state: DocState, onMore: () -> Unit) {
  val hand = LocalHand.current
  LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(vertical = 8.dp)) {
    if (state.lines.isEmpty()) {
      item(key = "empty") {
        Text("This file is empty.", Modifier.padding(16.dp), color = hand.muted, style = type.bodyMedium)
      }
    }
    itemsIndexed(state.lines, key = { index, _ -> "line:$index" }) { index, line ->
      Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp)) {
        Text(
          "${index + 1}",
          Modifier.width(44.dp).padding(end = 8.dp),
          color = hand.muted,
          textAlign = TextAlign.End,
          style = HandMono
        )
        Text(line, Modifier.weight(1f), style = HandMono)
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
  onTarget: (ThreadTarget) -> Unit
) {
  val hand = LocalHand.current
  val node = state.node
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
    if (node != null && state.problem != null) DocStale("Showing the last read. ${state.problem}", onRetry)
    Column(Modifier.weight(1f)) {
      when (docBody(state)) {
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
        DocBody.Text -> DocText(state, onMore)
        DocBody.Notice ->
          DocMessage(
            "This file can't be shown here.",
            listOfNotNull(node?.kind, node?.size?.let(::docSize)).joinToString(" · ")
          ) {}
      }
    }
  }
}
