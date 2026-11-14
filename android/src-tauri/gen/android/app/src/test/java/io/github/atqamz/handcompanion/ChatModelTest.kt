package io.github.atqamz.handcompanion

import java.util.TimeZone
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.flow.onSubscription
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private const val FLEET = "f00000000e0aa"
private const val CURSOR = "ZjAwMDAwMDAwZTBhYQBlODY3ZDUzN2E0YjczOTA0.cIX06aoaYFODA1bgziyMag"
private val UTC: TimeZone = TimeZone.getTimeZone("UTC")

private fun golden(name: String): String =
  checkNotNull(ChatModelTest::class.java.classLoader?.getResource("api/$name.json")).readText()

private fun refused(status: Int, code: String, message: String) =
  Raw(status, """{"error":{"status":$status,"code":"$code","message":"$message"}}""")

private fun envelope(data: String) =
  """{"version":1,"fleet":{"id":"$FLEET","name":"golden"},"cursor":"$CURSOR","data":$data}"""

private fun entry(
  key: String,
  role: String,
  at: String,
  text: String,
  no: Int = 0,
  input: Long = 0,
  ref: String = ""
) =
  """{"key":"$key","role":"$role","at":"$at","text":"$text","no":$no,"input":$input,"ref":"$ref"}"""

private fun timeline(older: Int, vararg entries: String) =
  envelope("""{"live":true,"older":$older,"entries":[${entries.joinToString(",")}]}""")

private class ChatBoard(
  var timelineAnswer: (String?) -> Raw = { Raw(200, golden("timeline")) },
  var olderAnswer: (Int) -> Raw = { Raw(200, timeline(70)) },
  private val features: List<String> = listOf("events"),
  private val readGate: CompletableDeferred<Unit>? = null,
  private val gateAfter: Int = 0
) : Transport {
  private val reads = AtomicInteger()
  val calls = CopyOnWriteArrayList<String>()

  override fun request(
    fleet: String,
    post: Boolean,
    path: String,
    csrf: String?,
    body: String?
  ): Raw {
    calls += "${if (post) "POST" else "GET"} $path"
    return when {
      path.startsWith("/api/v1/timeline?before=") ->
        olderAnswer(path.substringAfter("before=").toInt())
      path.startsWith("/api/v1/timeline") -> {
        val since = path.substringAfter("since=", "").takeIf { "since=" in path }
        val count = reads.incrementAndGet()
        if (readGate != null && count > gateAfter) {
          kotlinx.coroutines.runBlocking { readGate.await() }
        }
        timelineAnswer(since)
      }
      path == "/api/v1/meta" ->
        Raw(
          200,
          """{"version":1,"data":{"features":${features.joinToString(",", "[", "]") { "\"$it\"" }}}}"""
        )
      path == "/api/v1/session" ->
        Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"golden"},"data":{"csrf":"tok"}}""")
      else -> Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"golden"},"data":{}}""")
    }
  }

  fun reads() = reads.get()
}

private fun model(
  board: ChatBoard,
  bus: LiveBus = LiveBus(32),
  now: Long = 1_760_000_000_000
) = ChatModel(FLEET, board, bus, 5_000, { now })

private fun timelineReads(board: ChatBoard) =
  board.calls.filter { it.startsWith("GET /api/v1/timeline") }

private suspend fun eventually(what: String, check: () -> Boolean) {
  withTimeout(5_000) {
    while (!check()) delay(5)
  }
}

class ChatModelTest {
  @Test
  fun theGoldenAnswerMapsEveryFieldItReads() = runBlocking {
    val chat = model(ChatBoard())
    chat.load()
    val state = chat.state.value
    assertTrue(state.live)
    assertEquals(0, state.before)
    assertEquals(3, state.to)
    assertEquals("", state.note)
    val working = state.working ?: error("no working")
    assertEquals("s1", working.ref)
    assertEquals("2026-11-01T04:40:00Z", working.since)
    assertEquals("since the turn began", working.on)
    assertEquals("2026-11-01T04:41:00Z", working.last)
    assertEquals(3, state.page.size)
    assertEquals(
      listOf(
        "supervisor@2026-11-01T04:41:00Z",
        "hand@2026-11-01T04:40:00Z",
        "operator@2026-11-01T04:40:00Z"
      ),
      state.page.map { it.key }
    )
    val supervisor = state.page[0].entry
    assertEquals("supervisor", supervisor.role)
    assertEquals("All good.", supervisor.text)
    assertEquals(2, supervisor.no)
    assertEquals("s1", supervisor.ref)
    assertEquals("sonnet low", supervisor.model)
    assertEquals("hand", state.page[1].entry.role)
    assertEquals("s1 started · claude sonnet low", state.page[1].entry.text)
    assertEquals("operator", state.page[2].entry.role)
    assertEquals("status?", state.page[2].entry.text)
    assertEquals(ChatBody.Content, chatBody(state))
  }

  @Test
  fun oneReadOnOpenAndTheCursorOnTheNext() = runBlocking {
    val board =
      ChatBoard(timelineAnswer = { since ->
        if (since == null) Raw(200, golden("timeline")) else Raw(304, "")
      })
    val chat = model(board)
    chat.load()
    chat.load()
    assertEquals(
      listOf("GET /api/v1/timeline", "GET /api/v1/timeline?since=$CURSOR"),
      timelineReads(board)
    )
    assertEquals(3, chat.state.value.page.size)
    assertFalse(chat.state.value.loading)
  }

  @Test
  fun aBadCursorIsDroppedAndTheReadRepeatsWithoutIt() = runBlocking {
    val board =
      ChatBoard(timelineAnswer = { since ->
        when (since) {
          null -> Raw(200, golden("timeline"))
          CURSOR -> refused(400, "bad_cursor", "bad cursor")
          else -> Raw(200, golden("timeline"))
        }
      })
    val chat = model(board)
    chat.load()
    chat.load()
    assertEquals(
      listOf(
        "GET /api/v1/timeline",
        "GET /api/v1/timeline?since=$CURSOR",
        "GET /api/v1/timeline"
      ),
      timelineReads(board)
    )
    assertEquals(3, chat.state.value.page.size)
  }

  @Test
  fun aTimelineDeltaByKeyKeepsAChangedEntryInPlace() = runBlocking {
    val chat = model(ChatBoard())
    chat.load()
    chat.reduce(
      LiveEvent(
        FLEET,
        "timeline",
        """{"snapshot":false,"from":3,"to":3,"older":0,"live":true,"entries":[""" +
          entry(
            "supervisor@2026-11-01T04:41:00Z",
            "supervisor",
            "2026-11-01T04:41:00Z",
            "All good!",
            no = 2,
            ref = "s1"
          ) +
          "]}"
      )
    )
    val state = chat.state.value
    assertEquals(3, state.page.size)
    assertEquals(
      "supervisor@2026-11-01T04:41:00Z",
      state.page[0].key
    )
    assertEquals("All good!", state.page[0].entry.text)
  }

  @Test
  fun aNewEntryGoesInByAtAndADropRemovesItsKey() = runBlocking {
    val chat = model(ChatBoard())
    chat.load()
    chat.reduce(
      LiveEvent(
        FLEET,
        "timeline",
        """{"snapshot":false,"from":3,"to":4,"older":0,"live":true,"entries":[""" +
          entry(
            "supervisor@2026-11-01T04:45:00Z",
            "supervisor",
            "2026-11-01T04:45:00Z",
            "Done.",
            no = 3,
            ref = "s1"
          ) +
          "]}"
      )
    )
    assertEquals(4, chat.state.value.page.size)
    assertEquals("Done.", chat.state.value.page[0].entry.text)
    chat.reduce(
      LiveEvent(
        FLEET,
        "timeline",
        """{"snapshot":false,"from":4,"to":4,"older":0,"live":true,"drop":["hand@2026-11-01T04:40:00Z"],"entries":[]}"""
      )
    )
    assertEquals(3, chat.state.value.page.size)
    assertTrue(chat.state.value.page.none { it.key == "hand@2026-11-01T04:40:00Z" })
  }

  @Test
  fun aSnapshotReplacesThePageAndItsRange() = runBlocking {
    val chat = model(ChatBoard())
    chat.load()
    chat.reduce(
      LiveEvent(
        FLEET,
        "timeline",
        """{"snapshot":true,"from":0,"to":10,"older":9,"live":false,"note":"No supervisor",""" +
          """"entries":[""" +
          entry("i9", "operator", "2026-11-01T05:00:00Z", "hello", input = 9) +
          "]}"
      )
    )
    val state = chat.state.value
    assertEquals(1, state.page.size)
    assertEquals("i9", state.page[0].key)
    assertEquals(10, state.to)
    assertEquals(9, state.before)
    assertFalse(state.live)
    assertEquals("No supervisor", state.note)
  }

  @Test
  fun aGapRepublishesTheHeldPageExactlyOnce() = runBlocking {
    val gate = CompletableDeferred<Unit>()
    val reads = AtomicInteger()
    val pageAt12 =
      timeline(
        10,
        entry(
          "supervisor@2026-11-01T05:00:00Z",
          "supervisor",
          "2026-11-01T05:00:00Z",
          "Fresh",
          no = 11,
          ref = "s1"
        ),
        entry(
          "operator@2026-11-01T04:59:00Z",
          "operator",
          "2026-11-01T04:59:00Z",
          "Hi",
          no = 10
        )
      )
    val board =
      ChatBoard(
        timelineAnswer = {
          if (reads.incrementAndGet() == 1) Raw(200, golden("timeline")) else Raw(200, pageAt12)
        },
        readGate = gate,
        gateAfter = 1
      )
    val bus = LiveBus(16)
    val chat = model(board, bus)
    chat.load()
    assertEquals(3, chat.state.value.to)
    val ready = CompletableDeferred<Unit>()
    val collector =
      launch(Dispatchers.Default) {
        bus.events.onSubscription { ready.complete(Unit) }.filter { it.fleet == FLEET }.collect {
          chat.reduce(it)
        }
      }
    ready.await()
    bus.publish(
      FLEET,
      "timeline",
      """{"snapshot":true,"from":0,"to":3,"older":0,"live":true,"entries":[]}"""
    )
    eventually("the snapshot is applied") { chat.state.value.to == 3 }
    bus.publish(
      FLEET,
      "timeline",
      """{"snapshot":false,"from":7,"to":9,"older":0,"live":true,"entries":[]}"""
    )
    eventually("the gap starts a read") { board.reads() == 2 }
    bus.publish(
      FLEET,
      "timeline",
      """{"snapshot":false,"from":11,"to":12,"older":0,"live":true,"entries":[]}"""
    )
    gate.complete(Unit)
    eventually("the page is read again") { chat.state.value.to == 12 }
    delay(100)
    assertEquals(2, board.reads())
    collector.cancelAndJoin()
  }

  @Test
  fun resyncReadsForEveryTimelineItNames() = runBlocking {
    val board =
      ChatBoard(timelineAnswer = { since ->
        if (since == null) Raw(200, golden("timeline")) else Raw(304, "")
      })
    val chat = model(board)
    chat.load()
    val start = board.reads()
    chat.reduce(LiveEvent(FLEET, "resync", """{"topics":["status"]}"""))
    assertEquals(start, board.reads())
    chat.reduce(LiveEvent(FLEET, "resync", """{"topics":["timeline"]}"""))
    assertEquals(start + 1, board.reads())
    chat.reduce(LiveEvent(FLEET, "resync", """{"topics":["status","timeline"]}"""))
    assertEquals(start + 2, board.reads())
    chat.reduce(LiveEvent(FLEET, "touched", """{"topics":["tasks"]}"""))
    assertEquals(start + 2, board.reads())
    chat.reduce(LiveEvent(FLEET, "touched", """{"topics":["timeline"]}"""))
    assertEquals(start + 3, board.reads())
  }

  @Test
  fun aHintOrADeltaDoesNotReadThePage() = runBlocking {
    val board = ChatBoard()
    val chat = model(board)
    chat.load()
    val start = timelineReads(board).size
    chat.reduce(
      LiveEvent(
        FLEET,
        "timeline",
        """{"snapshot":false,"from":3,"to":3,"older":0,"live":true,"entries":[]}"""
      )
    )
    assertEquals(start, timelineReads(board).size)
  }

  @Test
  fun anOlderPageKeepsTheAnchoredEntryAndTheCursor() = runBlocking {
    val latest =
      timeline(
        120,
        entry(
          "supervisor@2026-11-02T10:00:00Z",
          "supervisor",
          "2026-11-02T10:00:00Z",
          "Newest",
          no = 20,
          ref = "s1"
        ),
        entry(
          "operator@2026-11-02T09:00:00Z",
          "operator",
          "2026-11-02T09:00:00Z",
          "Earlier",
          no = 19
        )
      )
    val olderPage =
      timeline(
        70,
        entry(
          "supervisor@2026-11-01T10:00:00Z",
          "supervisor",
          "2026-11-01T10:00:00Z",
          "Old",
          no = 18,
          ref = "s1"
        ),
        entry(
          "operator@2026-11-01T09:00:00Z",
          "operator",
          "2026-11-01T09:00:00Z",
          "Older",
          no = 17
        )
      )
    val board =
      ChatBoard(timelineAnswer = { Raw(200, latest) }, olderAnswer = { Raw(200, olderPage) })
    val chat = model(board)
    chat.load()
    val anchor = chat.state.value.page[1].key
    val before =
      displayEntries(chat.state.value.page, chat.state.value.older)
        .indexOfFirst { it.key == anchor }
    chat.older()
    val state = chat.state.value
    assertEquals(2, state.older.size)
    assertEquals(70, state.before)
    assertEquals(
      before,
      displayEntries(state.page, state.older).indexOfFirst { it.key == anchor }
    )
    assertEquals("Older", displayEntries(state.page, state.older).last().entry.text)
    assertEquals(listOf("GET /api/v1/timeline?before=120"), board.calls.filter { "before=" in it })
  }

  @Test
  fun aFailedOlderPageKeepsTheListAndOffersRetry() = runBlocking {
    var fail = true
    val latest =
      timeline(
        120,
        entry("i2", "operator", "2026-11-02T09:00:00Z", "Earlier", input = 2)
      )
    val board =
      ChatBoard(
        timelineAnswer = { Raw(200, latest) },
        olderAnswer = {
          if (fail) refused(500, "internal", "boom")
          else Raw(200, timeline(70, entry("i1", "operator", "2026-11-01T09:00:00Z", "Old", input = 1)))
        }
      )
    val chat = model(board)
    chat.load()
    chat.older()
    assertTrue(chat.state.value.olderFailed)
    assertEquals(1, chat.state.value.page.size)
    assertEquals(ChatBody.Content, chatBody(chat.state.value))
    fail = false
    chat.older()
    assertEquals(1, chat.state.value.older.size)
    assertFalse(chat.state.value.olderFailed)
    assertEquals(70, chat.state.value.before)
  }

  @Test
  fun loadingFailureAndARefusalAreTheScreensStates() = runBlocking {
    val board = ChatBoard(timelineAnswer = { throw BoardError.Unreachable })
    val chat = model(board)
    assertEquals(ChatBody.Loading, chatBody(chat.state.value))
    chat.load()
    assertEquals(ChatBody.Failed, chatBody(chat.state.value))
    assertTrue(chat.state.value.failed)

    val refusedBoard =
      ChatBoard(timelineAnswer = { refused(403, "forbidden", "Controls are off") })
    val refusedChat = model(refusedBoard)
    refusedChat.load()
    assertEquals(ChatBody.Refused, chatBody(refusedChat.state.value))
    assertEquals("Controls are off", refusedChat.state.value.refused)

    val unpaired = ChatBoard(timelineAnswer = { throw BoardError.Unpaired })
    val unpairedChat = model(unpaired)
    unpairedChat.load()
    assertTrue(unpairedChat.state.value.unpaired)
  }

  @Test
  fun anEmptyConversationSaysWhatTheBoardSays() = runBlocking {
    val board = ChatBoard(timelineAnswer = { Raw(200, timeline(0)) })
    val chat = model(board)
    chat.load()
    assertEquals("", chat.state.value.note)
    val noted = ChatBoard(timelineAnswer = { Raw(200, envelope("""{"live":true,"older":0,"note":"No supervisor","entries":[]}""")) })
    val notedChat = model(noted)
    notedChat.load()
    assertEquals("No supervisor", notedChat.state.value.note)
    assertEquals(ChatBody.Content, chatBody(notedChat.state.value))
  }

  @Test
  fun theLatestCountClearsWhenTheBottomIsReached() = runBlocking {
    val board = ChatBoard()
    val chat = model(board)
    chat.load()
    chat.scrolled(true)
    assertTrue(chat.state.value.aboveBottom)
    chat.reduce(
      LiveEvent(
        FLEET,
        "timeline",
        """{"snapshot":false,"from":3,"to":4,"older":0,"live":true,"entries":[""" +
          entry(
            "supervisor@2026-11-01T04:45:00Z",
            "supervisor",
            "2026-11-01T04:45:00Z",
            "One",
            no = 3,
            ref = "s1"
          ) +
          "]}"
      )
    )
    assertEquals(1, chat.state.value.newReplies)
    chat.reduce(
      LiveEvent(
        FLEET,
        "timeline",
        """{"snapshot":false,"from":4,"to":5,"older":0,"live":true,"entries":[""" +
          entry(
            "supervisor@2026-11-01T04:46:00Z",
            "supervisor",
            "2026-11-01T04:46:00Z",
            "Two",
            no = 4,
            ref = "s1"
          ) +
          "]}"
      )
    )
    assertEquals(2, chat.state.value.newReplies)
    chat.scrolled(false)
    assertFalse(chat.state.value.aboveBottom)
    assertEquals(0, chat.state.value.newReplies)
  }

  @Test
  fun theLatestThresholdIsFortyEightPixelsAboveTheBottom() {
    val threshold = 144
    assertFalse(aboveBottom(0, 0, threshold))
    assertFalse(aboveBottom(0, 144, threshold))
    assertTrue(aboveBottom(0, 145, threshold))
    assertTrue(aboveBottom(1, 0, threshold))
    assertEquals(0, latestCount(2, false, listOf(TimelineEntry(role = "supervisor"))))
    assertEquals(3, latestCount(2, true, listOf(TimelineEntry(role = "supervisor"))))
    assertEquals(
      3,
      latestCount(
        2,
        true,
        listOf(TimelineEntry(role = "supervisor"), TimelineEntry(role = "operator"))
      )
    )
  }

  @Test
  fun aDayDividerAppearsOnlyWhereTheDayChanges() {
    val rows =
      chatRows(
        pageKeys(
          listOf(
            TimelineEntry(role = "supervisor", at = "2026-11-02T10:00:00Z", text = "Two"),
            TimelineEntry(role = "operator", at = "2026-11-02T09:00:00Z", text = "One"),
            TimelineEntry(role = "supervisor", at = "2026-11-01T23:00:00Z", text = "Old")
          )
        ),
        UTC
      )
    val days = rows.filterIsInstance<ChatRow.Day>()
    assertEquals(1, days.size)
    assertEquals("Mon 2 Nov", days.single().label)
    assertEquals(2, rows.indexOf(days.single()))
    assertEquals(
      0,
      chatRows(
        pageKeys(
          listOf(
            TimelineEntry(role = "supervisor", at = "2026-11-02T10:00:00Z", text = "Two"),
            TimelineEntry(role = "operator", at = "2026-11-02T09:00:00Z", text = "One")
          )
        ),
        UTC
      ).count { it is ChatRow.Day }
    )
  }

  @Test
  fun consecutiveWakesAreOneLineThatExpandsToTheirOwnLines() {
    val wakes =
      listOf(
        TimelineEntry(role = "hand", at = "2026-11-01T12:28:00Z", text = "wake: decision.answered d11"),
        TimelineEntry(role = "hand", at = "2026-11-01T12:28:00Z", text = "wake: report.done r8"),
        TimelineEntry(role = "hand", at = "2026-11-01T12:29:00Z", text = "wake: attempt.reported a1: r1 done")
      )
    val folded = TimelineEntry(role = "wakes", at = "2026-11-01T12:29:00Z", wakes = wakes)
    val rows = chatRows(pageKeys(listOf(folded)), UTC)
    val line = rows.single() as ChatRow.Wakes
    assertEquals(3, line.entry.entry.wakes.size)
    assertEquals("3 wakes 12:28–12:29", wakesWords(line.entry.entry.wakes, UTC))
    assertEquals(
      listOf("decision answered d11", "report done r8", "attempt reported a1: r1 done"),
      line.entry.entry.wakes.map { handWords(it.text) }
    )
  }

  @Test
  fun aRepeatedRoleAndAtTakesTheLowerSuffixFromTheOldestEntry() {
    val page =
      pageKeys(
        listOf(
          TimelineEntry(role = "hand", at = "2026-11-01T10:00:00Z", text = "c"),
          TimelineEntry(role = "hand", at = "2026-11-01T10:00:00Z", text = "b"),
          TimelineEntry(role = "hand", at = "2026-11-01T10:00:00Z", text = "a")
        )
      )
    assertEquals(
      listOf("hand@2026-11-01T10:00:00Z#3", "hand@2026-11-01T10:00:00Z#2", "hand@2026-11-01T10:00:00Z"),
      page.map { it.key }
    )
  }

  @Test
  fun theWorkingLineSaysHowLongAndWhereItIs() {
    val now = epoch("2026-11-01T04:44:00Z")!!
    val working =
      TimelineWorking(
        ref = "s1",
        since = "2026-11-01T04:40:00Z",
        on = "on a wake",
        last = "2026-11-01T04:43:48Z",
        from = "2026-11-01T04:19:00Z",
        input = 3
      )
    assertEquals(
      "s1 working · 4m · last output 12s ago · on your 04:19 message",
      workingWords(working, now, UTC)
    )
    assertEquals(
      "s1 working · 4m · last output 12s ago · since the turn began",
      workingWords(working.copy(on = "since the turn began", input = 0, from = ""), now, UTC)
    )
    assertEquals("2h 5m", elapsedWords(now - 125 * 60_000, now))
  }

  @Test
  fun theHandLinesWordsDropTheWakePrefixAndTheEventDots() {
    assertEquals("decision answered d11", handWords("wake: decision.answered d11"))
    assertEquals("attempt failed a5", handWords("wake: attempt.failed a5:"))
    assertEquals("s1 started · claude sonnet low", handWords("s1 started · claude sonnet low"))
  }

  @Test
  fun aDroppedCursorFallsBackToAPlainRead() = runBlocking {
    val board =
      ChatBoard(timelineAnswer = { since ->
        when (since) {
          null -> Raw(200, golden("timeline"))
          else -> Raw(400, """{"error":{"status":400,"code":"bad_cursor","message":"bad"}}""")
        }
      })
    val chat = model(board)
    chat.load()
    chat.load()
    assertEquals(3, board.reads())
    assertNull(chat.state.value.refused)
  }
}
