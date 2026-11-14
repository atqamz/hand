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
private const val GOLDEN = "f00000000e0aa"
private const val CURSOR = "ZjAwMDAwMDAwZTBhYQA3MTU5YWVhMzY0ZTEwODhi.jIQQeOWp_eELiQXM1Su2FA"

private fun golden(name: String): String =
  checkNotNull(ReportModelTest::class.java.classLoader?.getResource("api/$name.json")).readText()

private fun refused(status: Int, code: String, message: String) =
  Raw(status, """{"error":{"status":$status,"code":"$code","message":"$message"}}""")

private class ReportBoard(
  var detailAnswer: (String?) -> Raw = { Raw(200, golden("report")) },
  var ackAnswer: () -> Raw = { Raw(200, golden("report-acked")) },
  private val features: List<String> = listOf("events"),
  private val gate: CompletableDeferred<Unit>? = null,
  private val readGate: CompletableDeferred<Unit>? = null,
  private val gateAfter: Int = 0
) : Transport {
  private val reads = java.util.concurrent.atomic.AtomicInteger()
  val calls = CopyOnWriteArrayList<String>()
  val posts = CopyOnWriteArrayList<List<String?>>()

  override fun request(fleet: String, post: Boolean, path: String, csrf: String?, body: String?): Raw {
    calls += "${if (post) "POST" else "GET"} $path"
    if (post) posts += listOf(path, body, csrf)
    return when {
      path.startsWith("/api/v1/report/") && post -> {
        gate?.let { kotlinx.coroutines.runBlocking { it.await() } }
        ackAnswer()
      }
      path.startsWith("/api/v1/report/") -> {
        val count = reads.incrementAndGet()
        if (readGate != null && count > gateAfter) kotlinx.coroutines.runBlocking { readGate.await() }
        detailAnswer(path.substringAfter("since=", "").takeIf { "since=" in path })
      }
      path == "/api/v1/meta" ->
        Raw(
          200,
          """{"version":1,"data":{"features":${features.joinToString(",", "[", "]") { "\"$it\"" }}}}"""
        )
      path == "/api/v1/session" ->
        Raw(200, """{"version":1,"fleet":{"id":"$GOLDEN","name":"golden"},"data":{"csrf":"tok"}}""")
      else -> Raw(200, """{"version":1,"fleet":{"id":"$GOLDEN","name":"golden"},"data":{}}""")
    }
  }
}

private fun model(
  board: ReportBoard,
  bus: LiveBus = LiveBus(8),
  pollMillis: Long = 5_000,
  fleet: String = GOLDEN,
  ref: String = "r1"
) = ReportModel(fleet, ref, board, bus, pollMillis)

private fun reads(board: ReportBoard) = board.calls.count { it.startsWith("GET /api/v1/report") }

private fun reportReads(board: ReportBoard) =
  board.calls.filter { it.startsWith("GET /api/v1/report") }

class ReportModelTest {
  @Test
  fun oneReadOnOpenAndTheCursorOnTheNext() = runBlocking {
    val board = ReportBoard(detailAnswer = { since -> if (since == null) Raw(200, golden("report")) else Raw(304, "") })
    val report = model(board)
    report.load()
    assertEquals(1, reads(board))
    report.load()
    assertEquals(
      listOf("GET /api/v1/report/r1", "GET /api/v1/report/r1?since=$CURSOR"),
      reportReads(board).takeLast(2)
    )
    assertEquals("r1", report.state.value.detail?.report?.ref)
  }

  @Test
  fun theGoldenAnswerMapsEveryFieldItReads() = runBlocking {
    val report = model(ReportBoard())
    report.load()
    val detail = report.state.value.detail ?: error("no detail")
    assertEquals("r1", detail.report.ref)
    assertEquals("t1", detail.report.task)
    assertEquals("a1", detail.report.attempt)
    assertEquals("done", detail.report.status)
    assertEquals("Cookie fixed", detail.report.summary)
    assertTrue(detail.report.body.startsWith("Cookie fixed\nPR:"))
    assertEquals("PR: https://github.com/example/hand/pull/42\n\nAll tests pass.", reportRest(detail.report.body))
    assertEquals("", reportRest("Cookie fixed"))
    assertEquals("", reportRest(""))
    assertEquals("2026-11-01T04:40:00Z", detail.report.createdAt)
    assertEquals("", detail.report.ackedAt)
    assertEquals("t1", detail.task.ref)
    assertEquals("Fix login", detail.task.title)
    assertEquals("hand", detail.task.project)
    assertEquals("https://github.com/example/hand/pull/42", detail.delivered.single().ref)
    assertEquals("PR", detail.delivered.single().kind)
    assertEquals(ReportBody.Content, reportBody(report.state.value))
  }

  @Test
  fun theAckedGoldenAnswerCarriesReadBy() = runBlocking {
    val report =
      model(ReportBoard(detailAnswer = { Raw(200, golden("report-acked")) }))
    report.load()
    val taskReport = report.state.value.detail?.report ?: error("no report")
    assertEquals("operator (desktop)", taskReport.ackedBy)
    assertEquals("read by operator (desktop)", reportRead(taskReport))
  }

  @Test
  fun aRequiredFieldTheGoldenFileLacksIsAMalformedAnswer() = runBlocking {
    val broken = golden("report").replace("\"summary\": \"Cookie fixed\",", "")
    val report = model(ReportBoard(detailAnswer = { Raw(200, broken) }))
    report.load()
    assertEquals(ReportBody.Failed, reportBody(report.state.value))
    assertTrue(report.state.value.failed)
  }

  @Test
  fun aBadCursorIsDroppedAndTheReadRepeatsWithoutIt() = runBlocking {
    val board =
      ReportBoard(
        detailAnswer = { since ->
          when (since) {
            null -> Raw(200, golden("report"))
            CURSOR -> refused(400, "bad_cursor", "bad cursor")
            else -> Raw(200, golden("report"))
          }
        }
      )
    val report = model(board)
    report.load()
    report.load()
    assertEquals(
      listOf(
        "GET /api/v1/report/r1",
        "GET /api/v1/report/r1?since=$CURSOR",
        "GET /api/v1/report/r1"
      ),
      reportReads(board)
    )
    assertEquals("r1", report.state.value.detail?.report?.ref)
  }

  @Test
  fun anUnchangedReadKeepsWhatIsShown() = runBlocking {
    var first = true
    val board =
      ReportBoard(
        detailAnswer = {
          if (first) {
            first = false
            Raw(200, golden("report"))
          } else Raw(304, "")
        }
      )
    val report = model(board)
    report.load()
    report.load()
    assertEquals("Cookie fixed", report.state.value.detail?.report?.summary)
    assertFalse(report.state.value.loading)
  }

  @Test
  fun aTransientFailureIsClearedByTheNextUnchangedRead() = runBlocking {
    var fail = false
    val board =
      ReportBoard(
        detailAnswer = { since ->
          when {
            fail -> refused(500, "internal", "boom")
            since == null -> Raw(200, golden("report"))
            else -> Raw(304, "")
          }
        }
      )
    val report = model(board)
    report.load()
    fail = true
    report.load()
    assertEquals("The Board's answer could not be read.", report.state.value.notice)
    fail = false
    report.load()
    assertNull(report.state.value.notice)
  }

  @Test
  fun markReadPostsTheAckAndTheDockReadsReadBy() = runBlocking {
    val board = ReportBoard()
    val report = model(board)
    report.load()
    assertNull(report.acknowledge())
    assertEquals(listOf("POST /api/v1/report/r1/ack"), board.calls.filter { it.startsWith("POST") })
    assertEquals("tok", board.posts.single()[2])
    val state = report.state.value
    assertFalse(state.marking)
    assertEquals("operator (desktop)", state.detail?.report?.ackedBy)
    assertEquals("read by operator (desktop)", reportRead(state.detail!!.report))
  }

  @Test
  fun theReadAfterAnAckKeepsTheReadCursorAndAnswersUnchanged() = runBlocking {
    val board =
      ReportBoard(
        detailAnswer = { since ->
          when (since) {
            null -> Raw(200, golden("report"))
            CURSOR -> Raw(304, "")
            else -> Raw(200, golden("report"))
          }
        }
      )
    val report = model(board)
    report.load()
    assertNull(report.acknowledge())
    report.load()
    assertEquals(
      listOf("GET /api/v1/report/r1?since=$CURSOR"),
      reportReads(board).takeLast(1)
    )
    assertEquals("operator (desktop)", report.state.value.detail?.report?.ackedBy)
  }

  @Test
  fun aSecondMarkReadWhileTheFirstIsInFlightIsIgnored() = runBlocking {
    val gate = CompletableDeferred<Unit>()
    val board = ReportBoard(gate = gate)
    val report = model(board)
    report.load()
    val first = launch(Dispatchers.IO) { report.acknowledge() }
    withTimeout(5_000) { report.state.first { it.marking } }
    assertNull(report.acknowledge())
    assertEquals(1, board.calls.count { it == "POST /api/v1/report/r1/ack" })
    gate.complete(Unit)
    first.join()
    assertEquals("operator (desktop)", report.state.value.detail?.report?.ackedBy)
  }

  @Test
  fun aRefusedMarkReadKeepsTheButtonAndSaysSo() = runBlocking {
    val board = ReportBoard(ackAnswer = { refused(409, "conflict", "already acknowledged") })
    val report = model(board)
    report.load()
    assertEquals("r1 is already read.", report.acknowledge())
    val state = report.state.value
    assertEquals("r1 is already read.", state.error)
    assertFalse(state.marking)
    assertEquals("", state.detail?.report?.ackedAt)
  }

  @Test
  fun aLostAckSaysItIsNotSureAndKeepsTheScreen() = runBlocking {
    val board = ReportBoard(ackAnswer = { throw BoardError.Lost })
    val report = model(board)
    report.load()
    assertEquals("Not sure r1 was marked read.", report.acknowledge())
    assertEquals("Not sure r1 was marked read.", report.state.value.error)
    assertEquals("r1", report.state.value.detail?.report?.ref)
    assertEquals("", report.state.value.detail?.report?.ackedAt)
  }

  @Test
  fun aMalformedAckThatIsReadBackShowsReadBy() = runBlocking {
    var acked = false
    val board =
      ReportBoard(
        detailAnswer = { Raw(200, if (acked) golden("report-acked") else golden("report")) },
        ackAnswer = {
          acked = true
          Raw(200, "not json")
        }
      )
    val report = model(board)
    report.load()
    assertNull(report.acknowledge())
    val state = report.state.value
    assertNull(state.error)
    assertEquals("operator (desktop)", state.detail?.report?.ackedBy)
  }

  @Test
  fun aFailedFirstReadShowsTheErrorAndRetryReadsAgain() = runBlocking {
    var broken = true
    val board =
      ReportBoard(detailAnswer = { if (broken) refused(500, "internal", "boom") else Raw(200, golden("report")) })
    val report = model(board)
    report.load()
    assertEquals(ReportBody.Failed, reportBody(report.state.value))
    broken = false
    report.load()
    assertEquals(ReportBody.Content, reportBody(report.state.value))
    assertNull(report.state.value.refused)
  }

  @Test
  fun aRefusedRefShowsTheBoardsWords() = runBlocking {
    val report = model(ReportBoard(detailAnswer = { refused(404, "not_found", "not found: report r99") }))
    report.load()
    assertEquals("not found: report r99", report.state.value.refused)
    assertEquals(ReportBody.Refused, reportBody(report.state.value))
  }

  @Test
  fun aFailedRefreshKeepsWhatIsShown() = runBlocking {
    var broken = false
    val board =
      ReportBoard(detailAnswer = { if (broken) refused(500, "internal", "boom") else Raw(200, golden("report")) })
    val report = model(board)
    report.load()
    broken = true
    report.load()
    assertEquals("r1", report.state.value.detail?.report?.ref)
    assertEquals("The Board's answer could not be read.", report.state.value.notice)
  }

  @Test
  fun aHintRereadsOnlyThisReport() = runBlocking {
    val board = ReportBoard()
    val report = model(board)
    report.load()
    report.reduce(LiveEvent(FLEET, "touched", """{"reports":["r2"]}"""))
    assertEquals(1, reads(board))
    report.reduce(LiveEvent(FLEET, "touched", """{"attempts":["a1"]}"""))
    assertEquals(1, reads(board))
    report.reduce(LiveEvent(FLEET, "touched", """{"reports":["r1"]}"""))
    assertEquals(2, reads(board))
    report.reduce(LiveEvent(FLEET, "touched", """{"topics":["reports"]}"""))
    assertEquals(3, reads(board))
    report.reduce(LiveEvent(FLEET, "touched", """{"topics":["status"]}"""))
    assertEquals(3, reads(board))
    report.reduce(LiveEvent(FLEET, "resync", """{"topics":["reports"]}"""))
    assertEquals(4, reads(board))
    report.reduce(LiveEvent(FLEET, "resync", """{"topics":["status"]}"""))
    assertEquals(4, reads(board))
  }

  @Test
  fun aHintDuringAReadQueuesOneMoreRead() = runBlocking {
    val gate = CompletableDeferred<Unit>()
    val board = ReportBoard(readGate = gate)
    val report = model(board)
    val job = launch(Dispatchers.IO) { report.load() }
    withTimeout(5_000) { while (reads(board) < 1) delay(5) }
    report.reduce(LiveEvent(GOLDEN, "touched", """{"reports":["r1"]}"""))
    assertEquals(1, reads(board))
    gate.complete(Unit)
    job.join()
    withTimeout(5_000) { while (reads(board) < 2) delay(5) }
    delay(50)
    assertEquals(2, reads(board))
  }

  @Test
  fun theEventStreamIsFilteredToThisFleet() = runBlocking {
    val board = ReportBoard()
    val bus = LiveBus(8)
    val report = model(board, bus = bus, pollMillis = 20)
    val job = launch(Dispatchers.Default) { report.run() }
    withTimeout(5_000) { while (reads(board) < 1) delay(5) }
    delay(100)
    bus.publish(FLEET, "touched", """{"reports":["r1"]}""")
    delay(100)
    assertEquals(1, reads(board))
    bus.publish(GOLDEN, "touched", """{"reports":["r1"]}""")
    withTimeout(5_000) { while (reads(board) < 2) delay(5) }
    job.cancelAndJoin()
  }

  @Test
  fun aLaptopWithoutTheStreamPollsAndStopsWithTheScreen() = runBlocking {
    val board = ReportBoard(features = listOf("fleets"))
    val report = model(board, pollMillis = 20)
    val job = launch(Dispatchers.Default) { report.run() }
    withTimeout(5_000) { while (reads(board) < 3) delay(5) }
    job.cancelAndJoin()
    val count = reads(board)
    delay(100)
    assertEquals(count, reads(board))
    assertEquals("GET /api/v1/meta", board.calls[1])
  }

  @Test
  fun theRoutingTableKeepsEveryRefNativeAndPathsOnTheFallback() {
    assertEquals(ThreadTarget.Task("t1"), refTarget("t1"))
    assertEquals(ThreadTarget.Attempt("a1"), refTarget("a1"))
    assertEquals(ThreadTarget.Decision("d1"), refTarget("d1"))
    assertEquals(ThreadTarget.Report("r1"), refTarget("r1"))
    assertEquals(ThreadTarget.None, refTarget("s1"))
    assertEquals(ThreadTarget.Web("/$FLEET/doc/reports/a.md"), pathTarget("reports/a.md", FLEET))
    assertEquals(ThreadTarget.Web("/$FLEET/doc/reports/a%20b.md"), pathTarget("reports/a b.md", FLEET))
    assertEquals(ThreadTarget.Web("/$FLEET/doc/reports/a.md"), pathTarget("file:reports/a.md", FLEET))
    assertEquals(ThreadTarget.None, pathTarget("file://", FLEET))
    assertEquals(ThreadTarget.None, pathTarget("/reports/a.md", FLEET))
    assertEquals(ThreadTarget.None, pathTarget("~/reports/a.md", FLEET))
    assertEquals(ThreadTarget.None, pathTarget("C:\\reports\\a.md", FLEET))
    assertEquals(ThreadTarget.None, pathTarget("//host/reports/a.md", FLEET))
  }
}
