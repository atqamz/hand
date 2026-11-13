package io.github.atqamz.handcompanion

import java.util.concurrent.Executors
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

private const val FLEET = "f00000000e0aa"

private data class Call(
  val fleet: String,
  val post: Boolean,
  val path: String,
  val csrf: String?,
  val body: String?
)

private class Script(private vararg val answers: Any) : Transport {
  val calls = mutableListOf<Call>()
  val threads = mutableListOf<String>()

  override fun request(
    fleet: String,
    post: Boolean,
    path: String,
    csrf: String?,
    body: String?
  ): Raw {
    calls += Call(fleet, post, path, csrf, body)
    threads += Thread.currentThread().name
    val answer = answers[calls.size - 1]
    if (answer is BoardError) throw answer
    return answer as Raw
  }
}

private fun golden(name: String): String =
  checkNotNull(Script::class.java.classLoader?.getResource("api/$name.json")).readText()

private fun envelope(cursor: String, data: String = status("working")): Raw =
  Raw(
    200,
    """{"version":1,"fleet":{"id":"$FLEET","name":"golden"},"cursor":"$cursor","data":$data}"""
  )

private fun status(pill: String) =
  """{"agent_state":"working","composer_hint":"","pill":"$pill","line":"working",""" +
    """"gauge":{"text":"","level":"","pct":0},"compactions":0,""" +
    """"needs":{"waiting":0,"more":0,"worst_count":0,"items":[]},""" +
    """"tasks":{"active":0,"inbox":0,"done":0,"abandoned":0},"projects":1}"""

private fun refusal(status: Int, code: String) =
  Raw(status, """{"error":{"status":$status,"code":"$code","message":"said $code"}}""")

private fun client(script: Transport) = BoardClient(FLEET, script, Dispatchers.Unconfined)

private fun <T> poll(poll: Poll<T>): Envelope<T> = (poll as Poll.Changed<T>).envelope

class BoardClientTest {
  @Test
  fun anEnvelopeCarriesTheFleetTheCursorAndTheData() {
    val script = Script(Raw(200, golden("status")))
    val answer = poll(runBlocking { client(script).status() })
    assertEquals(1, answer.version)
    assertEquals("f00000000e0aa", answer.fleet.id)
    assertEquals("golden", answer.fleet.name)
    assertEquals("2026-11-01T05:00:00Z", answer.generatedAt)
    assertEquals("ZjAwMDAwMDAwZTBhYQBlNjVkMTJjNDg2OGFmNWFi.M0tCmysObZAjlWlqmeF33A", answer.cursor)
    assertEquals("working", answer.data.pill)
  }

  @Test
  fun aRequestNamesItsFleetAndTheStatusPath() {
    val script = Script(envelope("c1"))
    runBlocking { client(script).status() }
    assertEquals(Call(FLEET, false, "/api/v1/status", null, null), script.calls.single())
  }

  @Test
  fun anotherVersionIsRefusedBeforeItsDataIsRead() {
    val raw = Raw(200, """{"version":2,"fleet":{"id":"$FLEET","name":"x"},"data":{"new":"shape"}}""")
    val error = failure { client(Script(raw)).status() }
    assertEquals(2, (error as BoardError.UnsupportedVersion).version)
  }

  @Test
  fun aBodyThatIsNotAnEnvelopeIsMalformed() {
    val bodies =
      listOf(
        "<html>",
        "[]",
        """{"fleet":{}}""",
        """{"version":1,"data":{}}""",
        """{"version":"one","fleet":{"id":"$FLEET","name":"x"},"data":{}}""",
        """{"version":1,"fleet":{"id":"other","name":"x"},"data":{}}""",
        """{"version":1,"fleet":"$FLEET","data":{}}"""
      )
    for (body in bodies) {
      assertSame(body, BoardError.Malformed, failure { client(Script(Raw(200, body))).status() })
    }
  }

  @Test
  fun everyApiErrorCodeHasItsOwnType() {
    val cases =
      listOf(
        400 to ("bad_request" to BoardError.BadRequest::class),
        400 to ("bad_cursor" to BoardError.BadCursor::class),
        401 to ("unauthorized" to BoardError.Unauthorized::class),
        403 to ("forbidden" to BoardError.Forbidden::class),
        403 to ("csrf_required" to BoardError.CsrfRequired::class),
        404 to ("not_found" to BoardError.NotFound::class),
        405 to ("method_not_allowed" to BoardError.MethodNotAllowed::class),
        409 to ("conflict" to BoardError.Conflict::class),
        500 to ("internal" to BoardError.Internal::class),
        507 to ("no_space" to BoardError.NoSpace::class)
      )
    for ((status, pair) in cases) {
      val (code, type) = pair
      val error = failure { client(Script(refusal(status, code))).status() }
      assertEquals(code, type, error::class)
      assertEquals("said $code", error.message)
    }
  }

  @Test
  fun anErrorIsToldByItsCodeAndNeverByItsStatus() {
    val error = failure { client(Script(refusal(404, "conflict"))).status() }
    assertTrue(error is BoardError.Conflict)
  }

  @Test
  fun anUnknownCodeKeepsItsStatusAndCode() {
    val error = failure { client(Script(refusal(418, "teapot"))).status() } as BoardError.Other
    assertEquals(418, error.status)
    assertEquals("teapot", error.code)
  }

  @Test
  fun anErrorWithNoEnvelopeKeepsItsStatus() {
    val error = failure { client(Script(Raw(502, "<html>bad gateway"))).status() }
    assertEquals(502, (error as BoardError.Other).status)
    assertNull(error.code)
  }

  @Test
  fun theCursorGoesBackAsSinceAndANotModifiedKeepsIt() {
    val script =
      Script(
        envelope("c1"),
        Raw(304, ""),
        Raw(304, ""),
        envelope("c2", status("failing")),
        Raw(304, "")
      )
    val client = client(script)
    runBlocking {
      assertEquals("c1", poll(client.status()).cursor)
      assertSame(Poll.Unchanged, client.status())
      assertSame(Poll.Unchanged, client.status())
      assertEquals("failing", poll(client.status()).data.pill)
      assertSame(Poll.Unchanged, client.status())
    }
    assertEquals(
      listOf(
        "/api/v1/status",
        "/api/v1/status?since=c1",
        "/api/v1/status?since=c1",
        "/api/v1/status?since=c1",
        "/api/v1/status?since=c2"
      ),
      script.calls.map { it.path }
    )
  }

  @Test
  fun aCursorIsEncodedInTheQuery() {
    val script = Script(envelope("a b&c=d"), Raw(304, ""))
    val client = client(script)
    runBlocking {
      client.status()
      client.status()
    }
    assertEquals("/api/v1/status?since=a+b%26c%3Dd", script.calls[1].path)
  }

  @Test
  fun aPollThatCouldNotReachTheBoardKeepsTheCursor() {
    val failures =
      listOf(
        BoardError.Busy,
        BoardError.Unreachable,
        BoardError.Lost,
        BoardError.TimedOut,
        BoardError.BoardNotRunning
      )
    for (failure in failures) {
      val script = Script(envelope("c1"), failure, Raw(304, ""))
      val client = client(script)
      runBlocking {
        client.status()
        assertSame(failure, failure { client.status() })
        client.status()
      }
      assertEquals("$failure", "/api/v1/status?since=c1", script.calls[2].path)
    }
  }

  @Test
  fun aRefusedCursorIsDroppedSoThePollRecovers() {
    val script = Script(envelope("c1"), refusal(400, "bad_cursor"), envelope("c2"), Raw(304, ""))
    val client = client(script)
    runBlocking {
      client.status()
      assertTrue(failure { client.status() } is BoardError.BadCursor)
      assertEquals("c2", poll(client.status()).cursor)
      client.status()
    }
    assertEquals(
      listOf(
        "/api/v1/status",
        "/api/v1/status?since=c1",
        "/api/v1/status",
        "/api/v1/status?since=c2"
      ),
      script.calls.map { it.path }
    )
  }

  @Test
  fun aWriteNamedWithALeadingSlashStaysUnderTheApiPrefix() {
    val script = Script(envelope("s", """{"csrf":"tok"}"""), envelope("w"))
    runBlocking { client(script).write("/message", "{}", Status.serializer()) }
    assertEquals("/api/v1/message", script.calls[1].path)
  }

  @Test
  fun aWriteTheBoardAcceptedButCouldNotReadBackIsNotAFailure() {
    val session = envelope("s", """{"csrf":"tok"}""")
    val script = Script(session, envelope("w", """{"accepted":true}"""))
    val result = runBlocking { client(script).write("message", """{"text":"hi"}""", Status.serializer()) }
    assertSame(Write.Accepted, result)
    assertEquals(2, script.calls.size)
  }

  @Test
  fun aWriteTheBoardAnsweredWithTheUpdatedObjectIsUpdated() {
    val script = Script(envelope("s", """{"csrf":"tok"}"""), envelope("w", status("failing")))
    val result = runBlocking { client(script).write("message", "{}", Status.serializer()) }
    assertEquals("failing", (result as Write.Updated).envelope.data.pill)
  }

  @Test
  fun aWriteThatWasAppliedButCannotBeReadIsNeverReportedAsNotSent() {
    val answers =
      listOf(
        envelope("w", """{"unexpected":"shape"}"""),
        envelope("w", "[]"),
        Raw(200, "<html>"),
        Raw(200, """{"version":2,"fleet":{"id":"$FLEET","name":"x"},"data":{}}"""),
        Raw(200, """{"version":1,"fleet":{"id":"other","name":"x"},"data":{"accepted":true}}""")
      )
    for (answer in answers) {
      val script = Script(envelope("s", """{"csrf":"tok"}"""), answer)
      val error = failure { client(script).write("message", "{}", Status.serializer()) }
      assertTrue("${(answer).body}", error is BoardError.AppliedUnread)
      assertNotNull(error.cause)
      assertEquals(2, script.calls.size)
    }
  }

  @Test
  fun aWriteReadsTheCsrfTokenOnceAndSendsItWithItsBody() {
    val session = envelope("s", """{"csrf":"tok"}""")
    val script = Script(session, envelope("w1"), envelope("w2"))
    val client = client(script)
    runBlocking {
      client.write("message", """{"text":"hi"}""", Status.serializer())
      client.write("supervisor/start", null, Status.serializer())
    }
    assertEquals(Call(FLEET, false, "/api/v1/session", null, null), script.calls[0])
    assertEquals(Call(FLEET, true, "/api/v1/message", "tok", """{"text":"hi"}"""), script.calls[1])
    assertEquals(Call(FLEET, true, "/api/v1/supervisor/start", "tok", ""), script.calls[2])
  }

  @Test
  fun aReadSendsNoCsrfToken() {
    val script = Script(envelope("s", """{"csrf":"tok"}"""), envelope("w"), envelope("c1"))
    val client = client(script)
    runBlocking {
      client.write("message", "{}", Status.serializer())
      client.status()
    }
    assertNull(script.calls.last().csrf)
  }

  @Test
  fun aRefusedTokenIsReadAgainAndTheWriteSentOnce() {
    val script =
      Script(
        envelope("s", """{"csrf":"old"}"""),
        refusal(403, "csrf_required"),
        envelope("s", """{"csrf":"new"}"""),
        envelope("w")
      )
    runBlocking { client(script).write("message", "{}", Status.serializer()) }
    assertEquals(
      listOf("/api/v1/session", "/api/v1/message", "/api/v1/session", "/api/v1/message"),
      script.calls.map { it.path }
    )
    assertEquals(listOf(null, "old", null, "new"), script.calls.map { it.csrf })
  }

  @Test
  fun aSecondRefusedTokenEndsTheWrite() {
    val script =
      Script(
        envelope("s", """{"csrf":"a"}"""),
        refusal(403, "csrf_required"),
        envelope("s", """{"csrf":"b"}"""),
        refusal(403, "csrf_required")
      )
    val error = failure { client(script).write("message", "{}", Status.serializer()) }
    assertTrue(error is BoardError.CsrfRequired)
    assertEquals(4, script.calls.size)
  }

  @Test
  fun aRefusedWriteIsNeverSentAgain() {
    val script = Script(envelope("s", """{"csrf":"a"}"""), refusal(409, "conflict"))
    val error = failure { client(script).write("report/r1/ack", null, Status.serializer()) }
    assertTrue(error is BoardError.Conflict)
    assertEquals(2, script.calls.size)
  }

  @Test
  fun everyLinkFailureArrivesAsItsOwnType() {
    val names =
      mapOf(
        "busy" to BoardError.Busy,
        "no_laptop" to BoardError.NoLaptop,
        "unreachable" to BoardError.Unreachable,
        "unpaired" to BoardError.Unpaired,
        "lost" to BoardError.Lost,
        "board_not_running" to BoardError.BoardNotRunning,
        "timed_out" to BoardError.TimedOut,
        "too_large" to BoardError.TooLarge,
        "malformed" to BoardError.Malformed
      )
    for ((name, error) in names) {
      try {
        parseWire("""{"failure":"$name"}""")
        fail(name)
      } catch (thrown: BoardError) {
        assertSame(name, error, thrown)
      }
    }
  }

  @Test
  fun aWireAnswerThatIsMissingOrBrokenIsMalformed() {
    val broken =
      listOf(
        null,
        "",
        "nope",
        "[]",
        """{"status":"x","body":""}""",
        """{"status":200}""",
        """{"failure":{}}""",
        """{"failure":["busy"]}"""
      )
    for (text in broken) {
      try {
        parseWire(text)
        fail("$text")
      } catch (thrown: BoardError) {
        assertSame("$text", BoardError.Malformed, thrown)
      }
    }
  }

  @Test
  fun aKnownCodeWithNoMessageStillMapsToItsType() {
    val raw = Raw(409, """{"error":{"code":"conflict"}}""")
    val error = failure { client(Script(raw)).status() }
    assertTrue(error is BoardError.Conflict)
    assertNull(error.message)
  }

  @Test
  fun anAnswerForAnotherFleetIsRefused() {
    val raw = Raw(200, golden("status").replace(FLEET, "f11111111e0aa"))
    assertSame(BoardError.Malformed, failure { client(Script(raw)).status() })
  }

  @Test
  fun theTransportRunsOffTheCallersThread() {
    val executor = Executors.newSingleThreadExecutor { Thread(it, "board-io-test") }
    try {
      val script = Script(envelope("c1"))
      val client = BoardClient(FLEET, script, executor.asCoroutineDispatcher())
      runBlocking { client.status() }
      assertTrue(script.threads.single().startsWith("board-io-test"))
      assertFalse(Thread.currentThread().name.startsWith("board-io-test"))
    } finally {
      executor.shutdown()
    }
  }

  @Test
  fun anAllClearStatusWithAHeldSupervisorDecodes() {
    val supervisor =
      """{"ref":"s2","harness":"claude","status":"held","live":false,"pending":1,"resumable":true,""" +
        """"held":true,"switching":"opus","reason":"waiting for a model switch"}"""
    val data =
      status("idle")
        .replace(""""agent_state"""", """"supervisor":$supervisor,"agent_state"""")
        .replace(
          """"needs":{"waiting":0,"more":0,"worst_count":0,"items":[]}""",
          """"needs":{"waiting":0,"more":0,"worst_count":0,"cleared":"All clear",""" +
            """"summary":"3 tasks, nothing waiting","items":[]}"""
        )
    val status = poll(runBlocking { client(Script(envelope("c", data))).status() }).data
    val held = checkNotNull(status.supervisor)
    assertTrue(held.held)
    assertEquals("opus", held.switching)
    assertEquals("waiting for a model switch", held.reason)
    assertNull(held.model)
    assertEquals("All clear", status.needs.cleared)
    assertEquals("3 tasks, nothing waiting", status.needs.summary)
    assertNull(status.needs.worst)
    assertTrue(status.needs.items.isEmpty())
  }

  @Test
  fun aWireAnswerKeepsItsStatusAndBody() {
    val raw = parseWire("""{"status":304,"body":""}""")
    assertEquals(304, raw.status)
    assertEquals("", raw.body)
    assertEquals("{\"a\":1}", parseWire("""{"status":200,"body":"{\"a\":1}"}""").body)
  }

  @Test
  fun aLinkFailureEndsTheCallWithItsType() {
    val transport = Transport { _, _, _, _, _ -> parseWire("""{"failure":"busy"}""") }
    assertSame(BoardError.Busy, failure { client(transport).status() })
  }

  @Test
  fun everyStatusFixtureDecodes() {
    for (name in listOf("status", "status-blocked", "supervisor-keys", "supervisor-started")) {
      val answer = poll(runBlocking { client(Script(Raw(200, golden(name)))).status() })
      assertEquals(name, "golden", answer.fleet.name)
      assertNotNull(name, answer.cursor)
      assertEquals(name, 1, answer.data.projects)
    }
  }

  @Test
  fun theQuietStatusFixtureKeepsItsNeedsItems() {
    val data = poll(runBlocking { client(Script(Raw(200, golden("status")))).status() }).data
    val supervisor = checkNotNull(data.supervisor)
    assertEquals("s1", supervisor.ref)
    assertEquals("sonnet", supervisor.model)
    assertEquals(false, supervisor.blocked)
    assertNull(supervisor.ask)
    assertEquals(4, data.needs.waiting)
    assertEquals("failure", data.needs.worst)
    assertEquals(listOf("failure", "decision", "report", "report"), data.needs.items.map { it.kind })
    val report = data.needs.items[2]
    assertEquals("PR: https://github.com/example/hand/pull/42\n\nAll tests pass.", report.excerpt)
    assertEquals("2026-11-01T04:40:00Z", report.createdAt)
    assertEquals(3, data.tasks.active)
  }

  @Test
  fun theBlockedFixtureCarriesTheAskAndTheKeyGuards() {
    val data = poll(runBlocking { client(Script(Raw(200, golden("status-blocked")))).status() }).data
    val supervisor = checkNotNull(data.supervisor)
    assertTrue(supervisor.blocked)
    assertEquals(Keys(7, "f8c722e0710879ae811b2fe7ad0c8ba0", 0), supervisor.keys)
    val ask = checkNotNull(supervisor.ask)
    assertEquals("Allow Bash?", ask.question)
    assertEquals(
      listOf(AskOption("2 No", listOf("2")), AskOption("1 Yes", listOf("1"))),
      ask.options
    )
    val blocked = data.needs.items.first()
    assertEquals("blocked", blocked.kind)
    assertNull(blocked.title)
    assertEquals("blocked", data.needs.worst)
    assertEquals("blocked", data.agentState)
  }

  @Test
  fun aBlockedItemMayCarryAnExcerptATitleAndOptions() {
    val item =
      """{"kind":"blocked","ref":"s1","word":"BLOCKED","tone":"fail","title":"Allow Bash?",""" +
        """"excerpt":"line 1\nline 2","cut":true,"deny_at":"2026-11-01T05:00:30Z",""" +
        """"options":[{"label":"1 Yes","keys":["1"]},{"label":"Other","keys":["down","enter"]}]}"""
    val data = status("failing").replace(""""items":[]""", """"items":[$item]""")
    val need = poll(runBlocking { client(Script(envelope("c", data))).status() }).data.needs.items.single()
    assertEquals("Allow Bash?", need.title)
    assertEquals("line 1\nline 2", need.excerpt)
    assertTrue(need.cut)
    assertEquals("2026-11-01T05:00:30Z", need.denyAt)
    assertEquals(
      listOf(AskOption("1 Yes", listOf("1")), AskOption("Other", listOf("down", "enter"))),
      need.options
    )
  }

  @Test
  fun anUnknownFieldIsIgnoredAtEveryLevel() {
    val root = kotlinx.serialization.json.Json.parseToJsonElement(golden("status-blocked")) as JsonObject
    val data = root.getValue("data") as JsonObject
    val supervisor = data.getValue("supervisor") as JsonObject
    val ask = supervisor.getValue("ask") as JsonObject
    val widened =
      JsonObject(
        root + ("later" to JsonPrimitive(1)) +
          ("data" to
            JsonObject(
              data +
                ("extra" to JsonObject(mapOf("a" to JsonPrimitive(true)))) +
                ("supervisor" to
                  JsonObject(supervisor + ("ask" to JsonObject(ask + ("hint" to JsonPrimitive("x")))) + ("next" to JsonPrimitive(2))))
            ))
      )
    val answer = poll(runBlocking { client(Script(Raw(200, widened.toString()))).status() })
    assertEquals("Allow Bash?", answer.data.supervisor?.ask?.question)
  }

  @Test
  fun theFeaturesComeFromTheHostsMetaAnswer() {
    val script =
      Script(
        Raw(
          200,
          """{"version":1,"generated_at":"2026-11-14T03:12:00Z","data":{"version":"9.9.9","features":["events","fleets","pair"]}}"""
        )
      )
    val features = runBlocking { client(script).features() }
    assertEquals(setOf("events", "fleets", "pair"), features)
    assertEquals(Call(FLEET, false, "/api/v1/meta", null, null), script.calls.single())
  }

  @Test
  fun aMetaAnswerOfAnotherVersionOrShapeIsMalformed() {
    for (body in
      listOf(
        """{"data":{"features":[]}}""",
        """{"version":1}""",
        """{"version":1,"data":{"features":"events"}}"""
      )) {
      assertSame(body, BoardError.Malformed, failure { client(Script(Raw(200, body))).features() })
    }
    val unsupported =
      failure { client(Script(Raw(200, """{"version":2,"data":{"features":[]}}"""))).features() }
    assertTrue(unsupported is BoardError.UnsupportedVersion)
  }

  private fun failure(call: suspend () -> Any?): BoardError =
    try {
      runBlocking { call() }
      fail("no error")
      throw IllegalStateException()
    } catch (error: BoardError) {
      error
    }
}
