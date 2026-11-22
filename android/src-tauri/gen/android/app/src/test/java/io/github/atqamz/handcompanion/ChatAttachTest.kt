package io.github.atqamz.handcompanion

import androidx.compose.ui.text.TextRange
import java.util.concurrent.CopyOnWriteArrayList
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private const val FLEET = "f00000000e0aa"

private fun envelope(data: String) =
  """{"version":1,"fleet":{"id":"$FLEET","name":"golden"},"cursor":"c","data":$data}"""

private fun stored(name: String) =
  Raw(
    200,
    envelope(
      """{"name":"20261121-101500-1-$name","label":"$name","kind":"${if (name.endsWith(".png")) "image" else "file"}","size":7}"""
    )
  )

private class Posts : Transport {
  val bodies = CopyOnWriteArrayList<String?>()
  var answer: Raw = Raw(200, envelope("""{"entries":[]}"""))
  var gate: CompletableDeferred<Unit>? = null

  override fun request(
    fleet: String,
    post: Boolean,
    path: String,
    csrf: String?,
    body: String?
  ): Raw =
    when {
      post -> {
        bodies += body
        gate?.let { runBlocking { it.await() } }
        answer
      }
      path == "/api/v1/session" -> Raw(200, envelope("""{"csrf":"tok"}"""))
      else -> Raw(200, envelope("""{"entries":[]}"""))
    }
}

private class Uploaded(
  var answer: (String) -> Raw = { stored(it) },
  var gate: CompletableDeferred<Unit>? = null
) : Uploads {
  val sent = CopyOnWriteArrayList<Triple<String, String, Int>>()

  override fun upload(fleet: String, csrf: String, name: String, bytes: ByteArray): Raw {
    sent += Triple(name, csrf, bytes.size)
    gate?.let { runBlocking { it.await() } }
    return answer(name)
  }
}

private class Held : ComposerDrafts {
  var text = ""
  var files = emptyList<Attached>()

  override fun read(fleet: String, now: Long) = text

  override fun write(fleet: String, text: String, now: Long) {
    this.text = text
  }

  override fun readUnsure(fleet: String): UnsureSend? = null

  override fun writeUnsure(fleet: String, sent: UnsureSend?) {}

  override fun readFiles(fleet: String, now: Long) = files

  override fun writeFiles(fleet: String, files: List<Attached>, now: Long) {
    this.files = files
  }
}

private fun chat(board: Posts, uploads: Uploaded, drafts: ComposerDrafts = Held()) =
  ChatModel(FLEET, board, drafts, LiveBus(32), 5_000, { 1_760_000_000_000 }, 300, Dispatchers.Default, uploads)

private fun pick(name: String, size: Long = 7, bytes: Int = 7): Pick = {
  Picked(name, size) { ByteArray(bytes) }
}

class ChatAttachTest {
  @Test
  fun aPickedFileIsUploadedWithTheSessionsTokenAndItsTokenLandsAtTheCursor() = runBlocking {
    val uploads = Uploaded()
    val model = chat(Posts(), uploads)
    model.edit("look at  as you see", TextRange(8))
    model.attachNow(listOf(pick("a.png")))
    val state = model.state.value
    assertEquals(listOf(Triple("a.png", "tok", 7)), uploads.sent.toList())
    assertEquals("look at [attachment 1] as you see", state.draft)
    assertEquals(TextRange(8 + "[attachment 1]".length), state.selection)
    assertEquals(listOf("a.png"), state.attached.map { it.label })
    assertEquals("20261121-101500-1-a.png", state.attached.single().name)
    assertEquals(emptyList<String>(), state.uploading)
    assertEquals("", state.attachError)
  }

  @Test
  fun anAttachedFileIsKeptWithTheDraft() = runBlocking {
    val held = Held()
    val model = chat(Posts(), Uploaded(), held)
    model.attachNow(listOf(pick("a.png"), pick("b.zip")))
    assertEquals(model.state.value.attached, held.files)
    assertEquals(2, held.files.size)
  }

  @Test
  fun filesPickedTogetherAreNumberedInOrderAtTheMovingCursor() = runBlocking {
    val model = chat(Posts(), Uploaded())
    model.attachNow(listOf(pick("a.png"), pick("b.zip")))
    val state = model.state.value
    assertEquals("[attachment 1][attachment 2]", state.draft)
    assertEquals(listOf("a.png", "b.zip"), state.attached.map { it.label })
    assertEquals(TextRange(state.draft.length), state.selection)
  }

  @Test
  fun aFileMovesTheTokenToWhereTheCursorIsNow() = runBlocking {
    val model = chat(Posts(), Uploaded())
    model.attachNow(listOf(pick("a.png")))
    model.edit("[attachment 1] then words", TextRange(0))
    model.attachNow(listOf(pick("b.png")))
    assertEquals("[attachment 2][attachment 1] then words", model.state.value.draft)
  }

  @Test
  fun aFileOverTenMibIsRefusedBeforeItIsReadOrSent() = runBlocking {
    val uploads = Uploaded()
    var read = false
    val model = chat(Posts(), uploads)
    model.attachNow(listOf({ Picked("big.bin", MAX_UPLOAD_BYTES + 1) { read = true; ByteArray(0) } }))
    val state = model.state.value
    assertEquals("big.bin is over 10 MiB.", state.attachError)
    assertFalse(read)
    assertEquals(0, uploads.sent.size)
    assertEquals(emptyList<Attached>(), state.attached)
    assertEquals("", state.draft)
  }

  @Test
  fun aFileOfExactlyTenMibGoesUp() = runBlocking {
    val uploads = Uploaded()
    val model = chat(Posts(), uploads)
    model.attachNow(listOf(pick("edge.bin", MAX_UPLOAD_BYTES, MAX_UPLOAD_BYTES.toInt())))
    assertEquals(1, uploads.sent.size)
    assertEquals("", model.state.value.attachError)
    assertEquals(1, model.state.value.attached.size)
  }

  @Test
  fun aFileWhoseSizeWasUnknownAndIsOverTenMibIsRefusedAfterTheRead() = runBlocking {
    val uploads = Uploaded()
    val model = chat(Posts(), uploads)
    model.attachNow(listOf(pick("grew.bin", -1, MAX_UPLOAD_BYTES.toInt() + 1)))
    assertEquals("grew.bin is over 10 MiB.", model.state.value.attachError)
    assertEquals(0, uploads.sent.size)
    assertEquals(emptyList<String>(), model.state.value.uploading)
  }

  @Test
  fun theTenthFileIsTakenAndTheEleventhIsRefused() = runBlocking {
    val uploads = Uploaded()
    val model = chat(Posts(), uploads)
    model.attachNow((1..10).map { pick("f$it.png") })
    assertEquals(10, model.state.value.attached.size)
    assertEquals("", model.state.value.attachError)
    model.attachNow(listOf(pick("extra.png")))
    val state = model.state.value
    assertEquals(10, state.attached.size)
    assertEquals(10, uploads.sent.size)
    assertEquals("At most 10 files in one message.", state.attachError)
    assertFalse(state.draft.contains("[attachment 11]"))
  }

  @Test
  fun twoPicksAtOnceStillStopAtTheLimit() = runBlocking {
    val uploads = Uploaded(answer = { Thread.sleep(50); stored(it) })
    val model = chat(Posts(), uploads)
    model.attachNow((1..9).map { pick("a$it.png") })
    val first = launch(Dispatchers.Default) { model.attachNow(listOf(pick("x.png"))) }
    val second = launch(Dispatchers.Default) { model.attachNow(listOf(pick("y.png"))) }
    first.join()
    second.join()
    assertEquals(10, model.state.value.attached.size)
    assertEquals(10, uploads.sent.size)
    assertEquals("At most 10 files in one message.", model.state.value.attachError)
    assertEquals(
      (1..10).map { "[attachment $it]" }.joinToString(""),
      model.state.value.draft
    )
  }

  @Test
  fun aRefusedUploadLeavesNoChipAndNoToken() = runBlocking {
    val uploads = Uploaded(answer = { Raw(413, """{"error":{"status":413,"code":"too_large","message":"x"}}""") })
    val model = chat(Posts(), uploads)
    model.edit("hello", TextRange(5))
    model.attachNow(listOf(pick("a.png")))
    val state = model.state.value
    assertEquals("a.png is over 10 MiB.", state.attachError)
    assertEquals("hello", state.draft)
    assertEquals(emptyList<Attached>(), state.attached)
    assertEquals(emptyList<String>(), state.uploading)
  }

  @Test
  fun aLostUploadSaysSoAndKeepsNothing() = runBlocking {
    val uploads = Uploaded(answer = { throw BoardError.Lost })
    val model = chat(Posts(), uploads)
    model.attachNow(listOf(pick("a.png")))
    assertEquals("Could not upload a.png.", model.state.value.attachError)
    assertEquals(emptyList<Attached>(), model.state.value.attached)
  }

  @Test
  fun anUnpairedUploadMarksTheFleetUnpaired() = runBlocking {
    val uploads = Uploaded(answer = { throw BoardError.Unpaired })
    val model = chat(Posts(), uploads)
    model.attachNow(listOf(pick("a.png")))
    assertTrue(model.state.value.unpaired)
  }

  @Test
  fun anUnreadableFileIsOneErrorAndTheNextOneStillGoesUp() = runBlocking {
    val uploads = Uploaded()
    val model = chat(Posts(), uploads)
    model.attachNow(listOf({ throw java.io.IOException("gone") }, pick("b.png")))
    val state = model.state.value
    assertEquals(listOf("b.png"), state.attached.map { it.label })
    assertEquals(1, uploads.sent.size)
  }

  @Test
  fun aWrongTokenIsFetchedAgainAndTheUploadRetried() = runBlocking {
    var calls = 0
    val uploads =
      Uploaded(answer = {
        calls++
        if (calls == 1) Raw(403, """{"error":{"status":403,"code":"csrf_required","message":"x"}}""")
        else stored(it)
      })
    val model = chat(Posts(), uploads)
    model.attachNow(listOf(pick("a.png")))
    assertEquals(2, uploads.sent.size)
    assertEquals(1, model.state.value.attached.size)
  }

  @Test
  fun aChipInFlightShowsAsUploadingAndSendWaitsForIt() = runBlocking {
    val gate = CompletableDeferred<Unit>()
    val board = Posts()
    val model = chat(board, Uploaded(gate = gate))
    model.edit("hi", TextRange(2))
    val running = launch(Dispatchers.Default) { model.attachNow(listOf(pick("a.png"))) }
    try {
      withTimeout(5_000) { while (model.state.value.uploading.isEmpty()) delay(5) }
      assertEquals(listOf("a.png"), model.state.value.uploading)
      model.sendNow()
      assertEquals(0, board.bodies.size)
    } finally {
      gate.complete(Unit)
    }
    running.join()
    assertEquals(emptyList<String>(), model.state.value.uploading)
    assertEquals(1, model.state.value.attached.size)
  }

  @Test
  fun removingAChipDropsItsTokenAndRenumbersTheRest() = runBlocking {
    val held = Held()
    val model = chat(Posts(), Uploaded(), held)
    model.attachNow(listOf(pick("a.png"), pick("b.png"), pick("c.png")))
    model.edit("x [attachment 1] y [attachment 2] z [attachment 3]", TextRange(0))
    model.detach(2)
    val state = model.state.value
    assertEquals("x [attachment 1] y  z [attachment 2]", state.draft)
    assertEquals(listOf("a.png", "c.png"), state.attached.map { it.label })
    assertEquals(listOf("a.png", "c.png"), held.files.map { it.label })
  }

  @Test
  fun removingAChipThatIsNotThereChangesNothing() = runBlocking {
    val model = chat(Posts(), Uploaded())
    model.attachNow(listOf(pick("a.png")))
    val before = model.state.value
    model.detach(0)
    model.detach(2)
    assertEquals(before, model.state.value)
  }

  @Test
  fun theMessageNamesTheFilesInOrderAndTheBoxEmptiesOnceItIsSent() = runBlocking {
    val board = Posts()
    val held = Held()
    val model = chat(board, Uploaded(), held)
    model.edit("see  and ", TextRange(4))
    model.attachNow(listOf(pick("a.png"), pick("b.zip")))
    model.sendNow()
    val body = boardJson.parseToJsonElement(board.bodies.single().orEmpty()).jsonObject
    assertEquals("see [attachment 1][attachment 2] and ", body.getValue("text").jsonPrimitive.content)
    assertEquals(
      listOf("20261121-101500-1-a.png", "20261121-101500-1-b.zip"),
      body.getValue("attachments").jsonArray.map { it.jsonPrimitive.content }
    )
    val state = model.state.value
    assertEquals(emptyList<Attached>(), state.attached)
    assertEquals("", state.draft)
    assertEquals(emptyList<Attached>(), held.files)
  }

  @Test
  fun aMessageWithoutFilesCarriesNoAttachmentsField() = runBlocking {
    val board = Posts()
    val model = chat(board, Uploaded())
    model.edit("plain")
    model.sendNow()
    val body = boardJson.parseToJsonElement(board.bodies.single().orEmpty()).jsonObject
    assertNull(body["attachments"])
  }

  @Test
  fun filesAloneAreSentWithAnEmptyText() = runBlocking {
    val board = Posts()
    val model = chat(board, Uploaded())
    model.attachNow(listOf(pick("a.png")))
    model.edit("", TextRange.Zero)
    model.sendNow()
    assertEquals(1, board.bodies.size)
    val body = boardJson.parseToJsonElement(board.bodies.single().orEmpty()).jsonObject
    assertEquals(1, body.getValue("attachments").jsonArray.size)
  }

  @Test
  fun aFailedSendKeepsTheChips() = runBlocking {
    val board = Posts()
    board.answer = Raw(404, """{"error":{"status":404,"code":"not_found","message":"not found: a.png in inbox/"}}""")
    val model = chat(board, Uploaded())
    model.attachNow(listOf(pick("a.png")))
    model.sendNow()
    val state = model.state.value
    assertEquals(1, state.attached.size)
    assertEquals("not found: a.png in inbox/", state.sendError)
    assertTrue(state.draft.contains("[attachment 1]"))
  }

  @Test
  fun aChipStaysWhileTheMessageIsGoingOut() = runBlocking {
    val board = Posts()
    val model = chat(board, Uploaded())
    model.attachNow(listOf(pick("a.png")))
    val gate = CompletableDeferred<Unit>()
    board.gate = gate
    val sending = launch(Dispatchers.Default) { model.sendNow() }
    try {
      withTimeout(5_000) { while (board.bodies.isEmpty()) delay(5) }
      model.detach(1)
      model.attachNow(listOf(pick("b.png")))
      assertEquals(1, model.state.value.attached.size)
      assertEquals("a.png", model.state.value.attached.single().label)
    } finally {
      gate.complete(Unit)
    }
    sending.join()
    assertEquals(emptyList<Attached>(), model.state.value.attached)
  }

  @Test
  fun filesSentWhileTheTextChangedAreStillGoneFromTheBox() = runBlocking {
    val board = Posts()
    val model = chat(board, Uploaded())
    model.attachNow(listOf(pick("a.png")))
    val gate = CompletableDeferred<Unit>()
    board.gate = gate
    val sending = launch(Dispatchers.Default) { model.sendNow() }
    try {
      withTimeout(5_000) { while (board.bodies.isEmpty()) delay(5) }
      model.edit("a new thought")
    } finally {
      gate.complete(Unit)
    }
    sending.join()
    assertEquals(emptyList<Attached>(), model.state.value.attached)
    assertEquals("a new thought", model.state.value.draft)
  }

  @Test
  fun typingClearsTheAttachError() = runBlocking {
    val model = chat(Posts(), Uploaded(answer = { throw BoardError.Lost }))
    model.attachNow(listOf(pick("a.png")))
    assertFalse(model.state.value.attachError.isEmpty())
    model.edit("x")
    assertEquals("", model.state.value.attachError)
  }

  @Test
  fun aRestoredDraftBringsItsChipsBack() {
    val held = Held()
    held.text = "see [attachment 1]"
    held.files = listOf(Attached("n-a.png", "a.png", "image", 7))
    val model = chat(Posts(), Uploaded(), held)
    model.restoreDraft()
    val state = model.state.value
    assertEquals("see [attachment 1]", state.draft)
    assertEquals(listOf("a.png"), state.attached.map { it.label })
    assertEquals(TextRange(state.draft.length), state.selection)
  }

  @Test
  fun chipsAloneAreRestoredWithAnEmptyBox() {
    val held = Held()
    held.files = listOf(Attached("n-a.png", "a.png", "image", 7))
    val model = chat(Posts(), Uploaded(), held)
    model.restoreDraft()
    assertEquals(1, model.state.value.attached.size)
  }

  @Test
  fun heldFilesRoundTripAndExpireWithTheDraft() {
    val files = listOf(Attached("n-a.png", "a.png", "image", 7), Attached("n-b.zip", "b.zip", "file", 9))
    val raw = encodeFiles(files, 1_000L)
    assertEquals(files, decodeFiles(raw, 1_099L, 100L))
    assertEquals(emptyList<Attached>(), decodeFiles(raw, 1_100L, 100L))
    assertEquals(emptyList<Attached>(), decodeFiles("garbage", 1_000L, 100L))
    assertEquals(emptyList<Attached>(), decodeFiles("1000\n{not json", 1_000L, 100L))
  }
}
