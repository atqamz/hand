package io.github.atqamz.handcompanion

import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private const val DECISION_FLEET = "f000000000003"

private val choices =
  """[{"key":"1","label":"Pin 0.14.2","note":"reproducible builds","value":"1. Pin 0.14.2","recommended":true},""" +
    """{"key":"2","label":"Track 0.14.x","note":"patch fixes arrive","value":"2. Track 0.14.x"},""" +
    """{"key":"3","label":"Other","value":"3. Other","other":true}]"""

private val body =
  "CI broke when luvus 0.15 changed its output.\\n\\n1. Pin 0.14.2 (recommended) - reproducible builds\\n" +
    "2. Track 0.14.x - patch fixes arrive\\n3. Other\\n\\n```\\n1. kept in the fence\\n```"

private fun detailOf(status: String = "open", answer: String = "", by: String = "", opts: String = choices) =
  Raw(
    200,
    """{"version":1,"fleet":{"id":"$DECISION_FLEET","name":"atlas"},"data":{"decision":{"ref":"d12","task":"t40",""" +
      """"status":"$status","headline":"Pin luvus to which version?","question":"q","answer":"$answer",""" +
      """"answered_by":"$by","created_at":"2026-11-11T11:40:00Z"},""" +
      """"task":{"ref":"t40","title":"Pin the luvus version in CI"},"body":"$body","options":$opts}}"""
  )

private fun refusedAnswer(status: Int, code: String) =
  Raw(status, """{"error":{"code":"$code","message":"no"}}""")

private class Drafting : Drafts {
  val saved = mutableMapOf<String, String>()

  override fun read(fleet: String, ref: String) = saved["$fleet/$ref"].orEmpty()

  override fun write(fleet: String, ref: String, note: String) {
    if (note.isEmpty()) saved.remove("$fleet/$ref") else saved["$fleet/$ref"] = note
  }
}

private class DecisionBoard(var read: () -> Raw = { detailOf() }, var post: (String?) -> Raw = { detailOf("answered") }) :
  Transport {
  val calls = mutableListOf<String>()
  val bodies = mutableListOf<String?>()
  var sendingDuring: Boolean? = null
  var model: DecisionModel? = null

  override fun request(fleet: String, post: Boolean, path: String, csrf: String?, body: String?): Raw {
    calls += "${if (post) "POST" else "GET"} $path"
    return when {
      path == "/api/v1/session" ->
        Raw(200, """{"version":1,"fleet":{"id":"$DECISION_FLEET","name":"atlas"},"data":{"csrf":"tok"}}""")
      post -> {
        bodies += body
        sendingDuring = model?.state?.value?.sending
        this.post(body)
      }
      else -> read()
    }
  }
}

private fun modelOf(board: DecisionBoard, drafts: Drafts = Drafting()) =
  DecisionModel(DECISION_FLEET, "d12", board, drafts).also { board.model = it }

private fun DecisionModel.now() = state.value

private fun DecisionModel.loaded() = also { runBlocking { load() } }

private fun postsTo(board: DecisionBoard) = board.calls.count { it.startsWith("POST") }

class DecisionModelTest {
  @Test
  fun theOptionsComeFromTheApiAndTheQuestionLosesItsOptionLines() {
    val model = modelOf(DecisionBoard()).loaded()
    val detail = model.now().detail!!
    assertEquals(listOf("1", "2", "3"), detail.options.map { it.key })
    assertEquals("reproducible builds", detail.options[0].note)
    assertEquals(
      "CI broke when luvus 0.15 changed its output.\n\n\n```\n1. kept in the fence\n```",
      prose(detail.body, detail.options)
    )
  }

  @Test
  fun aBodyWithoutOptionsStaysWhole() {
    assertEquals("1. a\n2. b", prose("1. a\n2. b", emptyList()))
  }

  @Test
  fun onlyTheRecommendedOptionCarriesTheBadge() {
    val detail = modelOf(DecisionBoard()).loaded().now().detail!!
    assertEquals(listOf("1"), detail.options.filter { it.recommended }.map { it.key })
  }

  @Test
  fun sendingWithNothingPickedOrWrittenAsksForOne() {
    val board = DecisionBoard()
    val model = modelOf(board).loaded()
    runBlocking { model.answer() }
    assertEquals("Pick an option or write an answer.", model.now().message)
    assertEquals(0, postsTo(board))
  }

  @Test
  fun anOtherOptionNeedsANote() {
    val board = DecisionBoard()
    val model = modelOf(board).loaded()
    model.pick("3")
    runBlocking { model.answer() }
    assertEquals("Write your answer.", model.now().message)
    assertEquals(0, postsTo(board))
    model.edit("  keep it loose  ")
    runBlocking { model.answer() }
    assertEquals(1, postsTo(board))
    assertEquals("""{"choice":"3. Other","note":"keep it loose"}""", board.bodies.single())
  }

  @Test
  fun aNoteAloneIsAnAnswer() {
    val board = DecisionBoard()
    val model = modelOf(board).loaded()
    model.edit("later")
    runBlocking { model.answer() }
    assertEquals("""{"choice":"","note":"later"}""", board.bodies.single())
  }

  @Test
  fun aSentAnswerSwapsTheFormForTheAnsweredState() {
    val board = DecisionBoard(post = { detailOf("answered", "2. Track 0.14.x\\nfine", "phone (Pixel)") })
    val model = modelOf(board).loaded()
    model.pick("2")
    runBlocking { model.answer() }
    val state = model.now()
    assertTrue(state.answered)
    assertFalse(state.sending)
    assertNull(state.message)
    assertEquals("""{"choice":"2. Track 0.14.x","note":""}""", board.bodies.single())
    val shown = state.detail!!
    assertEquals("2", chosenKey(shown.options, shown.decision.answer))
    assertEquals("fine", answerNote(shown.options, shown.decision.answer))
    assertTrue("POST /api/v1/decision/d12/answer" in board.calls)
  }

  @Test
  fun sendingStaysOffWhileTheAnswerIsInFlightAndASecondTapSendsNothing() {
    val board = DecisionBoard()
    board.post = {
      runBlocking { board.model!!.answer() }
      detailOf("answered")
    }
    val model = modelOf(board).loaded()
    model.pick("1")
    runBlocking { model.answer() }
    assertEquals(true, board.sendingDuring)
    assertEquals(1, postsTo(board))
  }

  @Test
  fun anAnsweredDecisionSendsNothing() {
    val board = DecisionBoard()
    val model = modelOf(board).loaded()
    model.pick("1")
    runBlocking { model.answer() }
    runBlocking { model.answer() }
    assertEquals(1, postsTo(board))
  }

  @Test
  fun anAnswerGivenElsewhereShowsTheAnsweredState() {
    val board = DecisionBoard(post = { refusedAnswer(409, "conflict") })
    val model = modelOf(board).loaded()
    model.pick("1")
    board.read = { detailOf("answered", "2. Track 0.14.x", "operator (desktop)") }
    runBlocking { model.answer() }
    val state = model.now()
    assertEquals("d12 is already answered.", state.message)
    assertTrue(state.answered)
    assertEquals("2", chosenKey(state.detail!!.options, state.detail!!.decision.answer))
  }

  @Test
  fun aWithdrawnDecisionSaysSoAfterTheReRead() {
    val board = DecisionBoard(post = { refusedAnswer(409, "conflict") })
    val model = modelOf(board).loaded()
    model.pick("1")
    board.read = { detailOf("withdrawn") }
    runBlocking { model.answer() }
    assertEquals("d12 is already withdrawn.", model.now().message)
    assertTrue(model.now().answered)
  }

  @Test
  fun aConflictWhoseReReadFailsKeepsTheFormAndNeverGuessesTheStatus() {
    val board = DecisionBoard(post = { refusedAnswer(409, "conflict") })
    val model = modelOf(board).loaded()
    model.pick("1")
    board.read = { throw BoardError.Unreachable }
    runBlocking { model.answer() }
    assertEquals("d12 is no longer open.", model.now().message)
    assertFalse(model.now().answered)
  }

  @Test
  fun aLostAnswerReadsTheDecisionAgainAndAnAnsweredOneDropsTheWarning() {
    val board = DecisionBoard(post = { throw BoardError.Lost })
    val model = modelOf(board).loaded()
    model.pick("1")
    board.read = { detailOf("answered", "1. Pin 0.14.2") }
    runBlocking { model.answer() }
    val state = model.now()
    assertNull(state.message)
    assertTrue(state.answered)
    assertEquals(2, board.calls.count { it == "GET /api/v1/decision/d12" })
  }

  @Test
  fun aTimedOutAnswerThatDidNotLandKeepsTheFormAndTheNote() {
    val drafts = Drafting()
    val board = DecisionBoard(post = { throw BoardError.TimedOut })
    val model = modelOf(board, drafts).loaded()
    model.pick("1")
    model.edit("why")
    runBlocking { model.answer() }
    val state = model.now()
    assertFalse(state.answered)
    assertFalse(state.sending)
    assertEquals("Not sure d12 was sent. The answer may have been sent.", state.message)
    assertEquals("why", state.note)
    assertEquals("why", drafts.read(DECISION_FLEET, "d12"))
  }

  @Test
  fun aTwoHundredThatCannotBeReadClearsTheDraftAndReadsTheDecisionAgain() {
    val drafts = Drafting()
    val board = DecisionBoard(post = { Raw(200, "{}") })
    val model = modelOf(board, drafts).loaded()
    model.pick("1")
    model.edit("why")
    board.read = { detailOf("answered", "1. Pin 0.14.2\\nwhy") }
    runBlocking { model.answer() }
    assertEquals("", drafts.read(DECISION_FLEET, "d12"))
    assertEquals("", model.now().note)
    assertTrue(model.now().answered)
    assertEquals(2, board.calls.count { it == "GET /api/v1/decision/d12" })
  }

  @Test
  fun readingAClosedDecisionDropsItsDraftAndWarning() {
    val drafts = Drafting()
    drafts.write(DECISION_FLEET, "d12", "late thought")
    val model = modelOf(DecisionBoard(read = { detailOf("answered", "2. Track 0.14.x") }), drafts).loaded()
    assertEquals("", drafts.read(DECISION_FLEET, "d12"))
    assertEquals("", model.now().note)
    assertNull(model.now().message)
  }

  @Test
  fun aRefusedAnswerKeepsTheFormAndSaysSo() {
    val board = DecisionBoard(post = { refusedAnswer(400, "bad_request") })
    val model = modelOf(board).loaded()
    model.pick("1")
    runBlocking { model.answer() }
    assertEquals("Could not send d12.", model.now().message)
    assertFalse(model.now().answered)
  }

  @Test
  fun aPhoneThatIsNoLongerPairedSaysSo() {
    val model = modelOf(DecisionBoard(read = { throw BoardError.Unpaired })).loaded()
    assertTrue(model.now().unpaired)
  }

  @Test
  fun aFailedReadShowsNoFormUntilRetried() {
    val board = DecisionBoard(read = { throw BoardError.Unreachable })
    val model = modelOf(board).loaded()
    assertTrue(model.now().failed)
    assertNull(model.now().detail)
    board.read = { detailOf() }
    model.loaded()
    assertFalse(model.now().failed)
    assertEquals(3, model.now().detail!!.options.size)
  }

  @Test
  fun theNoteIsKeptPerDecisionAndClearedAfterASuccessfulSend() {
    val drafts = Drafting()
    val first = modelOf(DecisionBoard(), drafts)
    first.edit("half a thought")
    assertEquals("half a thought", drafts.read(DECISION_FLEET, "d12"))
    assertEquals("", drafts.read(DECISION_FLEET, "d13"))
    val again = modelOf(DecisionBoard(), drafts).loaded()
    assertEquals("half a thought", again.now().note)
    again.pick("1")
    runBlocking { again.answer() }
    assertEquals("", drafts.read(DECISION_FLEET, "d12"))
    assertEquals("", again.now().note)
  }

  @Test
  fun aNoteWithoutOptionsIsTheAnswer() {
    val board = DecisionBoard(read = { detailOf(opts = "[]") })
    val model = modelOf(board).loaded()
    runBlocking { model.answer() }
    assertEquals("Write your answer.", model.now().message)
    model.edit("yes")
    runBlocking { model.answer() }
    assertEquals("""{"choice":"","note":"yes"}""", board.bodies.single())
  }
}
