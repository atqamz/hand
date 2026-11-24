package io.github.atqamz.handcompanion

import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private const val FLEET = "f000000000003"

private fun envelope(data: String) =
  Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":$data}""")

private fun refusal(status: Int, code: String, message: String) =
  Raw(status, """{"error":{"status":$status,"code":"$code","message":"$message"}}""")

internal fun searchPage(page: Int, pages: Int, total: Int, vararg refs: String): Raw {
  val rows =
    refs.joinToString(",") {
      """{"ref":"$it","title":"Task $it","state":"passing","project":"hand","open_decisions":1}"""
    }
  return envelope(
    """{"page":$page,"page_size":50,"total":$total,"pages":$pages,"groups":[{"day":"2026-11-20","name":"Nov 20","count":${refs.size},"tasks":[$rows]}]}"""
  )
}

private class SearchBoard(
  var projects: () -> Raw = { envelope("""{"projects":["hand","api"]}""") },
  var search: (String) -> Raw = { searchPage(1, 1, 1, "t1") }
) : Transport {
  val calls = CopyOnWriteArrayList<String>()

  override fun request(fleet: String, post: Boolean, path: String, csrf: String?, body: String?): Raw {
    calls += path
    return when {
      path == "/api/v1/projects" -> projects()
      path.startsWith("/api/v1/tasks/search") -> search(path)
      else -> refusal(404, "not_found", "not found")
    }
  }
}

private val hand = TaskSearchQuery(q = "fix", status = "all")

class TaskSearchModelTest {
  @Test
  fun theProjectsReadTurnsTheFiltersOn() = runBlocking {
    val board = SearchBoard()
    val model = TaskSearchModel(FLEET, board)
    assertNull(model.state.value.supported)
    model.loadProjects()
    assertEquals(true, model.state.value.supported)
    assertEquals(listOf("hand", "api"), model.state.value.projects)
    assertEquals(listOf("/api/v1/projects"), board.calls.toList())
  }

  @Test
  fun aHostWithoutTheRoutesHidesTheFiltersWhetherTheErrorIsJsonOrNot() = runBlocking {
    for (raw in listOf(refusal(404, "not_found", "not found"), Raw(404, "404 page not found"), refusal(405, "method_not_allowed", "no"))) {
      val model = TaskSearchModel(FLEET, SearchBoard(projects = { raw }))
      model.loadProjects()
      assertEquals(false, model.state.value.supported)
      assertEquals(SearchBody.Legacy, searchBody(model.state.value))
    }
  }

  @Test
  fun anUnreachableProjectsReadLeavesTheFiltersUndecided() = runBlocking {
    val model = TaskSearchModel(FLEET, SearchBoard(projects = { throw BoardError.Unreachable }))
    model.loadProjects()
    assertNull(model.state.value.supported)
    assertFalse(model.state.value.unpaired)
  }

  @Test
  fun anUnpairedReadSaysSo() = runBlocking {
    val model = TaskSearchModel(FLEET, SearchBoard(projects = { throw BoardError.Unpaired }))
    model.loadProjects()
    assertTrue(model.state.value.unpaired)
  }

  @Test
  fun aSearchAsksForTheQueryDesktopBuildsAndHoldsTheRows() = runBlocking {
    val board = SearchBoard()
    val model = TaskSearchModel(FLEET, board)
    model.loadProjects()
    model.run(hand, quiet = false)
    val state = model.state.value
    assertEquals("/api/v1/tasks/search?q=fix&status=all", board.calls.last())
    assertEquals(SearchBody.Rows, searchBody(state))
    assertEquals(hand, state.applied)
    assertEquals(listOf("t1"), state.result!!.groups.single().tasks.map { it.ref })
    assertEquals(1, state.result!!.groups.single().tasks.single().openDecisions)
  }

  @Test
  fun aNoRowAnswerIsEmptyAndShowsNoPagerRows() = runBlocking {
    val model = TaskSearchModel(FLEET, SearchBoard(search = { envelope("""{"page":1,"page_size":50,"total":0,"pages":0,"groups":[]}""") }))
    model.loadProjects()
    model.run(hand, quiet = false)
    assertEquals(SearchBody.Empty, searchBody(model.state.value))
  }

  @Test
  fun thePagerFollowsThePageTheAnswerEchoes() = runBlocking {
    val board = SearchBoard(search = { path -> if ("page=2" in path) searchPage(2, 3, 120, "t60") else searchPage(1, 3, 120, "t1") })
    val model = TaskSearchModel(FLEET, board)
    model.loadProjects()
    model.run(hand, quiet = false)
    assertEquals(3, model.state.value.result!!.pages)
    model.run(model.state.value.applied!!.copy(page = 2), quiet = false)
    assertEquals("/api/v1/tasks/search?page=2&q=fix&status=all", board.calls.last())
    assertEquals(2, model.state.value.result!!.page)
    assertEquals(2, model.state.value.applied!!.page)
  }

  @Test
  fun aFailedSearchShowsTheBoardsSentenceAndDropsTheRows() = runBlocking {
    val board = SearchBoard()
    val model = TaskSearchModel(FLEET, board)
    model.loadProjects()
    model.run(hand, quiet = false)
    board.search = { refusal(400, "bad_request", "since must be a date") }
    model.run(hand.copy(since = "x"), quiet = false)
    val state = model.state.value
    assertEquals(SearchBody.Failed, searchBody(state))
    assertEquals("since must be a date", state.error)
    assertNull(state.result)
    assertFalse(state.busy)
  }

  @Test
  fun anUnreachableBoardGetsTheGenericSentence() = runBlocking {
    val model = TaskSearchModel(FLEET, SearchBoard(search = { throw BoardError.Unreachable }))
    model.loadProjects()
    model.run(hand, quiet = false)
    assertEquals("The Board could not be reached.", model.state.value.error)
  }

  @Test
  fun aQuietFailureKeepsTheHeldRowsAndMarksThemStale() = runBlocking {
    val board = SearchBoard()
    val model = TaskSearchModel(FLEET, board)
    model.loadProjects()
    model.run(hand, quiet = false)
    board.search = { throw BoardError.Unreachable }
    model.refresh()
    val state = model.state.value
    assertEquals(SearchBody.Rows, searchBody(state))
    assertEquals("The Board could not be reached.", state.stale)
    assertNull(state.error)
    board.search = { searchPage(1, 1, 2, "t1", "t2") }
    model.refresh()
    assertNull(model.state.value.stale)
    assertEquals(2, model.state.value.result!!.total)
  }

  @Test
  fun aQuietRefusalIsNotRetriedAsStale() = runBlocking {
    val board = SearchBoard()
    val model = TaskSearchModel(FLEET, board)
    model.loadProjects()
    model.run(hand, quiet = false)
    board.search = { refusal(400, "bad_request", "no") }
    model.refresh()
    assertNull(model.state.value.stale)
    assertEquals(SearchBody.Rows, searchBody(model.state.value))
  }

  @Test
  fun aRouteThatVanishesTurnsTheFiltersOffAndReturnsToTheList() = runBlocking {
    val board = SearchBoard(search = { refusal(404, "not_found", "not found") })
    val model = TaskSearchModel(FLEET, board)
    model.loadProjects()
    model.run(hand, quiet = false)
    assertEquals(false, model.state.value.supported)
    assertNull(model.state.value.applied)
    assertEquals(SearchBody.Legacy, searchBody(model.state.value))
  }

  @Test
  fun aRefreshWithNothingAppliedAsksNothing() = runBlocking {
    val board = SearchBoard()
    val model = TaskSearchModel(FLEET, board)
    model.loadProjects()
    model.refresh()
    assertEquals(listOf("/api/v1/projects"), board.calls.toList())
  }

  @Test
  fun clearEmptiesTheFormAndTheResults() = runBlocking {
    val model = TaskSearchModel(FLEET, SearchBoard())
    model.loadProjects()
    model.edit { it.copy(q = "fix", status = "done", project = "hand", since = "2026-11-01", until = "2026-11-02") }
    model.run(hand, quiet = false)
    model.clear()
    val state = model.state.value
    assertEquals(TaskSearchForm(), state.form)
    assertNull(state.applied)
    assertNull(state.result)
    assertEquals(SearchBody.Legacy, searchBody(state))
  }

  @Test
  fun aBlankFormSearchGoesBackToTheListAndAsksNothing() = runBlocking {
    val board = SearchBoard()
    val model = TaskSearchModel(FLEET, board)
    model.loadProjects()
    model.search()
    assertNull(model.state.value.applied)
    assertEquals(listOf("/api/v1/projects"), board.calls.toList())
  }

  @Test
  fun aLateAnswerForAnOlderQueryNeverReplacesTheNewerOne() = runBlocking {
    val entered = CountDownLatch(1)
    val release = CountDownLatch(1)
    val board =
      SearchBoard(
        search = { path ->
          if ("q=old" in path) {
            entered.countDown()
            release.await()
            searchPage(1, 1, 1, "told")
          } else searchPage(1, 1, 1, "tnew")
        }
      )
    val model = TaskSearchModel(FLEET, board)
    model.loadProjects()
    val older = async(Dispatchers.Default) { model.run(TaskSearchQuery(q = "old"), quiet = false) }
    entered.await()
    model.run(TaskSearchQuery(q = "new"), quiet = false)
    release.countDown()
    older.await()
    val state = model.state.value
    assertEquals("new", state.applied!!.q)
    assertEquals("tnew", state.result!!.groups.single().tasks.single().ref)
    assertFalse(state.busy)
    assertNotNull(state.result)
  }

  private fun blocking(first: Int = 1, entered: CountDownLatch, release: CountDownLatch, after: () -> Raw): (String) -> Raw {
    val seen = java.util.concurrent.atomic.AtomicInteger()
    return {
      if (seen.incrementAndGet() == first) {
        entered.countDown()
        release.await()
      }
      after()
    }
  }

  @Test
  fun aCancelledSearchOrRefreshNeverLeavesTheModelBusy() = runBlocking {
    for (quiet in listOf(false, true)) {
      val entered = CountDownLatch(1)
      val release = CountDownLatch(1)
      val board = SearchBoard(search = blocking(2, entered, release) { searchPage(1, 1, 1, "t1") })
      val model = TaskSearchModel(FLEET, board)
      model.loadProjects()
      model.run(hand, quiet = false)
      val job = launch(Dispatchers.Default) { if (quiet) model.refresh() else model.run(hand, quiet = false) }
      entered.await()
      job.cancel()
      release.countDown()
      job.join()
      assertFalse("quiet=$quiet", model.state.value.busy)
    }
  }

  @Test
  fun aQuietRefreshNeverFlipsTheModelBusy() = runBlocking {
    val entered = CountDownLatch(1)
    val release = CountDownLatch(1)
    val board = SearchBoard(search = blocking(2, entered, release) { searchPage(1, 1, 1, "t1") })
    val model = TaskSearchModel(FLEET, board)
    model.loadProjects()
    model.run(hand, quiet = false)
    val job = launch(Dispatchers.Default) { model.refresh() }
    entered.await()
    try {
      assertFalse(model.state.value.busy)
    } finally {
      release.countDown()
    }
    job.join()
    assertFalse(model.state.value.busy)
  }

  @Test
  fun aLateFailureOfAnOlderQueryNeverReplacesTheNewerAnswer() = runBlocking {
    val entered = CountDownLatch(1)
    val release = CountDownLatch(1)
    val board =
      SearchBoard(
        search = { path ->
          if ("q=old" in path) {
            entered.countDown()
            release.await()
            throw BoardError.Unreachable
          }
          searchPage(1, 1, 1, "tnew")
        }
      )
    val model = TaskSearchModel(FLEET, board)
    model.loadProjects()
    val older = async(Dispatchers.Default) { model.run(TaskSearchQuery(q = "old"), quiet = false) }
    entered.await()
    model.run(TaskSearchQuery(q = "new"), quiet = false)
    release.countDown()
    older.await()
    val state = model.state.value
    assertNull(state.error)
    assertEquals("new", state.applied!!.q)
    assertEquals(SearchBody.Rows, searchBody(state))
    assertFalse(state.busy)
  }

  @Test
  fun clearDropsAnAnswerThatArrivesAfterIt() = runBlocking {
    val entered = CountDownLatch(1)
    val release = CountDownLatch(1)
    val board = SearchBoard(search = blocking(1, entered, release) { searchPage(1, 1, 1, "told") })
    val model = TaskSearchModel(FLEET, board)
    model.loadProjects()
    val older = async(Dispatchers.Default) { model.run(hand, quiet = false) }
    entered.await()
    model.clear()
    release.countDown()
    older.await()
    val state = model.state.value
    assertNull(state.applied)
    assertNull(state.result)
    assertFalse(state.busy)
    assertEquals(SearchBody.Legacy, searchBody(state))
  }

  @Test
  fun aRefreshWhileASearchIsInFlightAsksNothing() = runBlocking {
    val entered = CountDownLatch(1)
    val release = CountDownLatch(1)
    val board = SearchBoard(search = blocking(1, entered, release) { searchPage(1, 1, 1, "t1") })
    val model = TaskSearchModel(FLEET, board)
    model.loadProjects()
    val search = async(Dispatchers.Default) { model.run(hand, quiet = false) }
    entered.await()
    assertTrue(model.state.value.busy)
    model.refresh()
    release.countDown()
    search.await()
    assertEquals(1, board.calls.count { it.startsWith("/api/v1/tasks/search") })
  }

  @Test
  fun aBlankFormSearchAfterAnAppliedOneClearsTheResults() = runBlocking {
    val board = SearchBoard()
    val model = TaskSearchModel(FLEET, board)
    model.loadProjects()
    model.run(hand, quiet = false)
    model.edit { TaskSearchForm() }
    model.search()
    val state = model.state.value
    assertNull(state.applied)
    assertNull(state.result)
    assertEquals(SearchBody.Legacy, searchBody(state))
    assertEquals(1, board.calls.count { it.startsWith("/api/v1/tasks/search") })
  }

  @Test
  fun groupsWithoutTasksAreEmpty() = runBlocking {
    val raw =
      envelope(
        """{"page":1,"page_size":50,"total":0,"pages":1,"groups":[{"day":"2026-11-20","name":"Nov 20","count":0,"tasks":[]}]}"""
      )
    val model = TaskSearchModel(FLEET, SearchBoard(search = { raw }))
    model.loadProjects()
    model.run(hand, quiet = false)
    assertEquals(SearchBody.Empty, searchBody(model.state.value))
  }

  @Test
  fun anUnpairedSearchSaysSo() = runBlocking {
    val model = TaskSearchModel(FLEET, SearchBoard(search = { throw BoardError.Unpaired }))
    model.loadProjects()
    model.run(hand, quiet = false)
    assertTrue(model.state.value.unpaired)
    assertFalse(model.state.value.busy)
  }

  @Test
  fun aRefusalIsNotRetriableAndANetworkFailureIs() = runBlocking {
    val board = SearchBoard(search = { refusal(400, "bad_request", "since must be a date") })
    val model = TaskSearchModel(FLEET, board)
    model.loadProjects()
    model.run(hand, quiet = false)
    assertEquals("since must be a date", model.state.value.error)
    assertFalse(model.state.value.retriable)
    board.search = { throw BoardError.Unreachable }
    model.run(hand, quiet = false)
    assertTrue(model.state.value.retriable)
  }

  @Test
  fun retriableMeansANetworkOrServerFailureNotARefusal() {
    assertTrue(searchRetriable(BoardError.Unreachable))
    assertTrue(searchRetriable(BoardError.TimedOut))
    assertTrue(searchRetriable(BoardError.Internal("x")))
    assertTrue(searchRetriable(BoardError.Other(429, null, null)))
    assertFalse(searchRetriable(BoardError.BadRequest("x")))
    assertFalse(searchRetriable(BoardError.Forbidden("x")))
    assertFalse(searchRetriable(BoardError.Other(418, null, null)))
  }

  @Test
  fun theRowsMetaNamesTheParentTheProjectAndTheOpenDecisions() {
    val row = TaskSearchRow("t5", "T", "waiting", project = "api", parent = "t2", openDecisions = 2)
    assertEquals("waiting · under t2 · api · 2 open decisions", searchMeta(row, 2))
    assertEquals("waiting · under t2 · 2 open decisions", searchMeta(row, 1))
    assertEquals("passing · 1 open decision", searchMeta(TaskSearchRow("t1", "T", "passing", openDecisions = 1), 1))
  }
}
