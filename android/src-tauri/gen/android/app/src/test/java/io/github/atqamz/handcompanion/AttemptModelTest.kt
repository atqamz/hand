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

private fun turn(role: String = "worker", text: String = "Three places install luvus.", queued: Boolean = false) =
  """{"role":"$role","text":"$text","at":"2026-11-11T11:24:00Z"${if (queued) "," + """"queued":true""" else ""}}"""

private fun detail(
  state: String = "running",
  status: String = "running",
  brief: String = "t40: pin the luvus version in CI\n\nPin luvus in ci.yaml and install.sh.",
  note: String = "",
  turns: String = turn() + "," + turn("operator", "keep going", queued = true),
  more: Boolean = true,
  acked: Boolean = false
): String {
  val attempt =
    """{"ref":"a62","task":"t40","harness":"copilot","model":"gpt-5","effort":"high",""" +
      """"status":"$status","branch":"hand/t40-a62",""" +
      """"created_at":"2026-11-11T11:10:00Z"""" +
      (if (status == "running") "" else ""","ended_at":"2026-11-11T11:31:00Z"""") +
      "}"
  val task =
    """{"ref":"t40","title":"Pin the luvus version in CI","project":"hand","status":"active"}"""
  return """{"attempt":$attempt,"task":$task,"state":"$state",""" +
    """"brief":"$brief","model_note":"Asked for high effort; ran with medium.",""" +
    """"duration":"21m","reports":[${if (acked) report("r90", "2026-11-11T12:00:00Z", "phone (Pixel 8)") else report()}],""" +
    """"more_reports":true,"events":[{"seq":2,"at":"2026-11-11T11:31:00Z","kind":"attempt.reported","detail":"a62 reported r90","task":"t40"}],""" +
    """"more_events":true,"delivered":[{"ref":"plan-t40.md","kind":"file","href":"/doc/plan-t40.md"}],""" +
    """"transcript":{"shown":true,"more":$more,"note":"$note","model":"Asked for gpt-5, answered by gpt-5-mini.","answered":"gpt-5","turns":[$turns]}}"""
}

private fun answer(data: String, cursor: String = "c1") =
  Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"cursor":"$cursor","data":$data}""")

private fun read(data: String) =
  Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":$data}""")

private fun refused(status: Int, code: String, message: String) =
  Raw(status, """{"error":{"status":$status,"code":"$code","message":"$message"}}""")

private fun golden(name: String): String =
  checkNotNull(AttemptModelTest::class.java.classLoader?.getResource("api/$name.json")).readText()

private class AttemptBoard(
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
  private val attemptReads = java.util.concurrent.atomic.AtomicInteger()
  val calls = CopyOnWriteArrayList<String>()
  val posts = CopyOnWriteArrayList<List<String?>>()

  override fun request(fleet: String, post: Boolean, path: String, csrf: String?, body: String?): Raw {
    calls += "${if (post) "POST" else "GET"} $path"
    if (post) posts += listOf(path, body, csrf)
    return when {
      path.startsWith("/api/v1/attempt/") -> {
        val count = attemptReads.incrementAndGet()
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

private fun model(
  board: AttemptBoard,
  bus: LiveBus = LiveBus(8),
  pollMillis: Long = 5_000,
  fleet: String = FLEET,
  ref: String = "a62"
) = AttemptModel(fleet, ref, board, bus, pollMillis)

private fun reads(board: AttemptBoard) = board.calls.count { it.startsWith("GET /api/v1/attempt") }

private fun acks(board: AttemptBoard) = board.calls.count { it == "POST /api/v1/report/r90/ack" }

class AttemptModelTest {
  @Test
  fun oneReadOnOpenAndTheCursorOnTheNext() = runBlocking {
    val board = AttemptBoard(detailAnswer = { since -> if (since == null) answer(detail(), "c1") else Raw(304, "") })
    val attempt = model(board)
    attempt.load()
    assertEquals(1, reads(board))
    attempt.load()
    assertEquals(
      listOf("GET /api/v1/attempt/a62", "GET /api/v1/attempt/a62?since=c1"),
      board.calls.filter { it.startsWith("GET /api/v1/attempt") }
    )
    assertEquals("a62", attempt.state.value.detail?.attempt?.ref)
  }

  @Test
  fun aBadCursorIsDroppedAndTheReadRepeatsWithoutIt() = runBlocking {
    val board =
      AttemptBoard(
        detailAnswer = { since ->
          when (since) {
            null -> answer(detail(), "c1")
            "c1" -> refused(400, "bad_cursor", "bad cursor")
            else -> answer(detail(), "c2")
          }
        }
      )
    val attempt = model(board)
    attempt.load()
    attempt.load()
    assertEquals(
      listOf(
        "GET /api/v1/attempt/a62",
        "GET /api/v1/attempt/a62?since=c1",
        "GET /api/v1/attempt/a62"
      ),
      board.calls.filter { it.startsWith("GET /api/v1/attempt") }
    )
    assertEquals("a62", attempt.state.value.detail?.attempt?.ref)
  }

  @Test
  fun theDetailMapsEveryFieldAsTheBoardSendsIt() = runBlocking {
    val attempt = model(AttemptBoard())
    attempt.load()
    val detail = attempt.state.value.detail ?: error("no detail")
    assertEquals("copilot · gpt-5 · high", attemptTitle(detail.attempt))
    assertEquals("running", detail.attempt.status)
    assertEquals("running", detail.state)
    assertEquals("21m", detail.duration)
    assertEquals("hand/t40-a62", detail.attempt.branch)
    assertEquals("21m · branch hand/t40-a62", attemptFacts(detail))
    assertEquals("Asked for high effort; ran with medium.", detail.modelNote)
    assertTrue(detail.brief.startsWith("t40: pin the luvus version in CI"))
    assertEquals(listOf("worker", "operator"), detail.transcript.turns.map { it.role })
    assertEquals("Three places install luvus.", detail.transcript.turns.first().text)
    assertFalse(detail.transcript.turns.first().queued)
    assertTrue(detail.transcript.turns.last().queued)
    assertEquals("Asked for gpt-5, answered by gpt-5-mini.", detail.transcript.model)
    assertEquals("gpt-5", detail.transcript.answered)
    assertTrue(detail.transcript.shown)
    assertTrue(detail.transcript.more)
    assertEquals(listOf("r90"), detail.reports.map { it.ref })
    assertTrue(detail.moreReports)
    assertEquals("Older reports are not shown; run hand report list --attempt a62 --limit 500.", reportsMore("a62"))
    assertEquals(listOf(2L), detail.events.map { it.seq })
    assertTrue(detail.moreEvents)
    assertEquals(listOf("plan-t40.md"), detail.delivered.map { it.ref })
  }

  @Test
  fun anUnreadTranscriptCarriesTheBoardsNote() = runBlocking {
    val board =
      AttemptBoard(
        detailAnswer = {
          answer(detail(note = "The launch line has no session id, so there is no transcript to read.", turns = ""))
        }
      )
    val attempt = model(board)
    attempt.load()
    val transcript = attempt.state.value.detail?.transcript ?: error("no transcript")
    assertTrue(transcript.turns.isEmpty())
    assertEquals("The launch line has no session id, so there is no transcript to read.", transcriptEmpty(transcript))
  }

  @Test
  fun theFiveSecondReadsStopOnceTheAttemptFinishes() = runBlocking {
    var answers = 0
    val board =
      AttemptBoard(
        detailAnswer = {
          answers += 1
          answer(detail(state = if (answers <= 2) "running" else "idle", status = if (answers <= 2) "running" else "done"), "c$answers")
        }
      )
    val attempt = model(board, pollMillis = 30)
    val job = launch(Dispatchers.Default) { attempt.run() }
    withTimeout(5_000) { while (reads(board) < 3) delay(5) }
    delay(200)
    assertEquals(3, reads(board))
    assertTrue(job.isActive)
    job.cancelAndJoin()
  }

  @Test
  fun aFinishedAttemptIsNotReadAgainByTheClock() = runBlocking {
    val board = AttemptBoard(detailAnswer = { answer(detail(state = "idle", status = "done"), "c1") })
    val attempt = model(board, pollMillis = 20)
    val job = launch(Dispatchers.Default) { attempt.run() }
    withTimeout(5_000) { while (reads(board) < 1) delay(5) }
    delay(150)
    assertEquals(1, reads(board))
    job.cancelAndJoin()
  }

  @Test
  fun nothingIsReadWhenTheScreenIsNotCollected() = runBlocking {
    val board = AttemptBoard(detailAnswer = { answer(detail(), "c1") })
    val attempt = model(board, pollMillis = 20)
    val job = launch(Dispatchers.Default) { attempt.run() }
    withTimeout(5_000) { while (reads(board) < 1) delay(5) }
    job.cancelAndJoin()
    delay(100)
    assertEquals(1, reads(board))
  }

  @Test
  fun aRunningAttemptKeepsReadingWhileTheScreenShows() = runBlocking {
    val board = AttemptBoard(detailAnswer = { answer(detail(), "c1") })
    val attempt = model(board, pollMillis = 30)
    val job = launch(Dispatchers.Default) { attempt.run() }
    withTimeout(5_000) { while (reads(board) < 3) delay(5) }
    job.cancelAndJoin()
    assertTrue(reads(board) >= 3)
  }

  @Test
  fun aLaptopWithoutTheStreamKeepsTheFiveSecondRead() = runBlocking {
    val board = AttemptBoard(features = listOf("fleets"))
    val attempt = model(board, pollMillis = 20)
    val job = launch(Dispatchers.Default) { attempt.run() }
    withTimeout(5_000) { while (reads(board) < 3) delay(5) }
    job.cancelAndJoin()
    assertTrue(reads(board) >= 3)
    assertEquals("GET /api/v1/meta", board.calls[1])
  }

  @Test
  fun markReadPostsTheAckAndTheCardReadsReadBy() = runBlocking {
    val board = AttemptBoard()
    val attempt = model(board)
    attempt.load()
    assertNull(attempt.acknowledge("r90"))
    assertEquals(listOf("POST /api/v1/report/r90/ack"), board.calls.filter { it.startsWith("POST") })
    assertEquals("tok", board.posts.single()[2])
    val state = attempt.state.value
    assertTrue(state.marking.isEmpty())
    assertEquals("phone (Pixel 8)", state.detail?.reports?.single()?.ackedBy)
  }

  @Test
  fun aSecondMarkReadWhileTheFirstIsInFlightIsIgnored() = runBlocking {
    val gate = CompletableDeferred<Unit>()
    val board = AttemptBoard(gate = gate)
    val attempt = model(board)
    attempt.load()
    val first = launch(Dispatchers.IO) { attempt.acknowledge("r90") }
    withTimeout(5_000) { attempt.state.first { "r90" in it.marking } }
    assertNull(attempt.acknowledge("r90"))
    assertEquals(1, acks(board))
    gate.complete(Unit)
    first.join()
    assertEquals("phone (Pixel 8)", attempt.state.value.detail?.reports?.single()?.ackedBy)
    assertEquals(1, acks(board))
  }

  @Test
  fun aRefusedMarkReadKeepsTheButtonAndSaysSo() = runBlocking {
    val board = AttemptBoard(ackAnswer = { refused(409, "conflict", "already acknowledged") })
    val attempt = model(board)
    attempt.load()
    assertEquals("r90 is already read.", attempt.acknowledge("r90"))
    val state = attempt.state.value
    assertEquals("r90 is already read.", state.errors["r90"])
    assertTrue(state.marking.isEmpty())
    assertEquals("", state.detail?.reports?.single()?.ackedAt)
  }

  @Test
  fun aLostAckSaysItIsNotSureAndKeepsTheScreen() = runBlocking {
    val board = AttemptBoard(ackAnswer = { throw BoardError.Lost })
    val attempt = model(board)
    attempt.load()
    assertEquals("Not sure r90 was marked read.", attempt.acknowledge("r90"))
    val state = attempt.state.value
    assertEquals("Not sure r90 was marked read.", state.errors["r90"])
    assertEquals("a62", state.detail?.attempt?.ref)
    assertTrue(state.detail?.reports?.single()?.ackedAt == "")
  }

  @Test
  fun aMalformedAckSaysItIsNotSureAndKeepsTheButton() = runBlocking {
    val board = AttemptBoard(ackAnswer = { Raw(200, "not json") })
    val attempt = model(board)
    attempt.load()
    assertEquals("Not sure r90 was marked read.", attempt.acknowledge("r90"))
    assertEquals("Not sure r90 was marked read.", attempt.state.value.errors["r90"])
    assertTrue(attempt.state.value.detail?.reports?.single()?.ackedAt == "")
  }

  @Test
  fun aMalformedAckThatIsReadBackShowsReadBy() = runBlocking {
    var acked = false
    val board =
      AttemptBoard(
        detailAnswer = { answer(detail(acked = acked)) },
        ackAnswer = {
          acked = true
          Raw(200, "not json")
        }
      )
    val attempt = model(board)
    attempt.load()
    assertNull(attempt.acknowledge("r90"))
    val state = attempt.state.value
    assertNull(state.errors["r90"])
    assertEquals("phone (Pixel 8)", state.detail?.reports?.single()?.ackedBy)
  }

  @Test
  fun aFailedFirstReadShowsTheErrorAndRetryReadsAgain() = runBlocking {
    var broken = true
    val board = AttemptBoard(detailAnswer = { if (broken) refused(500, "internal", "boom") else answer(detail()) })
    val attempt = model(board)
    attempt.load()
    assertEquals(AttemptBody.Failed, attemptBody(attempt.state.value))
    broken = false
    attempt.load()
    assertEquals(AttemptBody.Content, attemptBody(attempt.state.value))
    assertNull(attempt.state.value.refused)
  }

  @Test
  fun aRefusedRefShowsTheBoardsWords() = runBlocking {
    val attempt = model(AttemptBoard(detailAnswer = { refused(404, "not_found", "not found: attempt a999") }))
    attempt.load()
    assertEquals("not found: attempt a999", attempt.state.value.refused)
    assertEquals(AttemptBody.Refused, attemptBody(attempt.state.value))
  }

  @Test
  fun aFailedRefreshKeepsWhatIsShown() = runBlocking {
    var broken = false
    val board = AttemptBoard(detailAnswer = { if (broken) refused(500, "internal", "boom") else answer(detail()) })
    val attempt = model(board)
    attempt.load()
    broken = true
    attempt.load()
    assertEquals("a62", attempt.state.value.detail?.attempt?.ref)
    assertEquals(AttemptBody.Content, attemptBody(attempt.state.value))
    assertEquals("The Board's answer could not be read.", attempt.state.value.notice)
  }

  @Test
  fun aHintRereadsOnlyThisAttempt() = runBlocking {
    val board = AttemptBoard()
    val attempt = model(board)
    attempt.load()
    attempt.reduce(LiveEvent(FLEET, "touched", """{"attempts":["a61"]}"""))
    assertEquals(1, reads(board))
    attempt.reduce(LiveEvent(FLEET, "touched", """{"reports":["r90"]}"""))
    assertEquals(1, reads(board))
    attempt.reduce(LiveEvent(FLEET, "touched", """{"attempts":["a62"]}"""))
    assertEquals(2, reads(board))
    attempt.reduce(LiveEvent(FLEET, "touched", """{"topics":["attempts"]}"""))
    assertEquals(3, reads(board))
    attempt.reduce(LiveEvent(FLEET, "touched", """{"topics":["status"]}"""))
    assertEquals(3, reads(board))
    attempt.reduce(LiveEvent(FLEET, "resync", """{"topics":["attempts"]}"""))
    assertEquals(4, reads(board))
    attempt.reduce(LiveEvent(FLEET, "resync", """{"topics":["status"]}"""))
    assertEquals(4, reads(board))
  }

  @Test
  fun theBriefOpensAndCloses() {
    val attempt = model(AttemptBoard())
    assertFalse(attempt.state.value.briefOpen)
    attempt.toggleBrief()
    assertTrue(attempt.state.value.briefOpen)
    attempt.toggleBrief()
    assertFalse(attempt.state.value.briefOpen)
  }

  @Test
  fun theRefRoutingTableKeepsBuiltScreensNativeAndTheRestOnTheBoard() {
    assertEquals(ThreadTarget.Task("t40"), refTarget("t40"))
    assertEquals(ThreadTarget.Decision("d12"), refTarget("d12"))
    assertEquals(ThreadTarget.Attempt("a62"), refTarget("a62"))
    assertEquals(ThreadTarget.Report("r90"), refTarget("r90"))
    assertEquals(ThreadTarget.None, refTarget("s1"))
    val report = TaskReport("r90", "t40", "a62", "progress", "s", "b", "2026-11-11T11:31:00Z")
    assertEquals(ThreadTarget.Report("r90"), reportTarget(report))
  }

  @Test
  fun theGoldenRunningAnswerDecodesEndToEnd() = runBlocking {
    val attempt =
      model(
        AttemptBoard(detailAnswer = { Raw(200, golden("attempt-running")) }),
        fleet = "f00000000e0aa",
        ref = "a1"
      )
    attempt.load()
    val detail = attempt.state.value.detail ?: error("no detail")
    assertEquals("a1", detail.attempt.ref)
    assertEquals("t1", detail.task.ref)
    assertEquals("claude · sonnet · low", attemptTitle(detail.attempt))
    assertEquals("running", detail.attempt.status)
    assertEquals("running", detail.state)
    assertEquals("hand/f00000000e0aa/t1-a1", detail.attempt.branch)
    assertEquals("20m", detail.duration)
    assertEquals("x", detail.brief)
    assertEquals(listOf("r1"), detail.reports.map { it.ref })
    assertEquals("https://github.com/example/hand/pull/42", detail.delivered.single().ref)
    assertEquals(listOf(8L, 9L, 10L), detail.events.map { it.seq })
    assertFalse(detail.transcript.shown)
  }

  @Test
  fun theGoldenFailedAnswerDecodesEndToEnd() = runBlocking {
    val attempt =
      model(
        AttemptBoard(detailAnswer = { Raw(200, golden("attempt-failed")) }),
        fleet = "f00000000e0aa",
        ref = "a2"
      )
    attempt.load()
    val detail = attempt.state.value.detail ?: error("no detail")
    assertEquals("a2", detail.attempt.ref)
    assertEquals("failed", detail.attempt.status)
    assertEquals("failing", detail.state)
    assertEquals("<1m", detail.duration)
    assertEquals("2026-11-01T04:40:00Z", detail.attempt.endedAt)
    assertTrue(detail.reports.isEmpty())
    assertEquals(listOf(14L, 15L), detail.events.map { it.seq })
    assertTrue(detail.delivered.isEmpty())
  }

  @Test
  fun aTransientFailureIsClearedByTheNextUnchangedRead() = runBlocking {
    var fail = false
    val board =
      AttemptBoard(
        detailAnswer = { since ->
          when {
            fail -> refused(500, "internal", "boom")
            since == null -> answer(detail(), "c1")
            else -> Raw(304, "")
          }
        }
      )
    val attempt = model(board)
    attempt.load()
    fail = true
    attempt.load()
    assertEquals("The Board's answer could not be read.", attempt.state.value.notice)
    fail = false
    attempt.load()
    assertNull(attempt.state.value.notice)
  }

  @Test
  fun shownNamesControlAndBidiCharacters() {
    assertEquals("aU+001B[31mbU+001B[0m", shown("a\u001B[31mb\u001B[0m"))
    assertEquals("U+202ExU+2066", shown("\u202Ex\u2066"))
    assertEquals("U+200Fleft", shown("\u200Fleft"))
    assertEquals("one\ntwo\tthree", shown("one\r\ntwo\tthree"))
  }

  @Test
  fun theBodyNamesLoadingFailedRefusedAndContent() {
    assertEquals(AttemptBody.Loading, attemptBody(AttemptState()))
    assertEquals(AttemptBody.Failed, attemptBody(AttemptState(loading = false, failed = true)))
    assertEquals(AttemptBody.Refused, attemptBody(AttemptState(loading = false, refused = "not found")))
    assertEquals(
      AttemptBody.Content,
      attemptBody(
        AttemptState(
          loading = false,
          detail =
            AttemptDetail(
              AttemptRecord("a62", "t40", "copilot", status = "running", createdAt = ""),
              TaskInfo("t40", "Title"),
              "running"
            )
        )
      )
    )
  }
}
