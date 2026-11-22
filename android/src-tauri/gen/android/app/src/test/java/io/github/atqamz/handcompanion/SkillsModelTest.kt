package io.github.atqamz.handcompanion

import java.util.concurrent.CopyOnWriteArrayList
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
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

private class SkillsBoard(
  private val features: List<String> = listOf("events", "skills"),
  var skills: () -> Raw = {
    Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"cursor":"c1","data":$skillsJson}""")
  }
) : Transport {
  val calls = CopyOnWriteArrayList<String>()

  override fun request(fleet: String, post: Boolean, path: String, csrf: String?, body: String?): Raw {
    calls += "${if (post) "POST" else "GET"} $path"
    return when (path) {
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
    assertTrue(model.state.value.problem!!.startsWith("The Board did not answer in 30 seconds."))
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
  fun aThirdPartyUpdateIsSentToTheHostForItsDiff() {
    val skills = boardJson.decodeFromString(SkillsData.serializer(), skillsJson).scopes[1].skills
    assertEquals("Third-party: read its diff on the host before it moves.", skillNote(skills[1]))
    assertEquals("", skillNote(skills[0]))
  }
}
