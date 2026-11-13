package io.github.atqamz.handcompanion

import java.util.concurrent.CopyOnWriteArrayList
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private const val FLEET = "f000000000003"

private fun report(ref: String = "r90", acked: String = "", by: String = "") =
  """{"ref":"$ref","task":"t40","attempt":"a62","status":"progress",""" +
    """"summary":"Three places install luvus","body":"Only ci.yaml floats.",""" +
    """"created_at":"2026-11-11T11:31:00Z"""" +
    (if (acked.isEmpty()) "" else ""","acked_at":"$acked","acked_by":"$by"""") +
    "}"

private fun decision(ref: String = "d12", answer: String = "") =
  """{"ref":"$ref","task":"t40","status":"${if (answer.isEmpty()) "open" else "answered"}",""" +
    """"pill":"${if (answer.isEmpty()) "waiting" else "passing"}",""" +
    """"headline":"Pin luvus to which version?","question":"Pin it?",""" +
    (if (answer.isEmpty()) "" else """"answer":"$answer","answered_by":"phone (Pixel 8)",""") +
    """"created_at":"2026-11-11T11:40:00Z"}"""

private fun attempt(ref: String = "a62", state: String = "running", status: String = "running") =
  """{"ref":"$ref","task":"t40","harness":"codex","model":"gpt-5","effort":"high",""" +
    """"status":"$status","state":"$state","branch":"hand/a62","created_at":"2026-11-11T11:10:00Z"}"""

private fun event(seq: Int, kind: String, detail: String) =
  """{"seq":$seq,"at":"2026-11-11T11:31:00Z","kind":"$kind","detail":"$detail","task":"t40"}"""

private fun detail(
  parent: String = "",
  goal: String = "",
  open: Boolean = true,
  acked: Boolean = false,
  children: Boolean = false,
  plan: Boolean = true
): String {
  val task =
    """{"ref":"t40","title":"Pin the luvus version in CI",""" +
      (if (goal.isEmpty()) "" else """"goal":"$goal",""") +
      """"project":"hand","status":"active","created_at":"2026-11-11T09:00:00Z",""" +
      """"updated_at":"2026-11-11T11:52:00Z"""" +
      (if (parent.isEmpty()) "" else ""","parent":"$parent"""") +
      "}"
  val planJson =
    if (plan)
      """"plan":{"ref":"p2","revision":2,"body":"Pin luvus in ci.yaml and fail when the pins differ.","created_at":"2026-11-11T10:00:00Z"},"""
    else ""
  val kids =
    if (children)
      """"children":[{"ref":"t42","title":"Bisect the arm64 failure","state":"idle","status":"inbox","parent":"t40"}]"""
    else ""
  return """{"task":$task,"pill":"running",""" +
    planJson +
    """"delivered":[{"ref":"https://github.com/atqamz/hand/pull/1241","kind":"PR"},""" +
    """{"ref":"plan-t40.md","kind":"file","href":"/doc/plan-t40.md"}],""" +
    """"open_decisions":[${if (open) decision() else ""}],""" +
    """"decisions":[${decision(answer = "2. Track 0.14.x")}],""" +
    """"unread":[${if (open && !acked) report() else ""}],""" +
    """"reports":[${if (acked) report("r90", "2026-11-11T12:00:00Z", "phone (Pixel 8)") else report()}],""" +
    """"more_reports":false,"attempts":[${attempt()},${attempt("a60", "failing", "failed")}],""" +
    """"events":[${event(1, "attempt.reported", "a62 reported r90")},""" +
    """${event(2, "decision.asked", "d12 asked")}],""" +
    """"more_events":true""" +
    (if (kids.isEmpty()) "" else ",$kids") +
    "}"
}

private fun answer(data: String, cursor: String = "c1") =
  Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"cursor":"$cursor","data":$data}""")

private fun read(answer: String) =
  Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":$answer}""")

private fun refused(status: Int, code: String, message: String) =
  Raw(status, """{"error":{"status":$status,"code":"$code","message":"$message"}}""")

private class TaskBoard(
  var detailAnswer: (String?) -> Raw = { answer(detail()) },
  var ackAnswer: () -> Raw = {
    read(
      """{"report":${report("r90", "2026-11-11T12:00:00Z", "phone (Pixel 8)")},""" +
        """"task":{"ref":"t40","title":"Pin the luvus version in CI"}}"""
    )
  },
  private val features: List<String> = listOf("events"),
  private val gate: CompletableDeferred<Unit>? = null,
  private val readGate: CompletableDeferred<Unit>? = null,
  private val gateAfter: Int = 0
) : Transport {
  private val taskReads = java.util.concurrent.atomic.AtomicInteger()
  val calls = CopyOnWriteArrayList<String>()
  val posts = CopyOnWriteArrayList<List<String?>>()

  override fun request(fleet: String, post: Boolean, path: String, csrf: String?, body: String?): Raw {
    calls += "${if (post) "POST" else "GET"} $path"
    if (post) posts += listOf(path, body, csrf)
    return when {
      path.startsWith("/api/v1/task/") -> {
        val count = taskReads.incrementAndGet()
        if (readGate != null && count > gateAfter) kotlinx.coroutines.runBlocking { readGate.await() }
        detailAnswer(path.substringAfter("since=", "").takeIf { "since=" in path })
      }
      path == "/api/v1/meta" ->
        Raw(
          200,
          """{"version":1,"data":{"features":${features.joinToString(",", "[", "]") { "\"$it\"" }}}}"""
        )
      path == "/api/v1/session" ->
        Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":{"csrf":"tok"}}""")
      path.startsWith("/api/v1/report/") -> {
        gate?.let { kotlinx.coroutines.runBlocking { it.await() } }
        ackAnswer()
      }
      else -> Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":{}}""")
    }
  }
}

private fun model(board: TaskBoard, bus: LiveBus = LiveBus(8), pollMillis: Long = 5_000) =
  TaskModel(FLEET, "t40", board, bus, pollMillis)

private fun reads(board: TaskBoard) = board.calls.count { it.startsWith("GET /api/v1/task") }

private fun acks(board: TaskBoard) = board.calls.count { it == "POST /api/v1/report/r90/ack" }

class TaskModelTest {
  @Test
  fun oneReadOnOpenAndTheCursorOnTheNext() = runBlocking {
    val board = TaskBoard(detailAnswer = { since -> if (since == null) answer(detail(), "c1") else Raw(304, "") })
    val task = model(board)
    task.load()
    assertEquals(1, reads(board))
    task.load()
    assertEquals(
      listOf("GET /api/v1/task/t40", "GET /api/v1/task/t40?since=c1"),
      board.calls.filter { it.startsWith("GET /api/v1/task") }
    )
    assertEquals("t40", task.state.value.detail?.task?.ref)
  }

  @Test
  fun aBadCursorIsDroppedAndTheReadRepeatsWithoutIt() = runBlocking {
    val board =
      TaskBoard(
        detailAnswer = { since ->
          when (since) {
            null -> answer(detail(), "c1")
            "c1" -> refused(400, "bad_cursor", "bad cursor")
            else -> answer(detail(), "c2")
          }
        }
      )
    val task = model(board)
    task.load()
    task.load()
    assertEquals(
      listOf(
        "GET /api/v1/task/t40",
        "GET /api/v1/task/t40?since=c1",
        "GET /api/v1/task/t40"
      ),
      board.calls.filter { it.startsWith("GET /api/v1/task") }
    )
  }

  @Test
  fun theDetailMapsEverySectionAndAttemptState() = runBlocking {
    val task = model(TaskBoard(detailAnswer = { answer(detail(parent = "t39", goal = "One pin.", children = true)) }))
    task.load()
    val detail = task.state.value.detail ?: error("no detail")
    assertEquals("t39", detail.task.parent)
    assertEquals("One pin.", detail.task.goal)
    assertEquals(2, detail.delivered.size)
    assertEquals("PR", detail.delivered.first().kind)
    assertEquals("/doc/plan-t40.md", detail.delivered.last().href)
    assertEquals(2, detail.plan?.revision)
    assertEquals(listOf("d12"), detail.openDecisions.map { it.ref })
    assertEquals(listOf("r90"), detail.unread.map { it.ref })
    assertEquals(listOf("r90"), detail.reports.map { it.ref })
    assertEquals("2. Track 0.14.x", detail.decisions.single().answer)
    assertEquals("running", detail.attempts.first().state)
    assertEquals("failing", detail.attempts.last().state)
    assertEquals(listOf(1L, 2L), detail.events.map { it.seq })
    assertEquals(listOf("t42"), detail.children.map { it.ref })
    assertEquals("t40", detail.children.single().parent)
    assertTrue(detail.moreEvents)
  }

  @Test
  fun aSectionTheAnswerLeavesEmptyStaysAbsent() = runBlocking {
    val task = model(TaskBoard(detailAnswer = { answer(detail(open = false, plan = false, children = false)) }))
    task.load()
    val detail = task.state.value.detail ?: error("no detail")
    assertNull(detail.plan)
    assertTrue(detail.openDecisions.isEmpty())
    assertTrue(detail.unread.isEmpty())
    assertTrue(detail.children.isEmpty())
    assertFalse(detail.moreReports)
  }

  @Test
  fun markReadPostsTheAckAndTheCardReadsReadBy() = runBlocking {
    val board = TaskBoard()
    val task = model(board)
    task.load()
    assertNull(task.acknowledge("r90"))
    assertEquals(listOf("POST /api/v1/report/r90/ack"), board.calls.filter { it.startsWith("POST") })
    assertEquals("tok", board.posts.single()[2])
    val state = task.state.value
    assertTrue(state.marking.isEmpty())
    assertEquals("phone (Pixel 8)", state.detail?.reports?.single()?.ackedBy)
    assertTrue(state.detail?.unread?.isEmpty() == true)
  }

  @Test
  fun aSecondMarkReadWhileTheFirstIsInFlightIsIgnored() = runBlocking {
    val gate = CompletableDeferred<Unit>()
    val board = TaskBoard(gate = gate)
    val task = model(board)
    task.load()
    val first = launch(Dispatchers.IO) { task.acknowledge("r90") }
    withTimeout(5_000) { task.state.first { "r90" in it.marking } }
    assertNull(task.acknowledge("r90"))
    assertEquals(1, acks(board))
    gate.complete(Unit)
    first.join()
    assertEquals("phone (Pixel 8)", task.state.value.detail?.reports?.single()?.ackedBy)
    assertEquals(1, acks(board))
  }

  @Test
  fun aRefusedMarkReadKeepsTheButtonAndSaysSo() = runBlocking {
    val board = TaskBoard(ackAnswer = { refused(409, "conflict", "already acknowledged") })
    val task = model(board)
    task.load()
    assertEquals("r90 is already read.", task.acknowledge("r90"))
    val state = task.state.value
    assertEquals("r90 is already read.", state.errors["r90"])
    assertTrue(state.marking.isEmpty())
    assertEquals("", state.detail?.reports?.single()?.ackedAt)
    assertEquals(1, state.detail?.unread?.size)
  }

  @Test
  fun aLostAckSaysItIsNotSureAndKeepsTheScreen() = runBlocking {
    val board = TaskBoard(ackAnswer = { throw BoardError.Lost })
    val task = model(board)
    task.load()
    assertEquals("Not sure r90 was marked read.", task.acknowledge("r90"))
    val state = task.state.value
    assertEquals("Not sure r90 was marked read.", state.errors["r90"])
    assertEquals("t40", state.detail?.task?.ref)
    assertTrue(state.detail?.reports?.single()?.ackedAt == "")
  }

  @Test
  fun aFailedFirstReadShowsTheErrorAndRetryReadsAgain() = runBlocking {
    var broken = true
    val board =
      TaskBoard(
        detailAnswer = {
          if (broken) refused(500, "internal", "boom") else answer(detail())
        }
      )
    val task = model(board)
    task.load()
    assertEquals(TaskBody.Failed, taskBody(task.state.value))
    broken = false
    task.load()
    assertEquals(TaskBody.Content, taskBody(task.state.value))
    assertNull(task.state.value.refused)
  }

  @Test
  fun aRefusedRefShowsTheBoardsWords() = runBlocking {
    val task = model(TaskBoard(detailAnswer = { refused(404, "not_found", "not found: task t999") }))
    task.load()
    assertEquals("not found: task t999", task.state.value.refused)
    assertEquals(TaskBody.Refused, taskBody(task.state.value))
  }

  @Test
  fun aFailedRefreshKeepsWhatIsShown() = runBlocking {
    var broken = false
    val board =
      TaskBoard(
        detailAnswer = {
          if (broken) refused(500, "internal", "boom") else answer(detail())
        }
      )
    val task = model(board)
    task.load()
    broken = true
    task.load()
    assertEquals("t40", task.state.value.detail?.task?.ref)
    assertEquals(TaskBody.Content, taskBody(task.state.value))
    assertEquals("The Board's answer could not be read.", task.state.value.notice)
  }

  @Test
  fun aHintRereadsOnlyWhatThisTaskShows() = runBlocking {
    val board = TaskBoard()
    val task = model(board)
    task.load()
    task.reduce(LiveEvent(FLEET, "touched", """{"topics":["tasks"],"tasks":["t41"]}"""))
    assertEquals(1, reads(board))
    task.reduce(LiveEvent(FLEET, "touched", """{"reports":["r99"]}"""))
    assertEquals(1, reads(board))
    task.reduce(LiveEvent(FLEET, "touched", """{"reports":["r90"]}"""))
    assertEquals(2, reads(board))
    task.reduce(LiveEvent(FLEET, "touched", """{"decisions":["d12"]}"""))
    assertEquals(3, reads(board))
    task.reduce(LiveEvent(FLEET, "touched", """{"attempts":["a62"]}"""))
    assertEquals(4, reads(board))
    task.reduce(LiveEvent(FLEET, "touched", """{"topics":["tasks"],"tasks":["t40"]}"""))
    assertEquals(5, reads(board))
    task.reduce(LiveEvent(FLEET, "touched", """{"topics":["tasks"]}"""))
    assertEquals(6, reads(board))
    task.reduce(LiveEvent(FLEET, "touched", """{"topics":["status"]}"""))
    assertEquals(6, reads(board))
    val childBoard = TaskBoard(detailAnswer = { answer(detail(children = true)) })
    val child = model(childBoard)
    child.load()
    child.reduce(LiveEvent(FLEET, "touched", """{"tasks":["t42"]}"""))
    assertEquals(2, reads(childBoard))
  }

  @Test
  fun aHintDuringAReadIsRepeatedWhenItFinishes() = runBlocking {
    val gate = CompletableDeferred<Unit>()
    val board = TaskBoard(readGate = gate, gateAfter = 1)
    val task = model(board)
    task.load()
    val first =
      launch(Dispatchers.IO) {
        task.reduce(LiveEvent(FLEET, "touched", """{"topics":["tasks"],"tasks":["t40"]}"""))
      }
    withTimeout(5_000) { while (reads(board) < 2) delay(5) }
    val second =
      launch(Dispatchers.IO) {
        task.reduce(LiveEvent(FLEET, "touched", """{"topics":["tasks"],"tasks":["t40"]}"""))
      }
    second.join()
    gate.complete(Unit)
    first.join()
    assertEquals(3, reads(board))
  }

  @Test
  fun aMalformedAckSaysItIsNotSureAndKeepsTheButton() = runBlocking {
    val board = TaskBoard(ackAnswer = { Raw(200, "not json") })
    val task = model(board)
    task.load()
    assertEquals("Not sure r90 was marked read.", task.acknowledge("r90"))
    val state = task.state.value
    assertEquals("Not sure r90 was marked read.", state.errors["r90"])
    assertEquals("", state.detail?.reports?.single()?.ackedAt)
    assertTrue(state.detail?.unread?.isNotEmpty() == true)
    assertEquals(2, reads(board))
  }

  @Test
  fun aMalformedAckThatIsReadBackShowsReadBy() = runBlocking {
    var acked = false
    val board =
      TaskBoard(
        detailAnswer = { answer(if (acked) detail(acked = true) else detail()) },
        ackAnswer = {
          acked = true
          Raw(200, "not json")
        }
      )
    val task = model(board)
    task.load()
    assertNull(task.acknowledge("r90"))
    val state = task.state.value
    assertNull(state.errors["r90"])
    assertEquals("phone (Pixel 8)", state.detail?.reports?.single()?.ackedBy)
    assertTrue(state.detail?.unread?.isEmpty() == true)
  }

  @Test
  fun nothingIsReadWhenTheScreenIsNotCollected() = runBlocking {
    val board = TaskBoard()
    val bus = LiveBus(8)
    val task = model(board, bus)
    val job = launch(Dispatchers.Default) { task.run() }
    withTimeout(5_000) { while (reads(board) < 1) delay(5) }
    job.cancelAndJoin()
    bus.publish(FLEET, "touched", """{"topics":["tasks"],"tasks":["t40"]}""")
    delay(100)
    assertEquals(1, reads(board))
    val again = launch(Dispatchers.Default) { task.run() }
    withTimeout(5_000) { while (reads(board) < 2) delay(5) }
    delay(100)
    bus.publish(FLEET, "touched", """{"topics":["tasks"],"tasks":["t40"]}""")
    withTimeout(5_000) { while (reads(board) < 3) delay(5) }
    again.cancelAndJoin()
    assertEquals(3, reads(board))
  }

  @Test
  fun aLaptopWithoutTheStreamPollsTheDetail() = runBlocking {
    val board = TaskBoard(features = listOf("fleets"), detailAnswer = { answer(detail()) })
    val task = model(board, pollMillis = 20)
    val job = launch(Dispatchers.Default) { task.run() }
    withTimeout(5_000) { while (reads(board) < 3) delay(5) }
    job.cancelAndJoin()
    assertTrue(reads(board) >= 3)
    assertEquals("GET /api/v1/meta", board.calls[1])
  }

  @Test
  fun theRefRoutingTableKeepsBuiltScreensNativeAndTheRestOnTheBoard() {
    assertEquals(ThreadTarget.Task("t40"), refTarget("t40", "t40", FLEET))
    assertEquals(ThreadTarget.Decision("d12"), refTarget("d12", "t40", FLEET))
    assertEquals(ThreadTarget.Attempt("a62"), refTarget("a62", "t40", FLEET))
    assertEquals(ThreadTarget.Web("/$FLEET/task/t40#r90"), refTarget("r90", "t40", FLEET))
    assertEquals(ThreadTarget.None, refTarget("s1", "t40", FLEET))
    val report = TaskReport("r90", "t40", "a62", "progress", "s", "b", "2026-11-11T11:31:00Z")
    assertEquals(ThreadTarget.Web("/$FLEET/task/t40#r90"), reportTarget(report, "t39", FLEET))
  }

  @Test
  fun theDeliveredKindsChooseTheBrowserOrTheWebBoard() {
    assertEquals(
      ThreadTarget.Browser("https://github.com/atqamz/hand/pull/1241"),
      deliverableTarget(TaskDeliverable("https://github.com/atqamz/hand/pull/1241", "PR"), FLEET)
    )
    assertEquals(
      ThreadTarget.Browser("https://example.com/notes"),
      deliverableTarget(TaskDeliverable("https://example.com/notes", "link"), FLEET)
    )
    assertEquals(
      ThreadTarget.Web("/$FLEET/doc/plan-t40.md"),
      deliverableTarget(TaskDeliverable("plan-t40.md", "file", "/doc/plan-t40.md"), FLEET)
    )
    assertEquals(ThreadTarget.None, deliverableTarget(TaskDeliverable("plan-t40.md", "file"), FLEET))
  }

  @Test
  fun theTaskPathIsAStack() {
    assertEquals("t40", pushTask("", "t40"))
    assertEquals("t40\nt42", pushTask("t40", "t42"))
    assertEquals("t40", popTask("t40\nt42"))
    assertEquals("", popTask("t40"))
    assertEquals("t42", currentTask("t40\nt42"))
    assertNull(currentTask(""))
  }

  @Test
  fun theBodyNamesLoadingFailedRefusedAndContent() {
    assertEquals(TaskBody.Loading, taskBody(TaskState()))
    assertEquals(TaskBody.Failed, taskBody(TaskState(loading = false, failed = true)))
    assertEquals(TaskBody.Refused, taskBody(TaskState(loading = false, refused = "not found")))
    assertEquals(
      TaskBody.Content,
      taskBody(TaskState(loading = false, detail = TaskDetail(TaskInfo("t40", "Title"), "running")))
    )
  }
}
