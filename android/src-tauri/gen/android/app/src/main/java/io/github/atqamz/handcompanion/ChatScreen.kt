package io.github.atqamz.handcompanion

import android.content.res.Configuration
import android.provider.Settings
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
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
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Badge
import androidx.compose.material3.BadgedBox
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import java.util.TimeZone
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch

private val SUPERVISOR_WIDTH = 540.dp
private val OPERATOR_WIDTH = 560.dp

@Composable
private fun Ring(modifier: Modifier = Modifier) {
  Box(modifier.size(8.dp).border(1.dp, LocalHand.current.lineStrong, CircleShape))
}

@Composable
private fun reducedMotion(): Boolean {
  val context = LocalContext.current
  return remember(context) {
    try {
      Settings.Global.getFloat(context.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, 1f) == 0f
    } catch (_: Exception) {
      false
    }
  }
}

@Composable
private fun Pulse() {
  val hand = LocalHand.current
  val alpha =
    if (reducedMotion()) 1f
    else {
      val transition = rememberInfiniteTransition(label = "live")
      val value by
        transition.animateFloat(
          initialValue = 1f,
          targetValue = 0.35f,
          animationSpec = infiniteRepeatable(tween(1_000), RepeatMode.Reverse),
          label = "pulse"
        )
      value
    }
  Box(
    Modifier.size(8.dp)
      .clip(CircleShape)
      .background(hand.ink(Tone.Accent).copy(alpha = alpha))
      .semantics { contentDescription = "Live" }
  )
}

@Composable
private fun MessageHead(entry: TimelineEntry, mine: Boolean, zone: TimeZone) {
  val hand = LocalHand.current
  val delivery =
    when {
      entry.queued || entry.delivery == "queued" -> "queued"
      entry.delivery.isNotEmpty() && entry.delivered.isNotEmpty() ->
        "${entry.delivery} ${chatClock(entry.delivered, zone)}"
      entry.delivery.isNotEmpty() -> entry.delivery
      else -> ""
    }
  Row(
    Modifier.fillMaxWidth().padding(bottom = 4.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(8.dp)
  ) {
    Text(
      if (mine) "you" else entry.ref.ifEmpty { "supervisor" },
      color = if (mine) hand.ink(Tone.Accent) else hand.muted,
      fontWeight = FontWeight.SemiBold,
      style = type.bodySmall
    )
    Text(chatClock(entry.at, zone), color = hand.muted, style = type.bodySmall)
    if (!mine && entry.model.isNotEmpty()) {
      Text(entry.model, color = hand.muted, style = type.bodySmall)
    }
    if (mine) {
      Spacer(Modifier.weight(1f))
      if (delivery.isNotEmpty()) Text(delivery, color = hand.muted, style = type.bodySmall)
    }
  }
}

@Composable
private fun SupervisorBubble(
  entry: ChatEntry,
  fleet: String,
  zone: TimeZone,
  onTarget: (ThreadTarget) -> Unit
) {
  val hand = LocalHand.current
  val blocks = remember(entry.entry.text) { markdown(entry.entry.text) }
  Column(
    Modifier.widthIn(max = SUPERVISOR_WIDTH)
      .background(hand.card, MaterialTheme.shapes.large)
      .border(1.dp, hand.lineStrong, MaterialTheme.shapes.large)
      .padding(13.dp, 9.dp, 13.dp, 11.dp)
  ) {
    MessageHead(entry.entry, false, zone)
    if (entry.entry.text.isNotEmpty()) MarkdownBody(blocks, fleet, onTarget)
  }
}

@Composable
private fun OperatorBubble(entry: ChatEntry, zone: TimeZone) {
  val hand = LocalHand.current
  Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
    Box(Modifier.fillMaxWidth(0.88f), contentAlignment = Alignment.CenterEnd) {
      Column(
        Modifier.widthIn(max = OPERATOR_WIDTH)
          .background(
            hand.fill(Tone.Accent),
            RoundedCornerShape(10.dp, 10.dp, 4.dp, 10.dp)
          )
          .padding(14.dp, 10.dp, 14.dp, 12.dp)
      ) {
        MessageHead(entry.entry, true, zone)
        if (entry.entry.text.isNotEmpty()) {
          Text(shown(entry.entry.text), style = type.bodyMedium)
        }
      }
    }
  }
}

@Composable
private fun HandLine(entry: TimelineEntry, zone: TimeZone, indent: Boolean = false) {
  val hand = LocalHand.current
  Row(
    Modifier.fillMaxWidth().padding(start = if (indent) 16.dp else 0.dp),
    verticalAlignment = Alignment.Top
  ) {
    Ring(Modifier.padding(top = 5.dp))
    Spacer(Modifier.size(8.dp))
    Text(
      handWords(entry.text),
      Modifier.weight(1f, fill = false),
      maxLines = 1,
      overflow = TextOverflow.Ellipsis,
      color = hand.muted,
      style = type.bodySmall
    )
    Spacer(Modifier.size(8.dp))
    Text(chatClock(entry.at, zone), color = hand.muted, style = type.bodySmall)
  }
}

@Composable
private fun WakesLine(
  entry: ChatEntry,
  zone: TimeZone,
  open: Boolean,
  onToggle: () -> Unit
) {
  val hand = LocalHand.current
  val wakes = entry.entry.wakes
  Column {
    Row(
      Modifier.fillMaxWidth().clickable(onClick = onToggle),
      verticalAlignment = Alignment.CenterVertically
    ) {
      Ring()
      Spacer(Modifier.size(8.dp))
      Text("${wakes.size} wakes", color = hand.muted, style = type.bodySmall)
      Spacer(Modifier.size(8.dp))
      Text(
        wakesSpan(wakes, zone),
        color = hand.muted,
        style = type.bodySmall
      )
      Spacer(Modifier.size(4.dp))
      Icon(
        HandIcons.Chevron,
        if (open) "Collapse wakes" else "Expand wakes",
        Modifier.size(14.dp).rotate(if (open) 180f else 0f),
        hand.muted
      )
    }
    if (open) {
      Column(Modifier.padding(top = 6.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        wakes.forEach { HandLine(it, zone, indent = true) }
      }
    }
  }
}

@Composable
private fun DayLine(label: String) {
  val hand = LocalHand.current
  Row(
    Modifier.fillMaxWidth().padding(vertical = 2.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(12.dp)
  ) {
    HorizontalDivider(Modifier.weight(1f), color = hand.line)
    Text(
      label,
      Modifier.background(hand.bg, CircleShape)
        .border(1.dp, hand.line, CircleShape)
        .padding(8.dp, 1.dp),
      color = hand.muted,
      style = type.bodySmall
    )
    HorizontalDivider(Modifier.weight(1f), color = hand.line)
  }
}

@Composable
private fun WorkingLine(working: TimelineWorking, live: Boolean, now: Long, zone: TimeZone) {
  val hand = LocalHand.current
  val words = workingWords(working, now, zone)
  val head = "${working.ref} working"
  Row(
    Modifier.fillMaxWidth(),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(8.dp)
  ) {
    if (live) Pulse()
    Text(
      buildAnnotatedString {
        withStyle(SpanStyle(color = hand.fg, fontWeight = FontWeight.SemiBold)) { append(head) }
        append(words.removePrefix(head))
      },
      color = hand.muted,
      style = type.bodySmall,
      maxLines = 1,
      overflow = TextOverflow.Ellipsis
    )
  }
}

@Composable
private fun ChatRowView(
  row: ChatRow,
  fleet: String,
  zone: TimeZone,
  open: Set<String>,
  onToggle: (String) -> Unit,
  onTarget: (ThreadTarget) -> Unit
) {
  when (row) {
    is ChatRow.Day -> DayLine(row.label)
    is ChatRow.Hand -> HandLine(row.entry.entry, zone)
    is ChatRow.Wakes ->
      WakesLine(row.entry, zone, row.key in open) { onToggle(row.key) }
    is ChatRow.Message ->
      if (row.mine) OperatorBubble(row.entry, zone)
      else SupervisorBubble(row.entry, fleet, zone, onTarget)
  }
}

@Composable
private fun LatestButton(count: Int, onLatest: () -> Unit) {
  val hand = LocalHand.current
  val coarse =
    LocalConfiguration.current.touchscreen != Configuration.TOUCHSCREEN_NOTOUCH
  val control = if (coarse) 44.dp else 32.dp
  val label =
    if (count > 0) {
      "Scroll to latest, $count new message${if (count == 1) "" else "s"}"
    } else {
      "Scroll to latest message"
    }
  val button: @Composable () -> Unit = {
    Box(
      Modifier.size(control)
        .clip(CircleShape)
        .background(hand.bg)
        .border(1.dp, hand.lineStrong, CircleShape)
        .clickable(onClick = onLatest)
        .semantics {
          contentDescription = label
          liveRegion = LiveRegionMode.Polite
        },
      contentAlignment = Alignment.Center
    ) {
      Icon(HandIcons.Down, null, Modifier.size(20.dp), hand.fg)
    }
  }
  if (count > 0) {
    BadgedBox(
      badge = {
        Badge(containerColor = hand.primary, contentColor = hand.onPrimary) {
          Text(
            if (count > 99) "99+" else "$count",
            Modifier.clearAndSetSemantics {}
          )
        }
      }
    ) {
      button()
    }
  } else {
    button()
  }
}

@Composable
private fun OlderLoading() {
  Row(
    Modifier.fillMaxWidth().padding(vertical = 12.dp),
    horizontalArrangement = Arrangement.Center
  ) {
    Meta("Loading older messages…")
  }
}

@Composable
private fun OlderRetry(onRetry: () -> Unit) {
  Row(
    Modifier.fillMaxWidth().padding(vertical = 12.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterHorizontally)
  ) {
    Meta("Older messages could not be read.")
    Action("Retry", onRetry)
  }
}

@Composable
private fun ChatEmpty(note: String) {
  Column(
    Modifier.fillMaxSize().padding(32.dp),
    verticalArrangement = Arrangement.Center,
    horizontalAlignment = Alignment.CenterHorizontally
  ) {
    if (note.isNotEmpty()) Text(note, color = LocalHand.current.muted, style = type.bodyMedium)
  }
}

@Composable
private fun ChatRefused(message: String) {
  Column(
    Modifier.fillMaxSize().padding(32.dp),
    verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
    horizontalAlignment = Alignment.CenterHorizontally
  ) {
    Text(message, style = type.titleMedium)
    Meta("The Board refused the conversation.")
  }
}

@Composable
private fun ChatFailed(onRetry: () -> Unit) {
  Column(
    Modifier.fillMaxSize().padding(32.dp),
    verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
    horizontalAlignment = Alignment.CenterHorizontally
  ) {
    Text("Couldn't read the conversation.", style = type.titleMedium)
    Meta("The Board's answer was not read.")
    Action("Retry", onRetry)
  }
}

@Composable
private fun ChatUnpaired() {
  Column(
    Modifier.fillMaxSize().padding(32.dp),
    verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
    horizontalAlignment = Alignment.CenterHorizontally
  ) {
    Text(
      "No longer paired",
      color = LocalHand.current.ink(Tone.Fail),
      style = type.titleMedium
    )
    Meta("Pair this phone again on the laptop's Board.")
  }
}

@Composable
private fun ChatContent(
  fleet: String,
  state: ChatState,
  zone: TimeZone,
  open: Set<String>,
  onToggle: (String) -> Unit,
  onOlder: () -> Unit,
  onRetryOlder: () -> Unit,
  onScrolled: (Boolean) -> Unit,
  onTarget: (ThreadTarget) -> Unit
) {
  val hand = LocalHand.current
  val rows = remember(state.page, state.older, zone) {
    chatRows(displayEntries(state.page, state.older), zone)
  }
  if (rows.isEmpty() && state.working == null) {
    ChatEmpty(state.note)
    return
  }
  val list = rememberLazyListState()
  val scope = rememberCoroutineScope()
  val reduce = reducedMotion()
  val threshold = with(LocalDensity.current) { 48.dp.roundToPx() }
  val rise = with(LocalDensity.current) { 8.dp.roundToPx() }
  val report = rememberUpdatedState(onScrolled)
  val older = rememberUpdatedState(onOlder)
  val head = state.page.firstOrNull()?.key
  val footer =
    state.olderLoading || state.olderFailed || (state.note.isNotEmpty() && rows.isNotEmpty())
  LaunchedEffect(list, threshold) {
    snapshotFlow {
      aboveBottom(list.firstVisibleItemIndex, list.firstVisibleItemScrollOffset, threshold)
    }
      .distinctUntilChanged()
      .collect { report.value(it) }
  }
  LaunchedEffect(list, head, threshold) {
    if (head != null &&
      pinned(list.firstVisibleItemIndex, list.firstVisibleItemScrollOffset, threshold)
    ) {
      list.scrollToItem(0)
    }
  }
  LaunchedEffect(list, state.before, state.olderLoading, state.olderFailed) {
    if (state.before <= 0 || state.olderLoading || state.olderFailed) return@LaunchedEffect
    snapshotFlow {
      val info = list.layoutInfo
      val last = info.visibleItemsInfo.lastOrNull()?.index ?: -1
      last >= info.totalItemsCount - 2
    }
      .distinctUntilChanged()
      .collect { if (it) older.value() }
  }
  Box(Modifier.fillMaxSize()) {
    LazyColumn(
      Modifier.fillMaxSize(),
      state = list,
      reverseLayout = true,
      contentPadding = PaddingValues(16.dp, 12.dp),
      verticalArrangement = Arrangement.spacedBy(0.dp, Alignment.Bottom)
    ) {
      state.working?.let { working ->
        item(key = "working") {
          Box(Modifier.padding(top = 14.dp)) {
            WorkingLine(working, state.live, state.now, zone)
          }
        }
      }
      itemsIndexed(rows, key = { _, row -> row.key }) { index, row ->
        val above = rows.getOrNull(index + 1)
        val gap =
          when {
            above != null -> if (row.isHandLine() && above.isHandLine()) 6.dp else 14.dp
            footer -> 14.dp
            else -> 0.dp
          }
        Box(Modifier.padding(top = gap)) {
          ChatRowView(row, fleet, zone, open, onToggle, onTarget)
        }
      }
      if (state.olderLoading) {
        item(key = "older-loading") {
          Box(Modifier.padding(top = 14.dp)) { OlderLoading() }
        }
      } else if (state.olderFailed) {
        item(key = "older-failed") {
          Box(Modifier.padding(top = 14.dp)) { OlderRetry(onRetryOlder) }
        }
      }
      if (state.note.isNotEmpty() && rows.isNotEmpty()) {
        item(key = "chat-note") { Meta(state.note) }
      }
    }
    Box(
      Modifier.align(Alignment.TopCenter)
        .fillMaxWidth()
        .height(16.dp)
        .background(Brush.verticalGradient(listOf(hand.bg, Color.Transparent)))
    )
    AnimatedVisibility(
      visible = state.aboveBottom,
      modifier = Modifier.align(Alignment.BottomEnd).padding(end = 16.dp, bottom = 12.dp),
      enter =
        if (reduce) EnterTransition.None
        else fadeIn(tween(150)) + slideInVertically(tween(150)) { rise },
      exit =
        if (reduce) ExitTransition.None
        else fadeOut(tween(150)) + slideOutVertically(tween(150)) { rise }
    ) {
      LatestButton(state.newReplies) {
        scope.launch {
          if (reduce) list.scrollToItem(0) else list.animateScrollToItem(0)
        }
      }
    }
  }
}

private fun ChatRow.isHandLine() = this is ChatRow.Hand || this is ChatRow.Wakes

@Composable
fun ChatScreen(
  fleet: String,
  state: ChatState,
  onOlder: () -> Unit,
  onRetry: () -> Unit,
  onRetryOlder: () -> Unit,
  onScrolled: (Boolean) -> Unit,
  onTarget: (ThreadTarget) -> Unit,
  zone: TimeZone = TimeZone.getDefault(),
  expand: Set<String> = emptySet()
) {
  var openText by rememberSaveable { mutableStateOf(expand.joinToString("\u0000")) }
  val open = remember(openText) {
    openText.split('\u0000').filter(String::isNotEmpty).toSet()
  }
  val toggle: (String) -> Unit = { key ->
    val next = if (key in open) open - key else open + key
    openText = next.joinToString("\u0000")
  }
  Box(Modifier.fillMaxSize()) {
    when {
      state.unpaired -> ChatUnpaired()
      chatBody(state) == ChatBody.Refused -> ChatRefused(state.refused.orEmpty())
      chatBody(state) == ChatBody.Failed -> ChatFailed(onRetry)
      chatBody(state) == ChatBody.Loading -> Loading()
      else ->
        ChatContent(
          fleet,
          state,
          zone,
          open,
          toggle,
          onOlder,
          onRetryOlder,
          onScrolled,
          onTarget
        )
    }
  }
}
