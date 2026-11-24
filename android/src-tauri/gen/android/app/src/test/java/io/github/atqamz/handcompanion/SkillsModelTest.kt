package io.github.atqamz.handcompanion

import java.util.concurrent.CopyOnWriteArrayList
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private const val FLEET = "f000000000003"

internal val skillsJson =
  """{"scopes":[""" +
    """{"scope":"fleet","skills":[""" +
    """{"name":"secondhand","source":"hand init","managed":true,"trusted":true,"state":"current"},""" +
    """{"name":"scratch"}]},""" +
    """{"scope":"project","project":"hand","skills":[""" +
    """{"name":"review","source":"https://github.com/atqamz/skills.git#skills/review","ref":"main",""" +
    """"commit":"1111111111111111111111111111111111111111","latest":"2222222222222222222222222222222222222222",""" +
    """"managed":true,"trusted":true,"state":"behind"},""" +
    """{"name":"notes","source":"https://github.com/example/notes.git","ref":"v1.2.0",""" +
    """"commit":"3333333333333333333333333333333333333333","latest":"4444444444444444444444444444444444444444",""" +
    """"managed":true,"trusted":false,"state":"behind","diff":"+x","diff_cut":true},""" +
    """{"name":"deploy","source":"https://github.com/atqamz/skills.git#skills/deploy","ref":"main",""" +
    """"commit":"1111111111111111111111111111111111111111","managed":true,"trusted":true,"state":"edited"},""" +
    """{"name":"triage","managed":true,"trusted":true,"state":"conflict"}]},""" +
    """{"scope":"project","project":"api","error":"the folder is not a git checkout","skills":[]},""" +
    """{"scope":"user","skills":[{"name":"lint","managed":true,"trusted":true,"state":"error",""" +
    """"detail":"the fetch of the source failed"}]}]}"""

private const val SYNC_RECEIPT = "Skills sync: 1 behind, 1 updated"

private val syncRun =
  Raw(
    200,
    """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"receipt":"$SYNC_RECEIPT",""" +
      """"data":{"scope":"project","project":"hand","results":[""" +
      """{"name":"review","state":"updated","detail":"111111111111 -> 222222222222"},""" +
      """{"name":"notes","state":"behind","detail":"third-party, latest 4444"}],"refused":0}}"""
  )

private class SkillsBoard(
  private val features: List<String> = listOf("events", "skills", "skills_sync"),
  var sync: () -> Raw = { syncRun },
  var skills: () -> Raw = {
    Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"cursor":"c1","data":$skillsJson}""")
  }
) : Transport {
  val calls = CopyOnWriteArrayList<String>()
  val bodies = CopyOnWriteArrayList<String>()

  override fun request(fleet: String, post: Boolean, path: String, csrf: String?, body: String?): Raw {
    calls += "${if (post) "POST" else "GET"} $path"
    if (post) bodies += body.orEmpty()
    return when (path) {
      "/api/v1/session" -> Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":{"csrf":"tok"}}""")
      "/api/v1/skills/sync" -> sync()
      "/api/v1/meta" ->
        Raw(200, """{"version":1,"data":{"features":${features.joinToString(",", "[", "]") { "\"$it\"" }}}}""")
      "/api/v1/skills" -> skills()
      else -> Raw(404, """{"error":{"status":404,"code":"not_found","message":"not found"}}""")
    }
  }
}

private fun refused(status: Int, code: String, message: String) =
  Raw(status, """{"error":{"status":$status,"code":"$code","message":"$message"}}""")

class SkillsModelTest {
  @Test
  fun aReadListsEachScopeWithItsSkills() = runBlocking {
    val board = SkillsBoard()
    val model = SkillsModel(FLEET, board)
    model.load()
    val state = model.state.value
    assertFalse(state.loading)
    assertNull(state.problem)
    assertEquals(
      listOf("Fleet", "Project hand", "Project api", "User"),
      state.data!!.scopes.map(::skillScopeTitle)
    )
    assertEquals(listOf("GET /api/v1/meta", "GET /api/v1/skills"), board.calls.toList())
  }

  @Test
  fun theModelNeverPostsAndAsksForNoOtherScopeThanTheBoardLists() = runBlocking {
    val board = SkillsBoard()
    SkillsModel(FLEET, board).load()
    assertTrue(board.calls.none { it.startsWith("POST") })
  }

  @Test
  fun aBoardWithoutTheSkillsFeatureSaysSoAndReadsNothing() = runBlocking {
    val board = SkillsBoard(features = listOf("events"))
    val model = SkillsModel(FLEET, board)
    model.load()
    assertEquals(SKILLS_NO_FEATURE, model.state.value.problem)
    assertNull(model.state.value.data)
    assertEquals(listOf("GET /api/v1/meta"), board.calls.toList())
  }

  @Test
  fun theBoardsSentenceForABusyReadIsShownAsWritten() = runBlocking {
    val sentence = "another skills request is running on this board; try again when it ends"
    val board = SkillsBoard(skills = { refused(409, "conflict", sentence) })
    val model = SkillsModel(FLEET, board)
    model.load()
    assertEquals(sentence, model.state.value.problem)
    assertFalse(model.state.value.loading)
  }

  @Test
  fun aTimedOutReadSaysItCanTakeLonger() = runBlocking {
    val board = SkillsBoard(skills = { throw BoardError.TimedOut })
    val model = SkillsModel(FLEET, board)
    model.load()
    assertTrue(model.state.value.problem!!.startsWith("The Board did not answer in time."))
  }

  @Test
  fun anUnpairedCompanionIsToldSoNotGivenAnError() = runBlocking {
    val board = SkillsBoard(skills = { throw BoardError.Unpaired })
    val model = SkillsModel(FLEET, board)
    model.load()
    assertTrue(model.state.value.unpaired)
    assertNull(model.state.value.problem)
  }

  @Test
  fun aFailedCheckKeepsTheListItHad() = runBlocking {
    val board = SkillsBoard()
    val model = SkillsModel(FLEET, board)
    model.load()
    board.skills = { throw BoardError.Lost }
    model.load()
    assertNotNull(model.state.value.data)
    assertEquals("The Board could not be reached.", model.state.value.problem)
  }

  @Test
  fun aSecondReadWhileOneRunsIsDropped() = runBlocking {
    val gate = CompletableDeferred<Unit>()
    val board =
      SkillsBoard(
        skills = {
          runBlocking { gate.await() }
          Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"cursor":"c1","data":$skillsJson}""")
        }
      )
    val model = SkillsModel(FLEET, board)
    val first = launch(Dispatchers.Default) { model.load() }
    while (board.calls.none { it == "GET /api/v1/skills" }) delay(5)
    model.load()
    gate.complete(Unit)
    first.join()
    assertEquals(1, board.calls.count { it == "GET /api/v1/skills" })
  }

  @Test
  fun eachStateHasItsWordToneAndTrust() {
    val skills = boardJson.decodeFromString(SkillsData.serializer(), skillsJson)
    val byName = skills.scopes.flatMap { it.skills }.associateBy { it.name }
    assertEquals("current", skillStateWord(byName.getValue("secondhand")))
    assertEquals(Tone.Pass, skillTone(byName.getValue("secondhand")))
    assertEquals("unmanaged", skillStateWord(byName.getValue("scratch")))
    assertEquals(Tone.Neutral, skillTone(byName.getValue("scratch")))
    assertNull(skillTrust(byName.getValue("scratch")))
    assertEquals(Tone.Wait, skillTone(byName.getValue("review")))
    assertEquals("own source", skillTrust(byName.getValue("review")))
    assertEquals("third-party", skillTrust(byName.getValue("notes")))
    assertEquals(Tone.Wait, skillTone(byName.getValue("deploy")))
    assertEquals(Tone.Fail, skillTone(byName.getValue("triage")))
    assertEquals(Tone.Fail, skillTone(byName.getValue("lint")))
  }

  @Test
  fun theSourceLineShowsTheLatestAfterAnArrowOnlyWhenItMoved() {
    val skills = boardJson.decodeFromString(SkillsData.serializer(), skillsJson).scopes[1].skills
    assertEquals(
      "https://github.com/atqamz/skills.git#skills/review · main · 111111111111 → 222222222222",
      skillSource(skills[0])
    )
    assertEquals(
      "https://github.com/atqamz/skills.git#skills/deploy · main · 111111111111",
      skillSource(skills[2])
    )
  }

  @Test
  fun aSkillAtItsLatestShowsNoArrow() {
    val skill =
      SkillInfo(
        name = "current",
        source = "https://github.com/atqamz/skills.git#skills/current",
        ref = "main",
        commit = "1111111111111111111111111111111111111111",
        latest = "1111111111111111111111111111111111111111",
        managed = true,
        trusted = true,
        state = "current"
      )
    assertEquals("https://github.com/atqamz/skills.git#skills/current · main · 111111111111", skillSource(skill))
  }

  @Test
  fun aReadCancelledWithItsCallerStillEndsAndALaterCallIsNotLeftLoading() = runBlocking {
    val gate = CompletableDeferred<Unit>()
    val board =
      SkillsBoard(
        skills = {
          runBlocking { gate.await() }
          Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"cursor":"c1","data":$skillsJson}""")
        }
      )
    val model = SkillsModel(FLEET, board)
    val first = launch(Dispatchers.Default) { model.load() }
    while (board.calls.none { it == "GET /api/v1/skills" }) delay(5)
    first.cancel()
    model.load()
    gate.complete(Unit)
    first.join()
    withTimeout(5000) { while (model.state.value.loading) delay(5) }
    assertNotNull(model.state.value.data)
  }

  @Test
  fun aThirdPartyUpdateIsSentToTheHostForItsDiff() {
    val skills = boardJson.decodeFromString(SkillsData.serializer(), skillsJson).scopes[1].skills
    assertEquals("Third-party: read its diff on the host before it moves.", skillNote(skills[1]))
    assertEquals("", skillNote(skills[0]))
  }

  private fun synced(board: SkillsBoard = SkillsBoard()) = runBlocking {
    val model = SkillsModel(FLEET, board)
    model.load()
    model
  }

  @Test
  fun aSyncSendsTheScopeAndTheProjectAndNothingElse() = runBlocking {
    val board = SkillsBoard()
    val model = synced(board)
    model.runSync(SyncKey("fleet"))
    model.runSync(SyncKey("project", "hand"))
    assertEquals(listOf("""{"scope":"fleet"}""", """{"scope":"project","project":"hand"}"""), board.bodies.toList())
    assertTrue(board.calls.filter { it.startsWith("POST") }.all { it == "POST /api/v1/skills/sync" })
  }

  @Test
  fun theUserScopeAndAnythingButFleetAndProjectIsNeverSent() = runBlocking {
    val board = SkillsBoard()
    val model = synced(board)
    model.runSync(SyncKey("user"))
    model.runSync(SyncKey("everything"))
    assertTrue(board.calls.none { it.startsWith("POST") })
  }

  @Test
  fun aBoardWithoutTheSyncTokenOffersNoSyncAndSendsNone() = runBlocking {
    val board = SkillsBoard(features = listOf("events", "skills"))
    val model = synced(board)
    assertFalse(model.state.value.canSync)
    model.runSync(SyncKey("fleet"))
    assertTrue(board.calls.none { it.startsWith("POST") })
  }

  @Test
  fun aBoardWithTheSyncTokenOffersIt() = runBlocking {
    assertTrue(synced().state.value.canSync)
  }

  @Test
  fun aSyncShowsTheReceiptAndTheRowsAndThenReadsTheListAgain() = runBlocking {
    val board = SkillsBoard()
    val model = synced(board)
    model.runSync(SyncKey("project", "hand"))
    val outcome = model.state.value.outcome!!
    assertEquals(SyncKey("project", "hand"), outcome.key)
    assertEquals(SYNC_RECEIPT, outcome.receipt)
    assertEquals(listOf("review" to "updated", "notes" to "behind"), outcome.results.map { it.name to it.state })
    assertNull(outcome.problem)
    assertNull(model.state.value.syncing)
    assertEquals(
      listOf("GET /api/v1/meta", "GET /api/v1/skills", "GET /api/v1/session", "POST /api/v1/skills/sync", "GET /api/v1/meta", "GET /api/v1/skills"),
      board.calls.toList()
    )
  }

  @Test
  fun aForbiddenSyncShowsTheBoardsSentenceAndHidesSyncForTheSession() = runBlocking {
    val sentence = "a paired Companion cannot sync the user scope; use the host's Board"
    val board = SkillsBoard(sync = { refused(403, "forbidden", sentence) })
    val model = synced(board)
    model.runSync(SyncKey("fleet"))
    assertEquals(sentence, model.state.value.outcome!!.problem)
    assertFalse(model.state.value.canSync)
    model.load()
    assertFalse(model.state.value.canSync)
    model.runSync(SyncKey("fleet"))
    assertEquals(1, board.bodies.size)
  }

  @Test
  fun aBusyBoardsSentenceIsShownAsWrittenAndSyncStaysOffered() = runBlocking {
    val sentence = "another skills request is running on this board; try again when it ends"
    val board = SkillsBoard(sync = { refused(409, "conflict", sentence) })
    val model = synced(board)
    model.runSync(SyncKey("fleet"))
    assertEquals(sentence, model.state.value.outcome!!.problem)
    assertTrue(model.state.value.canSync)
    assertNull(model.state.value.syncing)
    assertEquals(1, board.calls.count { it == "GET /api/v1/skills" })
  }

  @Test
  fun aTimedOutSyncSaysItMayStillBeRunningAndKeepsTheList() = runBlocking {
    val board = SkillsBoard(sync = { throw BoardError.TimedOut })
    val model = synced(board)
    model.runSync(SyncKey("fleet"))
    assertTrue(model.state.value.outcome!!.problem!!.contains("may still be running on the host"))
    assertNotNull(model.state.value.data)
    assertNull(model.state.value.syncing)
  }

  @Test
  fun aLostConnectionDuringASyncSaysItMayHaveRunOrStillRun() = runBlocking {
    val board = SkillsBoard(sync = { throw BoardError.Lost })
    val model = synced(board)
    model.runSync(SyncKey("fleet"))
    assertTrue(model.state.value.outcome!!.problem!!.contains("may have run or may still be running"))
    assertEquals(1, board.calls.count { it == "GET /api/v1/skills" })
  }

  @Test
  fun aSyncTheBoardRanButAnswerUnreadableSaysSoAndReadsTheListAgain() = runBlocking {
    val board = SkillsBoard(sync = { Raw(200, "{") })
    val model = synced(board)
    model.runSync(SyncKey("fleet"))
    assertEquals("The sync ran, but the Board's answer could not be read.", model.state.value.outcome!!.problem)
    assertEquals(2, board.calls.count { it == "GET /api/v1/skills" })
  }

  @Test
  fun anUnpairedSyncSaysSo() = runBlocking {
    val board = SkillsBoard(sync = { throw BoardError.Unpaired })
    val model = synced(board)
    model.runSync(SyncKey("fleet"))
    assertTrue(model.state.value.unpaired)
    assertNull(model.state.value.syncing)
  }

  @Test
  fun aSecondSyncWhileOneRunsIsDroppedNotQueued() = runBlocking {
    val gate = CompletableDeferred<Unit>()
    val board = SkillsBoard(sync = { runBlocking { gate.await() }; syncRun })
    val model = synced(board)
    val first = launch(Dispatchers.Default) { model.runSync(SyncKey("fleet")) }
    while (board.bodies.isEmpty()) delay(5)
    assertEquals(SyncKey("fleet"), model.state.value.syncing)
    val second = launch(Dispatchers.Default) { model.runSync(SyncKey("project", "hand")) }
    try {
      withTimeout(2000) { second.join() }
    } finally {
      gate.complete(Unit)
    }
    first.join()
    assertEquals(1, board.bodies.size)
  }

  @Test
  fun aReadWhileASyncRunsIsDropped() = runBlocking {
    val gate = CompletableDeferred<Unit>()
    val board = SkillsBoard(sync = { runBlocking { gate.await() }; syncRun })
    val model = synced(board)
    val first = launch(Dispatchers.Default) { model.runSync(SyncKey("fleet")) }
    while (board.bodies.isEmpty()) delay(5)
    val read = launch(Dispatchers.Default) { model.load() }
    try {
      withTimeout(2000) { read.join() }
    } finally {
      gate.complete(Unit)
    }
    first.join()
    assertEquals(2, board.calls.count { it == "GET /api/v1/skills" })
  }

  @Test
  fun aSyncCancelledWithItsCallerStillEndsAndLeavesItsResult() = runBlocking {
    val gate = CompletableDeferred<Unit>()
    val board = SkillsBoard(sync = { runBlocking { gate.await() }; syncRun })
    val model = synced(board)
    val first = launch(Dispatchers.Default) { model.runSync(SyncKey("fleet")) }
    while (board.bodies.isEmpty()) delay(5)
    first.cancel()
    gate.complete(Unit)
    first.join()
    withTimeout(5000) { while (model.state.value.syncing != null || model.state.value.loading) delay(5) }
    assertEquals(SYNC_RECEIPT, model.state.value.outcome!!.receipt)
    model.runSync(SyncKey("fleet"))
    assertEquals(2, board.bodies.size)
  }

  @OptIn(ExperimentalCoroutinesApi::class)
  @Test
  fun syncRunsInTheModelsOwnScope() {
    Dispatchers.setMain(Dispatchers.Unconfined)
    try {
      val board = SkillsBoard()
      val model = synced(board)
      model.sync("fleet")
      runBlocking {
        withTimeout(5000) { while (model.state.value.outcome == null || model.state.value.loading) delay(5) }
      }
      assertEquals(listOf("""{"scope":"fleet"}"""), board.bodies.toList())
    } finally {
      Dispatchers.resetMain()
    }
  }

  private fun scope(name: String, vararg skills: SkillInfo) = SkillScopeInfo(name, skills = skills.toList())

  private fun skill(trusted: Boolean, state: String, managed: Boolean = true) =
    SkillInfo("s", managed = managed, trusted = trusted, state = state)

  private fun offered(scope: SkillScopeInfo, syncing: SyncKey? = null, canSync: Boolean = true) =
    skillSyncShown(SkillsState(canSync = canSync, syncing = syncing, loading = false), scope)

  @Test
  fun syncShowsOnlyForAScopeWithAnOwnSkillBehind() {
    assertTrue(offered(scope("fleet", skill(true, "behind"))))
    assertTrue(offered(scope("project", skill(true, "behind"), skill(false, "behind"))))
    assertFalse(offered(scope("fleet", skill(false, "behind"))))
    assertFalse(offered(scope("fleet", skill(true, "edited"))))
    assertFalse(offered(scope("fleet", skill(true, "conflict"))))
    assertFalse(offered(scope("fleet", skill(true, "current"))))
    assertFalse(offered(scope("fleet", skill(true, "behind", managed = false))))
    assertFalse(offered(scope("fleet")))
  }

  @Test
  fun syncNeverShowsForTheUserScopeAFailedScopeOrAHostWithoutIt() {
    assertFalse(offered(scope("user", skill(true, "behind"))))
    assertFalse(offered(scope("fleet", skill(true, "behind")).copy(error = "unreadable")))
    assertFalse(offered(scope("fleet", skill(true, "behind")), canSync = false))
  }

  @Test
  fun syncStaysShownWhileItRuns() {
    assertTrue(offered(scope("project", skill(true, "current")).copy(project = "hand"), syncing = SyncKey("project", "hand")))
    assertFalse(offered(scope("project", skill(true, "current")).copy(project = "hand"), syncing = SyncKey("fleet")))
  }
}
