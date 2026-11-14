package io.github.atqamz.handcompanion

import java.time.Instant
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private const val FLEET = "f000000000003"

private fun need(kind: String, word: String, tone: String, extra: String = "") =
  """{"kind":"$kind","ref":"${kind.first()}1","word":"$word","tone":"$tone"$extra}"""

private val blocked = need("blocked", "BLOCKED", "fail")
private val worker = need("worker", "BLOCKED", "fail")
private val failure = need("failure", "FAILED", "fail", ""","title":"Flaky test"""")
private val decision = need("decision", "DECISION", "wait", ""","title":"Pin it?"""")
private val report =
  need("report", "REPORT", "neutral", ""","title":"Done","report":"r1","excerpt":"All tests pass."""")
private val another = report.replace("r1", "r2")
private val blocked2 = blocked.replace("b1", "b2")

private fun status(
  items: List<String>,
  pill: String = "failing",
  agentState: String = "idle",
  cleared: String? = null,
  summary: String? = null,
  more: Int = 0,
  supervisor: String? = null
): Raw {
  val clear = if (cleared == null) "" else """"cleared":"$cleared","summary":"$summary","""
  val needs =
    """{"waiting":${items.size},"more":$more,$clear"worst_count":${items.size},"items":[${items.joinToString(",")}]}"""
  val guard = supervisor?.let { """"supervisor":$it,""" }.orEmpty()
  return Raw(
    200,
    """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"cursor":"c","data":{""" +
      guard +
      """"agent_state":"$agentState","composer_hint":"","pill":"$pill","line":"$pill",""" +
      """"gauge":{"text":"","level":"","pct":0},"compactions":0,"needs":$needs,""" +
      """"tasks":{"active":0,"inbox":0,"done":0,"abandoned":0},"projects":1}}"""
  )
}

private const val DIGEST = "f8c722e0710879ae811b2fe7ad0c8ba0"

private fun supervisor(revision: Long = 7, pressed: Long = 0): String =
  """{"ref":"s1","harness":"claude","model":"sonnet","effort":"low","status":"running","live":true,""" +
    """"pending":0,"resumable":false,"blocked":true,"keys":{"revision":$revision,"digest":"$DIGEST","pressed":$pressed}}"""

private fun blockedNeed(deny: String? = null): String =
  """{"kind":"blocked","ref":"s1","title":"Allow Bash?","word":"BLOCKED","tone":"fail",""" +
    """"excerpt":"Bash command\n   rm -f x"""" +
    (deny?.let { ""","deny_at":"$it"""" } ?: "") +
    "}"

private fun refused(status: Int, code: String) =
  Raw(status, """{"error":{"code":"$code","message":"no"}}""")

private fun task(
  ref: String,
  state: String,
  title: String = "Task $ref",
  parent: String? = null,
  waits: String? = null
): String =
  buildString {
    append("""{"ref":"$ref","title":"$title","state":"$state"""")
    if (parent != null) append(""","parent":"$parent"""")
    if (waits != null) append(""","waits":$waits""")
    append("}")
  }

private fun group(name: String, count: Int, rows: List<String>): String =
  """{"name":"$name","count":$count,"tasks":[${rows.joinToString(",")}]}"""

private const val TASK_WAITS = """{"due":"2026-11-14T09:00:00Z","note":"the laptop is away"}"""

private fun finishedRows(extra: Boolean): List<String> =
  listOf(
    task("t38", "passing"),
    task("t37", "passing"),
    task("t35", "idle"),
    task("t33", "passing"),
    task("t32", "idle")
  ) + if (extra) listOf(task("t31", "passing"), task("t30", "idle")) else emptyList()

private fun tasksData(all: Boolean = false, hidden: Int = 0): Raw {
  val finished =
    if (all) {
      listOf(
        group("Done", 5, finishedRows(true).take(5)),
        group("Abandoned", 2, finishedRows(true).drop(5))
      )
    } else {
      listOf(group("Finished", 7, finishedRows(false)))
    }
  val groups =
    listOf(
      group("Active", 3, listOf(task("t41", "running"), task("t40", "waiting"), task("t39", "failing"))),
      group("Inbox", 2, listOf(task("t43", "idle"), task("t44", "idle", waits = TASK_WAITS)))
    ) + finished
  return Raw(
    200,
    """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"cursor":"c","data":{"all":$all,""" +
      """"hidden":$hidden,"finished_count":7,"groups":[${groups.joinToString(",")}]}}"""
  )
}

private class Board(
  var statusAnswer: () -> Raw,
  var ackAnswer: () -> Raw = { Raw(200, "{}") },
  var keysAnswer: () -> Raw = { statusAnswer() },
  var forceAnswer: () -> Raw = { statusAnswer() },
  var tasksAnswer: (Boolean) -> Raw = { all -> tasksData(all = all) }
) : Transport {
  val calls = mutableListOf<String>()
  val posts = mutableListOf<Pair<String, String?>>()

  override fun request(fleet: String, post: Boolean, path: String, csrf: String?, body: String?): Raw {
    calls += "${if (post) "POST" else "GET"} $path"
    if (post) posts += path to body
    return when {
      path.startsWith("/api/v1/status") -> statusAnswer()
      path == "/api/v1/tasks" -> tasksAnswer(false)
      path == "/api/v1/tasks?all=1" -> tasksAnswer(true)
      path == "/api/v1/session" ->
        Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":{"csrf":"tok"}}""")
      path == "/api/v1/supervisor/keys" -> keysAnswer()
      path == "/api/v1/supervisor/force" -> forceAnswer()
      else -> ackAnswer()
    }
  }
}

private fun ack(report: String = "r1") =
  Raw(
    200,
    """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":{"report":"$report"}}"""
  )

private fun model(board: Board, name: String? = null) =
  FleetModel(FLEET, name, board, { "4d92…6ed6" }, { "12:41" })

private fun FleetModel.now() = state.value

private fun opensOf(json: String) =
  bands(listOf(Json.decodeFromString<Need>(json))).single().entries.single().opens

class FleetModelTest {
  @Test
  fun needsIsTheFirstTabWhenAnythingWaitsAndChatOtherwise() {
    val waiting = model(Board({ status(listOf(report)) }))
    runBlocking { waiting.refresh() }
    assertEquals(Tab.Needs, waiting.now().tab)
    val quiet = model(Board({ status(emptyList(), "ready", "idle", "2026-11-01T12:41:00Z", "1 running") }))
    runBlocking { quiet.refresh() }
    assertEquals(Tab.Chat, quiet.now().tab)
  }

  @Test
  fun aChosenTabSurvivesTheNextPoll() {
    val model = model(Board({ status(listOf(report)) }))
    runBlocking { model.refresh() }
    model.select(Tab.Tasks)
    runBlocking { model.refresh() }
    assertEquals(Tab.Tasks, model.now().tab)
  }

  @Test
  fun enteringPicksNeedsOverChatWithoutAPoll() {
    val board = Board({ status(listOf(report)) })
    val model = model(board)
    runBlocking { model.refresh() }
    model.select(Tab.Chat)
    val polls = board.calls.size
    model.enter()
    assertEquals(Tab.Needs, model.now().tab)
    assertEquals(polls, board.calls.size)
  }

  @Test
  fun enteringKeepsATasksTabAndAnEmptyNeeds() {
    val model = model(Board({ status(listOf(report)) }))
    runBlocking { model.refresh() }
    model.select(Tab.Tasks)
    model.enter()
    assertEquals(Tab.Tasks, model.now().tab)
    val quiet = model(Board({ status(emptyList()) }))
    runBlocking { quiet.refresh() }
    quiet.enter()
    assertEquals(Tab.Chat, quiet.now().tab)
  }

  @Test
  fun aNotModifiedAfterEnteringDoesNotFlipAChatChoice() {
    var calls = 0
    val model = model(Board({ if (calls++ == 0) status(listOf(report)) else Raw(304, "") }))
    runBlocking { model.refresh() }
    model.enter()
    model.select(Tab.Chat)
    runBlocking { model.refresh() }
    assertEquals(Tab.Chat, model.now().tab)
  }

  @Test
  fun theBadgesCarryTheCountTheWorstToneAndTheWorkingDot() {
    val model = model(Board({ status(listOf(blocked, decision, report), "failing", "working") }))
    runBlocking { model.refresh() }
    val state = model.now()
    assertEquals(3, state.waiting)
    assertEquals(Tone.Fail, state.tone)
    assertTrue(state.working)
    assertEquals(Badge("failing", Tone.Fail), state.pill)
    assertEquals("atlas", state.name)
    val calm = model(Board({ status(listOf(report), "working", "idle") }))
    runBlocking { calm.refresh() }
    assertFalse(calm.now().working)
    assertEquals(Tone.Neutral, calm.now().tone)
  }

  @Test
  fun theTabsSayWhatTheirBadgesShow() {
    val state = FleetState(waiting = 5, working = true)
    assertEquals("Needs, 5 waiting", tabLabel(Tab.Needs, state))
    assertEquals("Tasks", tabLabel(Tab.Tasks, state))
    assertEquals("Chat, supervisor working", tabLabel(Tab.Chat, state))
    assertEquals("Needs", tabLabel(Tab.Needs, FleetState()))
    assertEquals("Chat", tabLabel(Tab.Chat, FleetState()))
  }

  @Test
  fun openingAFleetShowsLoadingBeforeTheFirstAnswer() {
    assertEquals(Body.Loading, body(FleetState(name = "atlas")))
  }

  @Test
  fun aTappedTabBeforeTheFirstAnswerStillShowsLoading() {
    assertEquals(Body.Loading, body(FleetState(name = "atlas", tab = Tab.Tasks)))
    assertEquals(Body.Loading, body(FleetState(name = "atlas", tab = Tab.Needs)))
  }

  @Test
  fun aFailedFirstRequestShowsLoadingUnderTheBand() {
    val band = Band("Can't reach 4d92…6ed6 since 12:41")
    assertEquals(Body.Loading, body(FleetState(name = "atlas", band = band)))
  }

  @Test
  fun theBodyFollowsTheAnswerAndThePairing() {
    assertEquals(Body.Needs, body(FleetState(loaded = true, tab = Tab.Needs)))
    assertEquals(Body.Placeholder, body(FleetState(loaded = true, tab = Tab.Chat)))
    assertEquals(Body.Unpaired, body(FleetState(unpaired = true)))
  }

  @Test
  fun adjacentItemsOfTheSameWordShareABandInTheBoardsOrder() {
    val items = listOf(blocked, worker, failure, decision, report, another)
    val model = model(Board({ status(items) }))
    runBlocking { model.refresh() }
    val bands = model.now().bands
    assertEquals(listOf("Blocked", "Failed", "Decision", "Report"), bands.map { it.title })
    assertEquals(listOf(Tone.Fail, Tone.Fail, Tone.Wait, Tone.Neutral), bands.map { it.tone })
    assertEquals(listOf(2, 1, 1, 2), bands.map { it.entries.size })
  }

  @Test
  fun everyKindHandSendsIsShownUnderItsWord() {
    val kinds =
      listOf(
        "blocked" to "BLOCKED",
        "worker" to "BLOCKED",
        "failure" to "FAILED",
        "limited" to "LIMITED",
        "nosup" to "NO SUPERVISOR",
        "stalled" to "STALLED",
        "resume" to "INTERRUPTED",
        "decision" to "DECISION",
        "report" to "REPORT",
        "quiet" to "QUIET",
        "long" to "LONG TURN",
        "kept" to "KEPT",
        "artifacts" to "OLD FILES"
      )
    val tones =
      mapOf("blocked" to "fail", "worker" to "fail", "failure" to "fail", "resume" to "fail") +
        listOf("limited", "nosup", "stalled", "decision").associateWith { "wait" }
    val model =
      model(
        Board({ status(kinds.map { (kind, word) -> need(kind, word, tones[kind] ?: "neutral") }) })
      )
    runBlocking { model.refresh() }
    assertEquals(
      listOf(
        "Blocked",
        "Failed",
        "Limited",
        "No supervisor",
        "Stalled",
        "Interrupted",
        "Decision",
        "Report",
        "Quiet",
        "Long turn",
        "Kept",
        "Old files"
      ),
      model.now().bands.map { it.title }
    )
    assertEquals(
      listOf(
        Tone.Fail,
        Tone.Fail,
        Tone.Wait,
        Tone.Wait,
        Tone.Wait,
        Tone.Fail,
        Tone.Wait,
        Tone.Neutral,
        Tone.Neutral,
        Tone.Neutral,
        Tone.Neutral,
        Tone.Neutral
      ),
      model.now().bands.map { it.tone }
    )
    assertEquals(13, model.now().bands.sumOf { it.entries.size })
  }

  @Test
  fun eachKindOpensWhereTheApiCanActOnIt() {
    assertEquals(Opens.Card, opensOf(blocked))
    assertEquals(Opens.Choice, opensOf(decision))
    assertEquals(Opens.Row, opensOf(failure))
    assertEquals(Opens.Report, opensOf(report))
    assertEquals(Opens.Web, opensOf(worker))
    assertEquals(Opens.Web, opensOf(need("artifacts", "OLD FILES", "neutral")))
  }

  @Test
  fun theMoreCountComesThrough() {
    val model = model(Board({ status(listOf(report), more = 3) }))
    runBlocking { model.refresh() }
    assertEquals(3, model.now().more)
  }

  @Test
  fun theFirstCardIsOpenAndATapMovesOrClosesIt() {
    val model = model(Board({ status(listOf(blocked, blocked2, report)) }))
    runBlocking { model.refresh() }
    assertEquals("blocked:b1", model.now().openKey())
    model.toggle("blocked:b2")
    assertEquals("blocked:b2", model.now().openKey())
    model.toggle("blocked:b2")
    assertNull(model.now().openKey())
  }

  @Test
  fun aDecisionOpensInPlaceInsteadOfTheFirstCard() {
    val model = model(Board({ status(listOf(decision, blocked)) }))
    runBlocking { model.refresh() }
    assertEquals("blocked:b1", model.now().openKey())
    model.toggle("decision:d1")
    assertEquals("decision:d1", model.now().openKey())
    model.toggle("decision:d1")
    assertNull(model.now().openKey())
  }

  @Test
  fun whenTheOpenCardLeavesTheFirstCardOpens() {
    var items = listOf(blocked, blocked2, decision)
    val model = model(Board({ status(items) }))
    runBlocking { model.refresh() }
    model.toggle("blocked:b2")
    items = listOf(decision, blocked)
    runBlocking { model.refresh() }
    assertEquals("blocked:b1", model.now().openKey())
  }

  @Test
  fun markReadPostsTheAckAndTheNextPollDropsTheItem() {
    var read = false
    val board =
      Board({ status(if (read) listOf(decision) else listOf(report, decision)) }, { read = true; ack() })
    val model = model(board)
    runBlocking {
      model.refresh()
      model.acknowledge("r1")
    }
    assertTrue("POST /api/v1/report/r1/ack" in board.calls)
    assertEquals(listOf("Decision"), model.now().bands.map { it.title })
    assertNull(model.now().notice)
    assertTrue(model.now().marking.isEmpty())
  }

  @Test
  fun theButtonStaysOffUntilTheItemLeavesAndASecondTapSendsNothing() {
    val board = Board({ status(listOf(report)) }, { ack() })
    val model = model(board)
    runBlocking {
      model.refresh()
      model.acknowledge("r1")
      model.acknowledge("r1")
    }
    assertEquals(setOf("r1"), model.now().marking)
    assertEquals(1, board.calls.count { it == "POST /api/v1/report/r1/ack" })
  }

  @Test
  fun aRefusedMarkReadSaysSoKeepsTheItemAndTurnsTheButtonBackOn() {
    val board = Board({ status(listOf(report)) }, { refused(409, "conflict") })
    val model = model(board)
    runBlocking {
      model.refresh()
      model.acknowledge("r1")
    }
    assertEquals(Notice("r1", "r1 is already read."), model.now().notice)
    assertEquals(listOf("Report"), model.now().bands.map { it.title })
    assertTrue(model.now().marking.isEmpty())
  }

  @Test
  fun aLostAckIsNotClaimedAsDoneNorAsFailed() {
    val board = Board({ status(listOf(report)) }, { throw BoardError.Lost })
    val model = model(board)
    runBlocking {
      model.refresh()
      model.acknowledge("r1")
    }
    assertEquals(Notice("r1", "Not sure r1 was marked read."), model.now().notice)
  }

  @Test
  fun aNoticeGoesWhenDismissedButNotAnIdenticalLaterOne() {
    val board = Board({ status(listOf(report)) }, { refused(404, "not_found") })
    val model = model(board)
    runBlocking {
      model.refresh()
      model.acknowledge("r1")
    }
    val first = checkNotNull(model.now().notice)
    runBlocking { model.acknowledge("r1") }
    val second = checkNotNull(model.now().notice)
    assertEquals(first, second)
    model.dismiss(first)
    assertEquals(second, model.now().notice)
    model.dismiss(second)
    assertNull(model.now().notice)
  }

  @Test
  fun aRefusalNoticeExpiresOnItsOwn() {
    val board = Board({ status(listOf(report)) }, { refused(409, "conflict") })
    val model = FleetModel(FLEET, null, board, { null }, { "12:41" }, 50)
    runBlocking {
      model.refresh()
      withTimeout(5_000) {
        val expiry = launch(Dispatchers.IO) { model.acknowledgeAndExpire("r1") }
        model.state.first { it.notice != null }
        model.state.first { it.notice == null }
        expiry.join()
      }
    }
    assertNull(model.now().notice)
  }

  @Test
  fun anAckTheBoardTookButAnswersUnreadableIsNotAFailure() {
    val board = Board({ status(listOf(report)) }, { Raw(200, "not json") })
    val model = model(board)
    runBlocking {
      model.refresh()
      model.acknowledge("r1")
    }
    assertNull(model.now().notice)
  }

  @Test
  fun allClearKeepsTheTimeItClearedAndTheSummary() {
    val model =
      model(Board({ status(emptyList(), "ready", "idle", "2026-11-01T12:41:00Z", "1 running · 6 inbox") }))
    runBlocking { model.refresh() }
    val state = model.now()
    assertTrue(state.bands.isEmpty())
    assertEquals("12:41", state.cleared)
    assertEquals("1 running · 6 inbox", state.summary)
    assertTrue(state.loaded)
  }

  @Test
  fun anUnreachableLaptopShowsTheBandAndKeepsTheLastItems() {
    var down = false
    val board = Board({ if (down) throw BoardError.Unreachable else status(listOf(report)) })
    val model = model(board)
    runBlocking { model.refresh() }
    down = true
    runBlocking { model.refresh() }
    assertEquals("Can't reach 4d92…6ed6 since 12:41", model.now().band?.title)
    assertEquals(listOf("Report"), model.now().bands.map { it.title })
    down = false
    runBlocking { model.refresh() }
    assertNull(model.now().band)
  }

  @Test
  fun aFirstOpenWithTheLaptopDownKeepsTheNameShowsTheBandAndPicksNoTabYet() {
    val model = model(Board({ throw BoardError.Unreachable }), "atlas")
    runBlocking { model.refresh() }
    val state = model.now()
    assertEquals("atlas", state.name)
    assertEquals("Can't reach 4d92…6ed6 since 12:41", state.band?.title)
    assertFalse(state.loaded)
    assertNull(state.tab)
  }

  @Test
  fun aPairingTheBoardDroppedMarksTheFleetUnpaired() {
    val model = model(Board({ throw BoardError.Unpaired }))
    runBlocking { model.refresh() }
    assertTrue(model.now().unpaired)
  }

  @Test
  fun aPressSendsTheGuardAndTheSupervisorRef() {
    val board =
      Board(
        { status(listOf(blockedNeed()), supervisor = supervisor()) },
        keysAnswer = { status(listOf(blockedNeed()), supervisor = supervisor(revision = 8, pressed = 1)) }
      )
    val model = model(board)
    runBlocking {
      model.refresh()
      assertEquals(Notice("s1", "Sent 2 to s1"), model.pressNow(listOf("2")))
    }
    assertEquals(listOf("POST /api/v1/supervisor/keys"), board.calls.filter { it.startsWith("POST") })
    assertEquals(
      """{"keys":["2"],"revision":7,"digest":"$DIGEST","after":0,"ref":"s1"}""",
      board.posts.single().second
    )
    assertEquals(8L, model.now().supervisor?.keys?.revision)
    assertEquals(1L, model.now().supervisor?.keys?.pressed)
    assertEquals("Sent 2 · waiting…", model.now().pressed["blocked:s1"])
    assertEquals("", model.now().open)
  }

  @Test
  fun anOptionWithSeveralKeysSendsThemInOneBody() {
    val board =
      Board(
        { status(listOf(blockedNeed()), supervisor = supervisor()) },
        keysAnswer = { status(listOf(blockedNeed()), supervisor = supervisor(revision = 8, pressed = 1)) }
      )
    val model = model(board)
    runBlocking {
      model.refresh()
      model.pressNow(listOf("down", "enter"))
    }
    assertEquals(
      """{"keys":["down","enter"],"revision":7,"digest":"$DIGEST","after":0,"ref":"s1"}""",
      board.posts.single().second
    )
  }

  @Test
  fun aConflictReopensTheFreshScreenAndSendsNothingElse() {
    var fresh = false
    val board =
      Board(
        { status(listOf(blockedNeed()), supervisor = supervisor(revision = if (fresh) 8 else 7)) },
        keysAnswer = {
          fresh = true
          refused(409, "conflict")
        }
      )
    val model = model(board)
    runBlocking {
      model.refresh()
      model.pressNow(listOf("2"))
    }
    assertEquals("blocked:s1", model.now().open)
    assertEquals(Notice("s1", "The screen moved; nothing was sent."), model.now().notice)
    assertEquals(8L, model.now().supervisor?.keys?.revision)
    assertEquals(1, board.calls.count { it == "POST /api/v1/supervisor/keys" })
    assertTrue(model.now().pressed.isEmpty())
  }

  @Test
  fun aPressWithNoSupervisorSendsNothing() {
    val board = Board({ status(listOf(blockedNeed())) })
    val model = model(board)
    runBlocking {
      model.refresh()
      assertNull(model.pressNow(listOf("2")))
    }
    assertTrue(board.posts.isEmpty())
  }

  @Test
  fun theForceBodyNamesTheSupervisorOnScreen() {
    val board =
      Board(
        { status(listOf(blockedNeed()), supervisor = supervisor()) },
        forceAnswer = { status(listOf(blockedNeed()), supervisor = supervisor(revision = 9)) }
      )
    val model = model(board)
    runBlocking {
      model.refresh()
      model.forceNow()
    }
    assertEquals(listOf("POST /api/v1/supervisor/force"), board.calls.filter { it.startsWith("POST") })
    assertEquals("""{"ref":"s1"}""", board.posts.single().second)
    assertEquals(9L, model.now().supervisor?.keys?.revision)
    assertEquals(Notice("s1", "Typed the queued messages to s1."), model.now().notice)
  }

  @Test
  fun aPressReceiptAndItsWaitingLineGoAwayOnTheirOwn() {
    val board =
      Board(
        { status(listOf(blockedNeed()), supervisor = supervisor()) },
        keysAnswer = { status(listOf(blockedNeed()), supervisor = supervisor(revision = 8, pressed = 1)) }
      )
    val model = FleetModel(FLEET, null, board, { null }, { "12:41" }, 50)
    runBlocking {
      model.refresh()
      withTimeout(5_000) {
        val expiry = launch(Dispatchers.IO) { model.pressAndExpire(listOf("2")) }
        assertEquals("Sent 2 · waiting…", model.state.first { it.notice != null }.pressed["blocked:s1"])
        model.state.first { it.notice == null }
        expiry.join()
      }
    }
    assertNull(model.now().notice)
    assertTrue(model.now().pressed.isEmpty())
  }

  @Test
  fun aReplacedReceiptClearsTheWaitingLine() {
    val board =
      Board(
        { status(listOf(blockedNeed()), supervisor = supervisor()) },
        keysAnswer = { status(listOf(blockedNeed()), supervisor = supervisor(revision = 8, pressed = 1)) },
        forceAnswer = { status(listOf(blockedNeed()), supervisor = supervisor(revision = 9)) }
      )
    val model = model(board)
    runBlocking {
      model.refresh()
      model.pressNow(listOf("2"))
      assertEquals("Sent 2 · waiting…", model.now().pressed["blocked:s1"])
      model.forceNow()
    }
    assertEquals(Notice("s1", "Typed the queued messages to s1."), model.now().notice)
    assertTrue(model.now().pressed.isEmpty())
  }

  @Test
  fun theCountdownTicksLocallyAndSaysWhenTheScreenWasDenied() {
    val base = 1_700_000_000_000L
    var clock = base
    val board = Board({ status(listOf(blockedNeed(deny = Instant.ofEpochMilli(base + 61_000).toString())), supervisor = supervisor()) })
    val model = FleetModel(FLEET, null, board, { null }, { "12:41" }, 4_000, { clock })
    runBlocking {
      model.refresh()
      assertEquals("1:01", model.now().left)
      val polls = board.calls.count { it.startsWith("GET /api/v1/status") }
      clock += 60_000
      assertNull(model.tickOnce())
      assertEquals("0:01", model.now().left)
      assertEquals(polls, board.calls.count { it.startsWith("GET /api/v1/status") })
      clock += 1_000
      assertEquals("The screen was denied.", model.tickOnce()?.text)
      assertNull(model.now().left)
      assertEquals(polls + 1, board.calls.count { it.startsWith("GET /api/v1/status") })
    }
  }

  @Test
  fun theTasksRequestNamesAllOnlyWhenTheFinishedGroupIsExpanded() {
    val board = Board({ status(listOf(report)) })
    val model = model(board)
    model.select(Tab.Tasks)
    runBlocking {
      model.loadTasks()
      model.loadTasks(all = true)
    }
    assertEquals(
      listOf("GET /api/v1/tasks", "GET /api/v1/tasks?all=1"),
      board.calls.filter { it.startsWith("GET /api/v1/tasks") }
    )
  }

  @Test
  fun theGroupsKeepTheBoardsOrderAndTheFinishedCountSurvivesTheFiveShown() {
    val model = model(Board({ status(listOf(report)) }))
    model.select(Tab.Tasks)
    runBlocking { model.loadTasks() }
    var tasks = model.now().tasks
    assertEquals(listOf("Active", "Inbox", "Finished"), tasks.groups.map { it.name })
    assertEquals(5, tasks.groups.last().tasks.size)
    assertEquals(7, tasks.finished)
    assertEquals(0, tasks.hidden)
    runBlocking { model.loadTasks(all = true) }
    tasks = model.now().tasks
    assertEquals(listOf("Active", "Inbox", "Done", "Abandoned"), tasks.groups.map { it.name })
    assertEquals(listOf(5, 2), tasks.groups.drop(2).map { it.tasks.size })
    assertEquals(7, tasks.groups.drop(2).sumOf { it.tasks.size })
    assertEquals(0, tasks.hidden)
  }

  @Test
  fun aSubTaskTakesOneIndentAndSaysUnderOnlyWhenItsParentIsElsewhere() {
    val group =
      TaskGroup(
        "Inbox",
        4,
        listOf(
          TaskRow("t1", "Parent", "running"),
          TaskRow("t2", "Child", "idle", parent = "t1"),
          TaskRow("t3", "Grandchild", "idle", parent = "t2"),
          TaskRow("t4", "Elsewhere", "idle", parent = "t9")
        )
      )
    assertEquals(listOf(0, 1, 1, 0), lines(group).map { it.depth })
    assertEquals(listOf(null, null, null, "t9"), lines(group).map { it.under })
    assertEquals("idle · under t9", taskMeta(lines(group).last()))
  }

  @Test
  fun waitsReadAsTheBoardWritesThem() {
    assertEquals(
      "Waits until Nov 14: the laptop is away",
      waitsLine(TaskWaits("2026-11-14T09:00:00Z", "the laptop is away"))
    )
    assertEquals("Waits until Nov 14", waitsLine(TaskWaits("2026-11-14T09:00:00Z")))
  }

  @Test
  fun theTaskBodyNamesLoadingFailedEmptyAndRows() {
    assertEquals(TasksBody.Loading, tasksBody(TasksState()))
    assertEquals(TasksBody.Failed, tasksBody(TasksState(failed = true)))
    assertEquals(TasksBody.Empty, tasksBody(TasksState(loaded = true)))
    assertEquals(
      TasksBody.Rows,
      tasksBody(TasksState(loaded = true, groups = listOf(TaskGroup("Active", 1, listOf(TaskRow("t1", "x", "idle"))))))
    )
    assertEquals(
      TasksBody.Rows,
      tasksBody(TasksState(loaded = true, failed = true, groups = listOf(TaskGroup("Active", 1, emptyList()))))
    )
  }

  @Test
  fun theFooterNamesTheFinishedCountAndHiddenIsShownInBothViews() {
    assertEquals("Show all finished tasks (7)", tasksFooter(TasksState(loaded = true, finished = 7)))
    assertNull(tasksFooter(TasksState(loaded = true, all = true, finished = 7)))
    assertNull(tasksFooter(TasksState(loaded = true)))
    assertEquals("12 more tasks are not shown", tasksHidden(TasksState(hidden = 12)))
    assertEquals("1 more task is not shown", tasksHidden(TasksState(hidden = 1)))
    assertEquals("12 more tasks are not shown", tasksHidden(TasksState(all = true, hidden = 12)))
    assertNull(tasksHidden(TasksState(loaded = true)))
  }

  @Test
  fun aFailedFirstReadShowsTheFailureBodyAndRecovers() {
    var down = true
    val board = Board({ status(listOf(report)) }, tasksAnswer = { if (down) throw BoardError.Lost else tasksData() })
    val model = model(board)
    model.select(Tab.Tasks)
    runBlocking { model.loadTasks() }
    assertTrue(model.now().tasks.failed)
    assertFalse(model.now().tasks.loaded)
    assertEquals(TasksBody.Failed, tasksBody(model.now().tasks))
    assertNull(model.now().band)
    down = false
    runBlocking { model.loadTasks() }
    assertFalse(model.now().tasks.failed)
    assertTrue(model.now().tasks.loaded)
  }

  @Test
  fun aFailedReloadKeepsTheRowsAndSaysSoInTheBand() {
    var down = false
    val board = Board({ status(listOf(report)) }, tasksAnswer = { if (down) throw BoardError.Lost else tasksData() })
    val model = model(board)
    model.select(Tab.Tasks)
    runBlocking { model.loadTasks() }
    val kept = model.now().tasks
    down = true
    runBlocking { model.loadTasks() }
    val failed = model.now().tasks
    assertTrue(failed.failed)
    assertTrue(failed.loaded)
    assertEquals(kept.groups, failed.groups)
    assertEquals(TasksBody.Rows, tasksBody(failed))
    assertEquals("The Board's answer could not be read.", model.now().band?.title)
    down = false
    runBlocking { model.loadTasks() }
    assertFalse(model.now().tasks.failed)
  }

  @Test
  fun aFailedShowAllKeepsTheFiveRowsAndTheAction() {
    val board =
      Board(
        { status(listOf(report)) },
        tasksAnswer = { all -> if (all) throw BoardError.Lost else tasksData() }
      )
    val model = model(board)
    model.select(Tab.Tasks)
    runBlocking { model.loadTasks() }
    val kept = model.now().tasks
    runBlocking { model.loadTasks(all = true) }
    val failed = model.now().tasks
    assertFalse(failed.all)
    assertTrue(failed.failed)
    assertEquals(kept.groups, failed.groups)
    assertEquals("Show all finished tasks (7)", tasksFooter(failed))
    assertEquals(TasksBody.Rows, tasksBody(failed))
    assertTrue(model.now().band != null)
    runBlocking { model.loadTasks() }
    assertFalse(model.now().tasks.failed)
  }

  @Test
  fun thePollReadsTasksOnlyWhileTheTasksTabIsVisible() {
    val board = Board({ status(listOf(report)) })
    val model = model(board)
    runBlocking { model.refresh() }
    assertEquals(0, board.calls.count { it.startsWith("GET /api/v1/tasks") })
    model.select(Tab.Tasks)
    runBlocking { model.refresh() }
    assertEquals(1, board.calls.count { it.startsWith("GET /api/v1/tasks") })
    model.select(Tab.Chat)
    runBlocking { model.refresh() }
    assertEquals(1, board.calls.count { it.startsWith("GET /api/v1/tasks") })
  }

  @Test
  fun aTabsContentStaysWhenAnotherTabIsChosen() {
    val model = model(Board({ status(listOf(report)) }))
    model.select(Tab.Tasks)
    runBlocking { model.loadTasks() }
    val kept = model.now().tasks
    model.select(Tab.Needs)
    assertEquals(kept, model.now().tasks)
    model.select(Tab.Tasks)
    assertEquals(kept, model.now().tasks)
  }
}
