package io.github.atqamz.handcompanion

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

private fun status(
  items: List<String>,
  pill: String = "failing",
  agentState: String = "idle",
  cleared: String? = null,
  summary: String? = null,
  more: Int = 0
): Raw {
  val clear = if (cleared == null) "" else """"cleared":"$cleared","summary":"$summary","""
  val needs =
    """{"waiting":${items.size},"more":$more,$clear"worst_count":${items.size},"items":[${items.joinToString(",")}]}"""
  return Raw(
    200,
    """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"cursor":"c","data":""" +
      """{"agent_state":"$agentState","composer_hint":"","pill":"$pill","line":"$pill",""" +
      """"gauge":{"text":"","level":"","pct":0},"compactions":0,"needs":$needs,""" +
      """"tasks":{"active":0,"inbox":0,"done":0,"abandoned":0},"projects":1}}"""
  )
}

private fun refused(status: Int, code: String) =
  Raw(status, """{"error":{"code":"$code","message":"no"}}""")

private class Board(var statusAnswer: () -> Raw, var ackAnswer: () -> Raw = { Raw(200, "{}") }) :
  Transport {
  val calls = mutableListOf<String>()

  override fun request(fleet: String, post: Boolean, path: String, csrf: String?, body: String?): Raw {
    calls += "${if (post) "POST" else "GET"} $path"
    return when {
      path.startsWith("/api/v1/status") -> statusAnswer()
      path == "/api/v1/session" ->
        Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":{"csrf":"tok"}}""")
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
    assertEquals(Opens.Row, opensOf(blocked))
    assertEquals(Opens.Choice, opensOf(decision))
    assertEquals(Opens.Row, opensOf(failure))
    assertEquals(Opens.Card, opensOf(report))
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
    val model = model(Board({ status(listOf(blocked, report, another)) }))
    runBlocking { model.refresh() }
    assertEquals("report:r1", model.now().openKey())
    model.toggle("report:r2")
    assertEquals("report:r2", model.now().openKey())
    model.toggle("report:r2")
    assertNull(model.now().openKey())
  }

  @Test
  fun aDecisionOpensInPlaceInsteadOfTheFirstCard() {
    val model = model(Board({ status(listOf(decision, report)) }))
    runBlocking { model.refresh() }
    assertEquals("report:r1", model.now().openKey())
    model.toggle("decision:d1")
    assertEquals("decision:d1", model.now().openKey())
    model.toggle("decision:d1")
    assertNull(model.now().openKey())
  }

  @Test
  fun whenTheOpenCardLeavesTheFirstCardOpens() {
    var items = listOf(report, decision, another)
    val model = model(Board({ status(items) }))
    runBlocking { model.refresh() }
    model.toggle("report:r2")
    items = listOf(decision, report)
    runBlocking { model.refresh() }
    assertEquals("report:r1", model.now().openKey())
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
}
