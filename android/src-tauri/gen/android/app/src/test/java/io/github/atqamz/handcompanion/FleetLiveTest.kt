package io.github.atqamz.handcompanion

import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.TimeZone
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private const val FLEET = "f000000000003"

private const val REPORT =
  """{"kind":"report","ref":"r1","word":"REPORT","tone":"neutral","title":"Done","report":"r1"}"""

private fun blocked(deny: String) =
  """{"kind":"blocked","ref":"s1","title":"Allow Bash?","word":"BLOCKED","tone":"fail",""" +
    """"deny_at":"$deny"}"""

private fun statusData(items: String = REPORT, now: String? = null, waiting: Int = 1) =
  """{${now?.let { """"now":"$it",""" } ?: ""}""" +
    """"agent_state":"idle","composer_hint":"","pill":"ready","line":"ready",""" +
    """"gauge":{"text":"","level":"","pct":0},"compactions":0,""" +
    """"needs":{"waiting":$waiting,"more":0,"worst_count":1,"items":[$items]},""" +
    """"tasks":{"active":0,"inbox":0,"done":0,"abandoned":0},"projects":1}"""

private fun statusAnswer(items: String = REPORT) =
  Raw(
    200,
    """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"cursor":"c","data":""" +
      statusData(items) +
      "}"
  )

private class LiveBoard(
  private val features: List<String> = listOf("events"),
  private val answer: () -> Raw = { statusAnswer() }
) : Transport {
  val calls = mutableListOf<String>()
  var liveStarted = false
  var liveStopped = 0

  override fun request(
    fleet: String,
    post: Boolean,
    path: String,
    csrf: String?,
    body: String?
  ): Raw {
    calls += "${if (post) "POST" else "GET"} $path"
    return when {
      path == "/api/v1/meta" ->
        Raw(
          200,
          """{"version":1,"generated_at":"x","data":{"version":"9.9.9","features":""" +
            features.joinToString(",", "[", "]") { "\"$it\"" } +
            "}}"
        )
      path.startsWith("/api/v1/status") -> answer()
      path == "/api/v1/tasks" -> tasksAnswer(false)
      path == "/api/v1/tasks?all=1" -> tasksAnswer(true)
      else -> Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":{}}""")
    }
  }

  private fun tasksAnswer(all: Boolean) =
    Raw(
      200,
      """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"cursor":"c","data":""" +
        """{"all":$all,"hidden":0,"finished_count":0,"groups":""" +
        """[{"name":"Active","count":1,"tasks":""" +
        """[{"ref":"t40","title":"Follow the stream","state":"running"}]}]}}"""
    )

  fun statusReads() = calls.count { it.startsWith("GET /api/v1/status") }
}

private fun rfc3339(millis: Long): String =
  SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'", Locale.ROOT)
    .apply { timeZone = TimeZone.getTimeZone("UTC") }
    .format(Date(millis))

private fun model(
  board: LiveBoard,
  bus: LiveBus = LiveBus(8),
  nowMillis: () -> Long = { System.currentTimeMillis() }
) =
  FleetModel(
    FLEET,
    "atlas",
    board,
    { "4d92…6ed6" },
    { "12:41" },
    nowMillis = nowMillis,
    bus = bus,
    pollMillis = 20,
    startLive = { board.liveStarted = true },
    stopLive = { board.liveStopped++ }
  )

private suspend fun awaitFailures(model: FleetModel, count: Int) {
  withTimeout(5_000) {
    while (model.liveFailures.get() != count) delay(5)
  }
}

class FleetLiveTest {
  @Test
  fun anInlineStatusReplacesTheStateWithoutARead() = runBlocking {
    val board = LiveBoard()
    val model = model(board)
    model.reduce(LiveEvent(FLEET, "status", statusData()))
    val state = model.state.value
    assertEquals(1, state.waiting)
    assertEquals("atlas", state.name)
    assertEquals(Tab.Needs, state.tab)
    assertTrue(state.loaded)
    assertEquals(Badge("ready", Tone.Pass), state.pill)
    assertTrue(board.calls.isEmpty())
  }

  @Test
  fun aHelloNamesTheFleet() = runBlocking {
    val model = model(LiveBoard())
    model.reduce(
      LiveEvent(FLEET, "hello", """{"version":1,"fleet":{"id":"$FLEET","name":"hand"}}""")
    )
    assertEquals("hand", model.state.value.name)
  }

  @Test
  fun aStatusResyncRereadsTheVisibleScreen() = runBlocking {
    val board = LiveBoard()
    val model = model(board)
    model.reduce(LiveEvent(FLEET, "resync", """{"topics":["status"]}"""))
    assertEquals(listOf("GET /api/v1/status"), board.calls)
    assertTrue(model.state.value.loaded)
  }

  @Test
  fun aResyncForAnotherScreenReadsNothing() = runBlocking {
    val board = LiveBoard()
    val model = model(board)
    model.reduce(LiveEvent(FLEET, "resync", """{"topics":["tasks"]}"""))
    model.reduce(LiveEvent(FLEET, "touched", """{"topics":["tasks"],"tasks":["t4"]}"""))
    assertTrue(board.calls.isEmpty())
  }

  @Test
  fun aTasksHintRereadsTheOpenTasksTab() = runBlocking {
    val board = LiveBoard()
    val model = model(board)
    model.select(Tab.Tasks)
    model.reduce(LiveEvent(FLEET, "touched", """{"topics":["tasks"],"tasks":["t40"]}"""))
    assertEquals(listOf("GET /api/v1/tasks"), board.calls)
    assertEquals(
      listOf("t40"),
      model.state.value.tasks.groups.flatMap { it.tasks }.map { it.ref }
    )
  }

  @Test
  fun aTasksResyncWithStatusReadsBothOnce() = runBlocking {
    val board = LiveBoard()
    val model = model(board)
    model.select(Tab.Tasks)
    model.reduce(LiveEvent(FLEET, "resync", """{"topics":["status","tasks"]}"""))
    assertEquals(listOf("GET /api/v1/status", "GET /api/v1/tasks"), board.calls)
  }

  @Test
  fun aLaptopWithoutTheFeaturePollsInsteadOfOpeningAStream() = runBlocking {
    val board = LiveBoard(features = listOf("fleets", "pair"))
    val model = model(board)
    val job = launch(Dispatchers.Default) { model.run() }
    withTimeout(5_000) { while (board.statusReads() < 3) delay(10) }
    job.cancelAndJoin()
    assertFalse(board.liveStarted)
    assertEquals("GET /api/v1/meta", board.calls[1])
    assertTrue(board.statusReads() >= 3)
  }

  @Test
  fun threeFailedStreamsFallBackToThePoll() = runBlocking {
    val board = LiveBoard()
    val bus = LiveBus(8)
    val model = model(board, bus)
    val job = launch(Dispatchers.Default) { model.run() }
    withTimeout(10_000) {
      while (!board.liveStarted) delay(5)
      while (board.statusReads() < 4) {
        bus.link(FLEET, "reconnecting", 1_700_000_000_000)
        delay(50)
      }
    }
    job.cancelAndJoin()
    assertTrue(board.liveStarted)
    assertEquals(1, board.liveStopped)
  }

  @Test
  fun aFlappingStreamFallsBackAfterThreeFailedConnections() = runBlocking {
    val board = LiveBoard()
    val bus = LiveBus(8)
    val model = model(board, bus)
    val job = launch(Dispatchers.Default) { model.run() }
    var live = true
    withTimeout(10_000) {
      while (!board.liveStarted) delay(5)
      while (board.statusReads() < 4) {
        bus.link(FLEET, if (live) "live" else "reconnecting", 1_700_000_000_000)
        live = !live
        delay(30)
      }
    }
    job.cancelAndJoin()
    assertEquals(1, board.liveStopped)
  }

  @Test
  fun aLinkForAnotherFleetDoesNotTouchTheModel() = runBlocking {
    val board = LiveBoard()
    val bus = LiveBus(8)
    val model = model(board, bus)
    val job = launch(Dispatchers.Default) { model.run() }
    withTimeout(5_000) { while (!board.liveStarted) delay(5) }
    val other = "f999999999999"
    bus.link(other, "unpaired", 0)
    bus.link(other, "reconnecting", 1_700_000_000_000)
    bus.link(other, "stopped", 0)
    delay(200)
    assertFalse(model.state.value.unpaired)
    assertNull(model.state.value.band)
    assertEquals(1, board.statusReads())
    assertEquals(0, board.liveStopped)
    bus.link(FLEET, "reconnecting", 1_700_000_000_000)
    withTimeout(5_000) { while (model.state.value.band == null) delay(5) }
    assertEquals("Reconnecting…", model.state.value.band?.title)
    job.cancelAndJoin()
  }

  @Test
  fun aStatusBetweenReconnectsKeepsTheStreamLive() = runBlocking {
    val board = LiveBoard()
    val bus = LiveBus(8)
    val model = model(board, bus)
    val job = launch(Dispatchers.Default) { model.run() }
    withTimeout(5_000) { while (!board.liveStarted) delay(5) }
    bus.link(FLEET, "reconnecting", 1_700_000_000_000)
    awaitFailures(model, 1)
    bus.link(FLEET, "reconnecting", 1_700_000_000_000)
    awaitFailures(model, 2)
    bus.link(FLEET, "live", 0)
    bus.publish(FLEET, "status", statusData(waiting = 0))
    withTimeout(5_000) { while (model.state.value.waiting != 0) delay(5) }
    assertEquals(0, model.liveFailures.get())
    bus.link(FLEET, "reconnecting", 1_700_000_000_000)
    awaitFailures(model, 1)
    bus.link(FLEET, "reconnecting", 1_700_000_000_000)
    awaitFailures(model, 2)
    delay(200)
    assertEquals(0, board.liveStopped)
    assertEquals(1, board.statusReads())
    job.cancelAndJoin()
  }

  @Test
  fun aStatusResyncBetweenReconnectsKeepsTheStreamLive() = runBlocking {
    val board = LiveBoard()
    val bus = LiveBus(8)
    val model = model(board, bus)
    val job = launch(Dispatchers.Default) { model.run() }
    withTimeout(5_000) { while (!board.liveStarted) delay(5) }
    bus.link(FLEET, "reconnecting", 1_700_000_000_000)
    awaitFailures(model, 1)
    bus.link(FLEET, "reconnecting", 1_700_000_000_000)
    awaitFailures(model, 2)
    bus.publish(FLEET, "resync", """{"topics":["status"]}""")
    awaitFailures(model, 0)
    bus.link(FLEET, "reconnecting", 1_700_000_000_000)
    awaitFailures(model, 1)
    bus.link(FLEET, "reconnecting", 1_700_000_000_000)
    awaitFailures(model, 2)
    delay(200)
    assertEquals(0, board.liveStopped)
    job.cancelAndJoin()
  }

  @Test
  fun aHealthyStreamWithThreeReconnectsDoesNotFallBack() = runBlocking {
    val board = LiveBoard()
    val bus = LiveBus(8)
    val model = model(board, bus)
    val job = launch(Dispatchers.Default) { model.run() }
    withTimeout(5_000) { while (!board.liveStarted) delay(5) }
    for (waiting in 0..2) {
      bus.link(FLEET, "reconnecting", 1_700_000_000_000)
      awaitFailures(model, 1)
      bus.publish(FLEET, "status", statusData(waiting = waiting))
      withTimeout(5_000) { while (model.state.value.waiting != waiting) delay(5) }
      awaitFailures(model, 0)
    }
    delay(200)
    assertEquals(0, board.liveStopped)
    assertEquals(1, board.statusReads())
    job.cancelAndJoin()
  }

  @Test
  fun aStoppedLinkFallsBackToThePoll() = runBlocking {
    val board = LiveBoard()
    val bus = LiveBus(8)
    val model = model(board, bus)
    val job = launch(Dispatchers.Default) { model.run() }
    withTimeout(10_000) {
      while (!board.liveStarted) delay(5)
      while (board.statusReads() < 3) {
        bus.link(FLEET, "stopped", 0)
        delay(30)
      }
    }
    job.cancelAndJoin()
    assertEquals(1, board.liveStopped)
  }

  @Test
  fun theBlockedCountdownUsesTheBoardsClock() = runBlocking {
    val phone = 1_700_000_000_000L
    val boardNow = phone + 60_000
    val model = model(LiveBoard(), nowMillis = { phone })
    model.reduce(
      LiveEvent(
        FLEET,
        "status",
        statusData(blocked(rfc3339(boardNow + 60_000)), now = rfc3339(boardNow))
      )
    )
    assertEquals("1:00", model.state.value.left)
  }

  @Test
  fun anUnpairedStreamStopsWithoutFallingBackToThePoll() = runBlocking {
    val board = LiveBoard()
    val bus = LiveBus(8)
    val model = model(board, bus)
    val job = launch(Dispatchers.Default) { model.run() }
    withTimeout(10_000) {
      while (!board.liveStarted) delay(5)
      while (!model.state.value.unpaired) {
        bus.link(FLEET, "unpaired", 0)
        delay(50)
      }
    }
    val reads = board.statusReads()
    delay(100)
    job.cancelAndJoin()
    assertTrue(model.state.value.unpaired)
    assertEquals(reads, board.statusReads())
    assertEquals(1, board.liveStopped)
  }
}
