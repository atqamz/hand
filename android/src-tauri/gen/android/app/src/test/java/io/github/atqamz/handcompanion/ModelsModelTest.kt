package io.github.atqamz.handcompanion

import java.util.concurrent.CopyOnWriteArrayList
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private const val FLEET = "f000000000003"

private fun modelsRaw(running: String, switchable: Boolean = true, pending: String? = null) =
  Raw(
    200,
    """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":{"harnesses":[""" +
      """{"name":"claude","groups":[{"label":"built-in","models":[""" +
      """{"name":"opus","text":"Opus · opus"},{"name":"sonnet","text":"Sonnet · sonnet"},{"name":"haiku","text":"haiku"}]}]},""" +
      """{"name":"codex","groups":[{"label":"built-in","models":[{"name":"gpt-6","text":"gpt-6"}]}]}],""" +
      """"current":"claude","model":"$running","switchable":$switchable""" +
      (pending?.let { ""","switching":"$it"""" } ?: "") +
      "}}"
  )

private class ModelsBoard(
  var running: String = "opus",
  var switchable: Boolean = true,
  var models: (() -> Raw)? = null,
  var switch: ((String) -> Raw)? = null
) : Transport {
  val calls = CopyOnWriteArrayList<String>()

  override fun request(fleet: String, post: Boolean, path: String, csrf: String?, body: String?): Raw {
    calls += "${if (post) "POST" else "GET"} $path${body?.let { " $it" }.orEmpty()}"
    return when {
      path == "/api/v1/session" -> Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":{"csrf":"tok"}}""")
      post && path == "/api/v1/supervisor/switch" ->
        switch?.invoke(body.orEmpty())
          ?: run {
            running = "sonnet"
            Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"receipt":"Switch applied","data":{"accepted":true}}""")
          }
      !post && path == "/api/v1/models" -> models?.invoke() ?: modelsRaw(running, switchable)
      else -> Raw(404, """{"error":{"status":404,"code":"not_found","message":"not found"}}""")
    }
  }
}

private fun refusal(status: Int, code: String, message: String) =
  Raw(status, """{"error":{"status":$status,"code":"$code","message":"$message"}}""")

class ModelsModelTest {
  @Test
  fun theRunningHarnessModelsAreListedAndOnlyTheRunningOneIsCurrent() = runBlocking {
    val model = ModelsModel(FLEET, ModelsBoard())
    model.load()
    val state = model.state.value
    assertEquals(listOf("opus", "sonnet", "haiku"), state.choices.map { it.name })
    assertEquals(listOf("opus"), state.choices.filter { it.current }.map { it.name })
    assertEquals("haiku", state.choices.last().text)
    assertTrue(state.visible)
  }

  @Test
  fun theCurrentMarkerFollowsTheModelTheBoardReports() = runBlocking {
    val board = ModelsBoard(running = "haiku")
    val model = ModelsModel(FLEET, board)
    model.load()
    assertEquals(listOf("haiku"), model.state.value.choices.filter { it.current }.map { it.name })
  }

  @Test
  fun aSwitchPostsTheSupervisorAndTheModelToTheSwitchPathThenRereads() = runBlocking {
    val board = ModelsBoard()
    val model = ModelsModel(FLEET, board)
    model.load()
    var after = 0
    model.switchNow("sonnet", "s4") { after++ }
    assertEquals(
      listOf(
        "GET /api/v1/models",
        "GET /api/v1/session",
        "POST /api/v1/supervisor/switch {\"ref\":\"s4\",\"model\":\"sonnet\"}",
        "GET /api/v1/models"
      ),
      board.calls.toList()
    )
    assertEquals(1, after)
    assertEquals(listOf("sonnet"), model.state.value.choices.filter { it.current }.map { it.name })
    assertNull(model.state.value.switching)
    assertNull(model.state.value.error)
  }

  @Test
  fun aSwitchWithoutASupervisorSendsNothing() = runBlocking {
    val board = ModelsBoard()
    val model = ModelsModel(FLEET, board)
    model.switchNow("sonnet", null) {}
    assertTrue(board.calls.isEmpty())
  }

  @Test
  fun aRefusedSwitchShowsTheBoardsSentenceAndKeepsTheMarker() = runBlocking {
    val board =
      ModelsBoard(switch = {
        refusal(409, "conflict", "s2 is on screen, but s3 is running now; refresh and try again")
      })
    val model = ModelsModel(FLEET, board)
    model.load()
    model.switchNow("sonnet", "s2") {}
    assertEquals("s2 is on screen, but s3 is running now; refresh and try again", model.state.value.error)
    assertNull(model.state.value.switching)
    assertEquals(listOf("opus"), model.state.value.choices.filter { it.current }.map { it.name })
  }

  @Test
  fun theSwitchingStateShowsWhileTheWriteRuns() = runBlocking {
    lateinit var model: ModelsModel
    val seen = CopyOnWriteArrayList<String?>()
    val board =
      ModelsBoard(switch = {
        seen += model.state.value.switching
        Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":{"accepted":true}}""")
      })
    model = ModelsModel(FLEET, board)
    model.switchNow("haiku", "s1") {}
    assertEquals(listOf<String?>("haiku"), seen.toList())
  }

  @Test
  fun aRefreshRereadsTheModelsAndNeverPostsTheRefreshRoute() = runBlocking {
    val board = ModelsBoard()
    val model = ModelsModel(FLEET, board)
    model.load()
    board.models = {
      modelsRaw("opus").let { Raw(200, it.body.replace("\"haiku\",\"text\":\"haiku\"", "\"haiku\",\"text\":\"haiku\"},{\"name\":\"fable\",\"text\":\"fable\"")) }
    }
    model.refreshNow()
    assertEquals(listOf("GET /api/v1/models", "GET /api/v1/models"), board.calls.toList())
    assertTrue("fable" in model.state.value.choices.map { it.name })
    assertFalse(model.state.value.refreshing)
  }

  @Test
  fun aFailedRefreshShowsTheErrorAndKeepsTheList() = runBlocking {
    val board = ModelsBoard()
    val model = ModelsModel(FLEET, board)
    model.load()
    board.models = { throw BoardError.Unreachable }
    model.refreshNow()
    assertEquals(CONTROL_UNREACHABLE, model.state.value.error)
    assertEquals(3, model.state.value.choices.size)
    assertFalse(model.state.value.refreshing)
  }

  @Test
  fun aHostWithoutTheRouteHidesTheControl() = runBlocking {
    for (raw in listOf(refusal(404, "not_found", "no"), Raw(404, "404 page not found"), refusal(405, "method_not_allowed", "no"))) {
      val model = ModelsModel(FLEET, ModelsBoard(models = { raw }))
      model.load()
      assertEquals(false, model.state.value.supported)
      assertFalse(model.state.value.visible)
    }
  }

  @Test
  fun noRunningSupervisorOrNoModelListHidesTheControl() = runBlocking {
    val idle = ModelsModel(FLEET, ModelsBoard(switchable = false))
    idle.load()
    assertFalse(idle.state.value.visible)
    val none =
      ModelsModel(
        FLEET,
        ModelsBoard(models = {
          Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":{"harnesses":[{"name":"opencode","groups":[]}],"current":"opencode","switchable":true}}""")
        })
      )
    none.load()
    assertFalse(none.state.value.visible)
  }

  @Test
  fun aPendingSwitchIsKept() = runBlocking {
    val model = ModelsModel(FLEET, ModelsBoard(models = { modelsRaw("opus", pending = "sonnet high") }))
    model.load()
    assertEquals("sonnet high", model.state.value.pending)
  }

  @Test
  fun theRequestBodyNamesTheRefAndTheModelOnly() {
    assertEquals("{\"ref\":\"s1\",\"model\":\"opus\"}", switchBody("s1", "opus"))
  }
}
