package io.github.atqamz.handcompanion

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.LinkAnnotation
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLinkStyles
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.withLink
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.delay

internal fun LazyListScope.markdown(
  blocks: List<MdBlock>,
  fleet: String,
  onTarget: (ThreadTarget) -> Unit
) {
  itemsIndexed(blocks, key = { index, _ -> "md:$index" }) { _, block ->
    Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp).padding(bottom = 10.dp)) {
      MarkdownBlock(block, fleet, onTarget)
    }
  }
}

@Composable
private fun MarkdownBlock(
  block: MdBlock,
  fleet: String,
  onTarget: (ThreadTarget) -> Unit
) {
  when (block) {
    is MdParagraph -> Paragraph(block.lines, fleet, onTarget)
    is MdHeading ->
      Text(
        annotated(block.text, fleet, onTarget),
        Modifier.fillMaxWidth().padding(0.dp, 12.dp, 0.dp, 4.dp),
        style = if (block.level <= 2) type.headlineSmall else type.titleMedium
      )
    is MdCodeBlock -> CodeBlock(block.text)
    MdRule -> HorizontalDivider(Modifier.padding(vertical = 8.dp), color = LocalHand.current.line)
    is MdQuote -> Quote(block.blocks, fleet, onTarget)
    is MdTable -> Table(block, fleet, onTarget)
    is MdList -> Bullets(block, fleet, onTarget)
  }
}

@Composable
private fun Paragraph(
  lines: List<List<MdInline>>,
  fleet: String,
  onTarget: (ThreadTarget) -> Unit
) {
  val hand = LocalHand.current
  Text(
    buildAnnotatedString {
      lines.forEachIndexed { index, line ->
        if (index > 0) append("\n")
        inlines(line, hand, fleet, onTarget)
      }
    },
    Modifier.fillMaxWidth(),
    style = type.bodyMedium
  )
}

@Composable
private fun Quote(
  blocks: List<MdBlock>,
  fleet: String,
  onTarget: (ThreadTarget) -> Unit
) {
  val hand = LocalHand.current
  Row(Modifier.fillMaxWidth().padding(vertical = 4.dp).height(IntrinsicSize.Min)) {
    Box(Modifier.width(2.dp).fillMaxHeight().background(hand.lineStrong))
    Column(Modifier.weight(1f).padding(start = 12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
      blocks.forEach { MarkdownBlock(it, fleet, onTarget) }
    }
  }
}

@Composable
private fun Bullets(
  list: MdList,
  fleet: String,
  onTarget: (ThreadTarget) -> Unit
) {
  Column(Modifier.fillMaxWidth().padding(vertical = 2.dp)) {
    list.items.forEachIndexed { index, item ->
      Row(Modifier.fillMaxWidth()) {
        Text(
          if (list.ordered) "${list.start + index}." else "•",
          Modifier.width(24.dp).padding(top = 2.dp),
          color = LocalHand.current.muted,
          style = type.bodyMedium
        )
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
          Text(annotated(item.lead, fleet, onTarget), style = type.bodyMedium)
          item.blocks.forEach { MarkdownBlock(it, fleet, onTarget) }
        }
      }
    }
  }
}

@Composable
private fun Table(table: MdTable, fleet: String, onTarget: (ThreadTarget) -> Unit) {
  val hand = LocalHand.current
  Box(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(vertical = 4.dp)) {
    Column {
      Row(Modifier.background(hand.panel)) {
        table.head.forEachIndexed { index, cell ->
          TableCell(cell, index, table.align, true, fleet, onTarget)
        }
      }
      HorizontalDivider(color = hand.lineStrong)
      table.rows.forEach { row ->
        Row {
          row.forEachIndexed { index, cell ->
            TableCell(cell, index, table.align, false, fleet, onTarget)
          }
        }
        HorizontalDivider(color = hand.line)
      }
    }
  }
}

@Composable
private fun TableCell(
  cell: List<MdInline>,
  index: Int,
  align: List<String>,
  head: Boolean,
  fleet: String,
  onTarget: (ThreadTarget) -> Unit
) {
  Text(
    annotated(cell, fleet, onTarget),
    Modifier.padding(8.dp, 6.dp).widthIn(min = 80.dp),
    textAlign =
      when (align.getOrNull(index)) {
        "r" -> TextAlign.End
        "c" -> TextAlign.Center
        else -> TextAlign.Start
      },
    style = if (head) type.bodySmall.copy(fontWeight = FontWeight.SemiBold) else type.bodySmall
  )
}

@Composable
private fun CodeBlock(text: String) {
  val hand = LocalHand.current
  val clipboard = LocalClipboardManager.current
  var copied by remember(text) { mutableStateOf(false) }
  LaunchedEffect(copied) {
    if (copied) {
      delay(1500)
      copied = false
    }
  }
  Box(
    Modifier.fillMaxWidth()
      .padding(vertical = 4.dp)
      .clip(MaterialTheme.shapes.medium)
      .background(hand.panel)
  ) {
    Box(Modifier.horizontalScroll(rememberScrollState()).padding(12.dp).padding(end = 40.dp)) {
      Text(shown(text), style = HandMono)
    }
    Box(
      Modifier.align(Alignment.TopEnd)
        .padding(4.dp)
        .size(32.dp)
        .clip(MaterialTheme.shapes.small)
        .clickable {
          clipboard.setText(AnnotatedString(text))
          copied = true
        }
        .semantics { liveRegion = LiveRegionMode.Polite },
      contentAlignment = Alignment.Center
    ) {
      Icon(
        if (copied) HandIcons.Copied else HandIcons.Copy,
        if (copied) "Copied" else "Copy code",
        Modifier.size(16.dp),
        if (copied) hand.ink(Tone.Pass) else hand.muted
      )
    }
  }
}

@Composable
private fun annotated(
  spans: List<MdInline>,
  fleet: String,
  onTarget: (ThreadTarget) -> Unit
): AnnotatedString {
  val hand = LocalHand.current
  return buildAnnotatedString { inlines(spans, hand, fleet, onTarget) }
}

private fun AnnotatedString.Builder.inlines(
  spans: List<MdInline>,
  hand: HandColors,
  fleet: String,
  onTarget: (ThreadTarget) -> Unit
) {
  for (span in spans) {
    when (span) {
      is MdText -> append(shown(span.text))
      is MdCode ->
        if (span.path == null) code(span.text, hand)
        else
          link(pathTarget(span.path, fleet), "path:${span.path}", pathStyles(hand), onTarget) {
            code(span.text, hand)
          }
      is MdStrong ->
        withStyle(SpanStyle(fontWeight = FontWeight.SemiBold)) {
          inlines(span.children, hand, fleet, onTarget)
        }
      is MdEm ->
        withStyle(SpanStyle(fontStyle = FontStyle.Italic)) {
          inlines(span.children, hand, fleet, onTarget)
        }
      is MdStrike ->
        withStyle(SpanStyle(color = hand.muted, textDecoration = TextDecoration.LineThrough)) {
          inlines(span.children, hand, fleet, onTarget)
        }
      is MdLink ->
        link(ThreadTarget.Browser(span.url), span.url, linkStyles(hand), onTarget) {
          withStyle(SpanStyle(color = hand.ink(Tone.Accent))) {
            inlines(span.children, hand, fleet, onTarget)
          }
        }
      is MdRef ->
        link(refTarget(span.ref), "ref:${span.ref}", refStyles(hand), onTarget) {
          withStyle(SpanStyle(fontFamily = HandMono.fontFamily, color = hand.ink(Tone.Accent))) {
            append(span.ref)
          }
        }
      is MdPath ->
        link(pathTarget(span.path, fleet), "path:${span.path}", pathStyles(hand), onTarget) {
          inlines(span.children, hand, fleet, onTarget)
        }
    }
  }
}

private fun AnnotatedString.Builder.code(text: String, hand: HandColors) {
  withStyle(
    SpanStyle(
      fontFamily = HandMono.fontFamily,
      background = hand.panel,
      color = hand.fg
    )
  ) {
    append(shown(text))
  }
}

private fun AnnotatedString.Builder.link(
  target: ThreadTarget,
  tag: String,
  styles: TextLinkStyles,
  onTarget: (ThreadTarget) -> Unit,
  block: AnnotatedString.Builder.() -> Unit
) {
  if (target == ThreadTarget.None) {
    block()
    return
  }
  withLink(
    LinkAnnotation.Clickable(
      tag,
      styles,
      linkInteractionListener = { onTarget(target) }
    )
  ) {
    block()
  }
}

private fun pathStyles(hand: HandColors) =
  TextLinkStyles(
    style = SpanStyle(color = hand.ink(Tone.Accent), textDecoration = TextDecoration.Underline)
  )

private fun refStyles(hand: HandColors) =
  TextLinkStyles(style = SpanStyle(color = hand.ink(Tone.Accent)))

private fun linkStyles(hand: HandColors) =
  TextLinkStyles(
    style = SpanStyle(color = hand.ink(Tone.Accent), textDecoration = TextDecoration.Underline)
  )
