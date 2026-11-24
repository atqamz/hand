package io.github.atqamz.handcompanion

import java.util.concurrent.CopyOnWriteArrayList
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private const val FLEET = "f000000000004"

internal fun hit(ref: String, who: String, text: String, at: String = "2026-11-03T09:15:00Z") =
  HistoryHit(ref, who, at, text)

private fun hitJson(hit: HistoryHit) =
  """{"ref":"${hit.ref}","who":"${hit.who}","at":"${hit.at}","text":"${hit.text}"}"""

private fun searchRaw(hits: List<HistoryHit>, total: Int = hits.size, more: Boolean = false) =
  Raw(
    200,
    """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":{"q":"x","total":$total,"more":$more,"hits":${hits.joinToString(",", "[", "]", transform = ::hitJson)}}}"""
  )

private fun entryRaw(ref: String, who: String, text: String, truncated: Boolean = false) =
  Raw(
    200,
    """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":{"ref":"$ref","who":"$who","at":"2026-11-03T09:15:00Z","text":"$text","truncated":$truncated}}"""
  )

private fun failed(status: Int, code: String, message: String) =
  Raw(status, """{"error":{"status":$status,"code":"$code","message":"$message"}}""")

private class HistoryBoard(
  var search: (String) -> Raw = { searchRaw(emptyList()) },
  var entry: (String) -> Raw = { entryRaw(it, "operator", "whole") }
) : Transport {
  val calls = CopyOnWriteArrayList<String>()

  override fun request(fleet: String, post: Boolean, path: String, csrf: String?, body: String?): Raw {
    calls += "${if (post) "POST" else "GET"} $path"
    return when {
      path.startsWith("/api/v1/history/search") -> search(path)
      path.startsWith("/api/v1/history/") -> entry(path.substringAfterLast('/'))
      else -> failed(404, "not_found", "not found")
    }
  }
}

@OptIn(ExperimentalCoroutinesApi::class)
class HistoryModelTest {
  @Before fun main() = Dispatchers.setMain(Dispatchers.Unconfined)

  @After fun reset() = Dispatchers.resetMain()

  @Test
  fun aSearchAsksForTheEncodedQueryAndAPageOfTwenty() = runBlocking {
    val board = HistoryBoard()
    val model = HistoryModel(FLEET, board)
    model.edit("  amber \"kiln\" kil*  ")
    model.read("amber \"kiln\" kil*", HISTORY_PAGE)
    assertEquals(
      listOf("GET /api/v1/history/search?q=amber+%22kiln%22+kil*&limit=20"),
      board.calls.toList()
    )
  }

  @Test
  fun hitsTotalAndMoreAreKept() = runBlocking {
    val hits = listOf(hit("i4", "operator", "the «amber» kiln"), hit("r2", "report", "«amber» done"))
    val board = HistoryBoard(search = { searchRaw(hits, total = 31, more = true) })
    val model = HistoryModel(FLEET, board)
    model.read("amber", HISTORY_PAGE)
    val state = model.state.value
    assertEquals(hits, state.hits)
    assertEquals(31, state.total)
    assertTrue(state.more)
    assertEquals("amber", state.searched)
    assertFalse(state.searching)
    assertEquals("Showing 2 of 31", historyCount(state))
  }

  @Test
  fun moreAsksForTheLargestPageAndKeepsTheHitsMeanwhile() = runBlocking {
    val board = HistoryBoard(search = { searchRaw(listOf(hit("i1", "operator", "«a»")), 60, true) })
    val model = HistoryModel(FLEET, board)
    model.read("a", HISTORY_PAGE)
    board.search = { searchRaw(listOf(hit("i1", "operator", "«a»"), hit("i2", "operator", "«a»")), 60, true) }
    model.read("a", HISTORY_MAX)
    assertEquals(2, model.state.value.hits.size)
    assertEquals(HISTORY_MAX, model.state.value.limit)
    assertEquals("GET /api/v1/history/search?q=a&limit=50", board.calls.last())
    assertEquals("Showing the first 2 of 60. Narrow the search.", historyCount(model.state.value))
  }

  @Test
  fun aSearchWithNoHitsIsAnEmptyResultNotAnError() = runBlocking {
    val model = HistoryModel(FLEET, HistoryBoard())
    model.read("zzz", HISTORY_PAGE)
    val state = model.state.value
    assertTrue(state.hits.isEmpty())
    assertNull(state.problem)
    assertEquals("zzz", state.searched)
  }

  @Test
  fun theBoardsSentenceForARefusedSearchIsShownAsWritten() = runBlocking {
    val sentence = "q must not be blank"
    val board = HistoryBoard(search = { failed(400, "bad_request", sentence) })
    val model = HistoryModel(FLEET, board)
    model.read("x", HISTORY_PAGE)
    assertEquals(sentence, model.state.value.problem)
    assertFalse(model.state.value.searching)
  }

  @Test
  fun anUnreachableBoardAndATimeoutHaveTheirOwnSentences() = runBlocking {
    val board = HistoryBoard(search = { throw BoardError.Unreachable })
    val model = HistoryModel(FLEET, board)
    model.read("x", HISTORY_PAGE)
    assertEquals("The Board could not be reached.", model.state.value.problem)
    board.search = { throw BoardError.TimedOut }
    model.read("x", HISTORY_PAGE)
    assertEquals("The Board did not answer in time.", model.state.value.problem)
  }

  @Test
  fun aFailedMoreKeepsTheHitsItHad() = runBlocking {
    val board = HistoryBoard(search = { searchRaw(listOf(hit("i1", "operator", "«a»")), 40, true) })
    val model = HistoryModel(FLEET, board)
    model.read("a", HISTORY_PAGE)
    board.search = { throw BoardError.Unreachable }
    model.read("a", HISTORY_MAX)
    assertEquals(1, model.state.value.hits.size)
    assertEquals("The Board could not be reached.", model.state.value.problem)
  }

  @Test
  fun aNewQueryDropsTheOldHitsBeforeItsAnswerArrives() = runBlocking {
    val board = HistoryBoard(search = { searchRaw(listOf(hit("i1", "operator", "«a»"))) })
    val model = HistoryModel(FLEET, board)
    model.read("a", HISTORY_PAGE)
    board.search = { throw BoardError.Unreachable }
    model.read("b", HISTORY_PAGE)
    assertTrue(model.state.value.hits.isEmpty())
    assertEquals("b", model.state.value.searched)
  }

  @Test
  fun anUnpairedCompanionIsToldSoNotGivenAnError() = runBlocking {
    val board = HistoryBoard(search = { throw BoardError.Unpaired })
    val model = HistoryModel(FLEET, board)
    model.read("x", HISTORY_PAGE)
    assertTrue(model.state.value.unpaired)
    assertNull(model.state.value.problem)
  }

  @Test
  fun aBlankQueryAsksNothing() = runBlocking {
    val board = HistoryBoard()
    val model = HistoryModel(FLEET, board)
    model.edit("   ")
    model.search()
    assertTrue(board.calls.isEmpty())
    assertEquals("   ", model.state.value.query)
  }

  @Test
  fun anEntryIsReadWholeByItsRef() = runBlocking {
    val board = HistoryBoard(entry = { entryRaw(it, "operator", "the whole text", truncated = true) })
    val model = HistoryModel(FLEET, board)
    val picked = hit("i4", "operator", "the «whole» text")
    model.open(picked)
    val open = awaitEntry(model)
    assertEquals("the whole text", open.entry!!.text)
    assertTrue(open.entry!!.truncated)
    assertEquals(listOf("GET /api/v1/history/i4"), board.calls.toList())
  }

  @Test
  fun anEntryTheBoardRefusesCanBeRetried() = runBlocking {
    val board = HistoryBoard(entry = { failed(404, "not_found", "no such entry") })
    val model = HistoryModel(FLEET, board)
    model.open(hit("r9", "report", "«x»"))
    assertEquals("no such entry", awaitEntry(model).problem)
    board.entry = { entryRaw(it, "report", "back") }
    model.retryOpen()
    assertEquals("back", awaitEntry(model).entry!!.text)
  }

  @Test
  fun aRefThatIsNotAnEntryRefIsNeverSent() = runBlocking {
    val board = HistoryBoard()
    val client = BoardClient(FLEET, board)
    try {
      client.historyEntry("../status")
      error("sent")
    } catch (_: BoardError.Malformed) {}
    assertTrue(board.calls.isEmpty())
  }

  @Test
  fun closingAnEntryReturnsToTheHits() = runBlocking {
    val board = HistoryBoard(search = { searchRaw(listOf(hit("i1", "operator", "«a»"))) })
    val model = HistoryModel(FLEET, board)
    model.read("a", HISTORY_PAGE)
    model.open(model.state.value.hits.first())
    model.close()
    assertNull(model.state.value.open)
    assertEquals(1, model.state.value.hits.size)
  }

  @Test
  fun aSnippetSplitsAtTheMatchMarks() {
    assertEquals(
      listOf("a " to false, "kiln" to true, " b " to false, "amber" to true),
      historySnippet("a «kiln» b «amber»")
    )
    assertEquals(listOf("…cut «open" to false), historySnippet("…cut «open"))
    assertEquals(emptyList<Pair<String, Boolean>>(), historySnippet(""))
  }

  @Test
  fun aHitOpensTheScreenThatOwnsItsRef() {
    assertEquals(ThreadTarget.Report("r2"), historyTarget("r2"))
    assertEquals(ThreadTarget.Decision("d7"), historyTarget("d7"))
    assertEquals(ThreadTarget.Task("t12"), historyTarget("t12"))
    assertEquals(ThreadTarget.None, historyTarget("i4"))
  }

  private suspend fun awaitEntry(model: HistoryModel): HistoryOpen {
    val deadline = System.currentTimeMillis() + 5_000
    while (System.currentTimeMillis() < deadline) {
      val open = model.state.value.open
      if (open != null && !open.loading) return open
      kotlinx.coroutines.delay(5)
    }
    error("the entry did not load")
  }
}
