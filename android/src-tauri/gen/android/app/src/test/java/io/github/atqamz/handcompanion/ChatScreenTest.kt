package io.github.atqamz.handcompanion

import android.view.View
import android.view.ViewGroup
import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.node.RootForTest
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import java.util.TimeZone
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

private const val FLEET = "f00000000e0aa"
private val UTC: TimeZone = TimeZone.getTimeZone("UTC")
private val NOW = epoch("2026-11-02T11:00:00Z")!!

private fun at(minutes: Int, day: Int = 2) =
  "2026-11-0${day}T${(10 + minutes / 60).toString().padStart(2, '0')}:${(minutes % 60).toString().padStart(2, '0')}:00Z"

private val wakes =
  listOf(
    TimelineEntry(
      role = "hand",
      at = at(8),
      text = "wake: decision.answered d11"
    ),
    TimelineEntry(role = "hand", at = at(9), text = "wake: report.done r8"),
    TimelineEntry(role = "hand", at = at(10), text = "wake: attempt.reported a1: r1 done")
  )

private val conversation =
  listOf(
    TimelineEntry(
      role = "supervisor",
      at = at(58),
      no = 41,
      ref = "s1",
      model = "sonnet low",
      text =
        "The rename is **t41**. `a63` has opened the PR and keeps the old name readable " +
          "for one release, as you answered.\n\n" +
          "- t40 waits on d12\n" +
          "- t39 failed twice; r89 has the log"
    ),
    TimelineEntry(
      role = "operator",
      at = at(40),
      input = 7,
      delivery = "delivered",
      delivered = at(40),
      text = "Please look at the cookie rename before lunch.\nThe docs still use the old name."
    ),
    TimelineEntry(role = "hand", at = at(20), text = "wake: decision.answered d11"),
    TimelineEntry(role = "wakes", at = at(10), wakes = wakes),
    TimelineEntry(
      role = "supervisor",
      at = at(0, day = 1),
      no = 38,
      ref = "s1",
      text = "Good morning. Overnight a61 failed twice on the arm64 runner."
    )
  )

private fun pageState(
  entries: List<TimelineEntry> = conversation,
  live: Boolean = true,
  working: TimelineWorking? =
    TimelineWorking(
      ref = "s1",
      since = at(55),
      on = "on a wake",
      last = at(57)
    ),
  note: String = "",
  before: Int = 118,
  aboveBottom: Boolean = false,
  newReplies: Int = 0,
  olderLoading: Boolean = false,
  olderFailed: Boolean = false,
  loading: Boolean = false,
  failed: Boolean = false,
  draft: String = "",
  draftNote: String = "",
  sending: Boolean = false,
  sendError: String = "",
  now: Long = NOW
) =
  ChatState(
    page = pageKeys(entries),
    before = before,
    to = before + entries.size,
    live = live,
    note = note,
    working = working,
    loading = loading,
    failed = failed,
    olderLoading = olderLoading,
    olderFailed = olderFailed,
    aboveBottom = aboveBottom,
    newReplies = newReplies,
    draft = draft,
    draftNote = draftNote,
    sending = sending,
    sendError = sendError,
    now = now
  )

private val longConversation =
  (0 until 60).map { index ->
    val hour = (index / 60).toString().padStart(2, '0')
    val minute = (index % 60).toString().padStart(2, '0')
    TimelineEntry(
      role = "supervisor",
      at = "2026-11-01T$hour:$minute:00Z",
      no = 20 + index,
      ref = "s1",
      text = "Reply $index."
    )
  }

private val newestReply =
  TimelineEntry(
    role = "supervisor",
    at = "2026-11-02T12:00:00Z",
    no = 120,
    ref = "s1",
    text = "The newest reply."
  )

class ChatScreenTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 3200))

  private fun shot(
    dark: Boolean,
    state: ChatState,
    expand: Set<String> = emptySet(),
    zone: TimeZone = UTC,
    hint: String = "",
    hintBlocked: Boolean = false,
    input: androidx.compose.runtime.MutableState<TextFieldValue>? = null
  ) =
    paparazzi.snapshot {
      HandTheme(dark) {
        ChatScreen(
          FLEET,
          state,
          onOlder = {},
          onRetry = {},
          onRetryOlder = {},
          onScrolled = {},
          onTarget = {},
          zone = zone,
          expand = expand,
          hint = hint,
          hintBlocked = hintBlocked,
          input = input ?: remember(state.draft) { mutableStateOf(TextFieldValue(state.draft)) }
        )
      }
    }

  @Test fun conversationLight() = shot(false, pageState())

  @Test fun conversationDark() = shot(true, pageState())

  @Test
  fun wakesExpandedLight() {
    val state = pageState()
    val fold = state.page.first { it.entry.role == "wakes" }
    shot(false, state, expand = setOf(fold.key))
  }

  @Test
  fun wakesExpandedDark() {
    val state = pageState()
    val fold = state.page.first { it.entry.role == "wakes" }
    shot(true, state, expand = setOf(fold.key))
  }

  @Test fun latestLight() = shot(false, pageState(aboveBottom = true, newReplies = 3))

  @Test fun latestDark() = shot(true, pageState(aboveBottom = true, newReplies = 3))

  @Test fun olderLoadingLight() = shot(false, pageState(olderLoading = true, before = 118))

  @Test fun olderLoadingDark() = shot(true, pageState(olderLoading = true, before = 118))

  @Test
  fun olderFailedLight() = shot(false, pageState(olderFailed = true, before = 118))

  @Test
  fun olderFailedDark() = shot(true, pageState(olderFailed = true, before = 118))

  @Test
  fun emptyLight() =
    shot(false, pageState(entries = emptyList(), working = null, note = "No supervisor"))

  @Test
  fun emptyDark() =
    shot(true, pageState(entries = emptyList(), working = null, note = "No supervisor"))

  @Test fun errorLight() = shot(false, ChatState(loading = false, failed = true, now = NOW))

  @Test fun errorDark() = shot(true, ChatState(loading = false, failed = true, now = NOW))

  @Test fun loadingLight() = shot(false, ChatState(now = NOW))

  @Test fun loadingDark() = shot(true, ChatState(now = NOW))

  @Test fun unpairedLight() = shot(false, ChatState(loading = false, unpaired = true, now = NOW))

  @Test fun unpairedDark() = shot(true, ChatState(loading = false, unpaired = true, now = NOW))

  @Test fun refusedLight() =
    shot(false, ChatState(loading = false, refused = "Controls are off", now = NOW))

  @Test fun refusedDark() =
    shot(true, ChatState(loading = false, refused = "Controls are off", now = NOW))

  @Composable
  private fun pinnedScene(dark: Boolean, list: LazyListState, state: ChatState) {
    HandTheme(dark) {
      ChatContent(FLEET, state, UTC, emptySet(), {}, {}, {}, {}, {}, list)
    }
  }

  @Test
  fun aPageHeadChangePinsTheListInsideTheBand() {
    for (dark in listOf(false, true)) {
      val list = LazyListState(0, 30)
      val state = mutableStateOf(pageState(entries = longConversation))
      paparazzi.snapshot(if (dark) "bandDark" else "bandLight") {
        pinnedScene(dark, list, state.value)
      }
      assertEquals(0, list.firstVisibleItemIndex)
      assertEquals(30, list.firstVisibleItemScrollOffset)
      state.value = pageState(entries = listOf(newestReply) + longConversation)
      paparazzi.snapshot(if (dark) "pinnedDark" else "pinnedLight") {
        pinnedScene(dark, list, state.value)
      }
      assertEquals(0, list.firstVisibleItemIndex)
      assertEquals(0, list.firstVisibleItemScrollOffset)
    }
  }

  @Test fun composerEmptyLight() = shot(false, pageState())

  @Test fun composerEmptyDark() = shot(true, pageState())

  @Test fun composerTextLight() = shot(false, pageState(draft = said))

  @Test fun composerTextDark() = shot(true, pageState(draft = said))

  @Test fun composerSendingLight() = shot(false, pageState(draft = said, sending = true))

  @Test fun composerSendingDark() = shot(true, pageState(draft = said, sending = true))

  @Test
  fun composerFailedLight() =
    shot(false, pageState(draft = said, sendError = overLimit))

  @Test
  fun composerFailedDark() =
    shot(true, pageState(draft = said, sendError = overLimit))

  @Test
  fun composerHintLight() = shot(false, pageState(draft = said), hint = switchHint)

  @Test
  fun composerHintDark() = shot(true, pageState(draft = said), hint = switchHint)

  @Test
  fun composerBlockedLight() =
    shot(false, pageState(draft = said), hint = blockedHint, hintBlocked = true)

  @Test
  fun composerBlockedDark() =
    shot(true, pageState(draft = said), hint = blockedHint, hintBlocked = true)

  @Test
  fun composerNoteLight() = shot(false, pageState(draft = said, draftNote = "Draft saved 12:40"))

  @Test
  fun composerNoteDark() = shot(true, pageState(draft = said, draftNote = "Draft saved 12:40"))

  @Composable
  private fun caretScene(
    dark: Boolean,
    state: ChatState,
    input: androidx.compose.runtime.MutableState<TextFieldValue>,
    hint: String,
    height: Dp? = null,
    seen: (List<SemanticsNode>) -> Unit = {}
  ) {
    val view = LocalView.current
    HandTheme(dark) {
      Box(
        (if (height == null) Modifier else Modifier.height(height))
          .drawWithContent {
            drawContent()
            seen(semanticsTree(view))
          }
      ) {
        ChatScreen(
          FLEET,
          state,
          onOlder = {},
          onRetry = {},
          onRetryOlder = {},
          onScrolled = {},
          onTarget = {},
          zone = UTC,
          hint = hint,
          input = input
        )
      }
    }
  }

  @Test
  fun theComposerKeepsTheCaretThroughALiveUpdateAndAResize() {
    val caret = TextRange(5)
    val input = mutableStateOf(TextFieldValue(said, caret))
    val before = pageState(draft = said)
    val after =
      pageState(
        entries = listOf(newestReply) + conversation,
        draft = said,
        now = NOW + 5_000
      )
    for (dark in listOf(false, true)) {
      val name = if (dark) "Dark" else "Light"
      var tree: List<SemanticsNode> = emptyList()
      paparazzi.snapshot("caretBefore$name") {
        caretScene(dark, before, input, "") {
          tree = it
        }
      }
      assertEquals(TextFieldValue(said, caret), input.value)
      assertComposer(tree, said)
      paparazzi.snapshot("caretEmpty$name") {
        caretScene(dark, pageState(), mutableStateOf(TextFieldValue("")), "") {
          tree = it
        }
      }
      assertComposer(tree, "")
      paparazzi.snapshot("caretAfter$name") {
        caretScene(dark, after, input, "s1 is starting; your message waits.") {
          tree = it
        }
      }
      assertEquals(TextFieldValue(said, caret), input.value)
      assertComposer(tree, said)
      paparazzi.snapshot("caretResized$name") {
        caretScene(dark, after, input, "s1 is starting; your message waits.", 420.dp) {
          tree = it
        }
      }
      assertEquals(TextFieldValue(said, caret), input.value)
      assertComposer(tree, said)
    }
  }
}

private fun rootForTest(view: View): RootForTest? =
  when (view) {
    is RootForTest -> view
    is ViewGroup -> (0 until view.childCount).firstNotNullOfOrNull { rootForTest(view.getChildAt(it)) }
    else -> null
  }

// Paparazzi's merged tree drops the composer's Text nodes; the unmerged one holds them.
private fun semanticsTree(view: View): List<SemanticsNode> {
  val root = rootForTest(view) ?: return emptyList()
  val nodes = mutableListOf<SemanticsNode>()
  fun walk(node: SemanticsNode) {
    nodes += node
    node.children.forEach(::walk)
  }
  walk(root.semanticsOwner.unmergedRootSemanticsNode)
  return nodes
}

private fun SemanticsNode.texts(): List<String> =
  config.getOrElseNullable(SemanticsProperties.Text) { null }?.map { it.text }.orEmpty()

private fun assertComposer(tree: List<SemanticsNode>, draft: String) {
  val send =
    tree.firstOrNull { it.config.getOrElseNullable(SemanticsProperties.Role) { null } == Role.Button }
      ?: error("no Send button in the composer's tree")
  assertTrue("the Send button says Send", tree.any { "Send" in it.texts() })
  if (draft.isEmpty()) {
    assertTrue("Send is disabled on an empty box", send.config.contains(SemanticsProperties.Disabled))
    assertTrue(
      "the empty box shows its placeholder",
      tree.any { "Message the supervisor" in it.texts() }
    )
  } else {
    assertFalse("Send is enabled with text", send.config.contains(SemanticsProperties.Disabled))
    assertTrue("Send takes a tap with text", send.config.contains(SemanticsActions.OnClick))
  }
  val field =
    tree.firstOrNull { it.config.contains(SemanticsProperties.EditableText) }
      ?: error("no field in the composer's tree")
  assertEquals(draft, field.config.getOrElseNullable(SemanticsProperties.EditableText) { null }?.text)
}

private val said = "Also check that the relay page names the new cookie."
private val switchHint = "s1 switches to opus high after this turn; your message waits."
private val blockedHint = "s1 is waiting on a screen in Needs; your message waits too."
private val overLimit = "invalid: message is 17001 bytes; the most is 16384"
