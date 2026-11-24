package io.github.atqamz.handcompanion

import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

private const val FLEET = "f000000000007"

private fun envelope(data: String) =
  Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":$data}""")

private fun fileNode(kind: String, text: Boolean, name: String = "a.md") =
  envelope("""{"path":"reports/$name","name":"$name","kind":"$kind","text":$text,"size":303}""")

private fun folderNode(page: Int = 1, pages: Int = 1, vararg names: String) =
  envelope(
    """{"path":"reports","name":"reports","kind":"folder","entries":[${
      names.joinToString(",") { """{"name":"$it","path":"reports/$it","kind":"file","label":"MD","size":12}""" }
    }],"page":$page,"pages":$pages,"total":${names.size}}"""
  )

private fun window(from: Int, lines: List<String>, next: Int? = null) =
  envelope(
    """{"path":"reports/a.txt","name":"a.txt","from":$from,"lines":[${
      lines.joinToString(",") { "\"$it\"" }
    }]${if (next != null) ""","next":$next""" else ""}}"""
  )

private fun refusal(status: Int, code: String, message: String) =
  Raw(status, """{"error":{"status":$status,"code":"$code","message":"$message"}}""")

private class DocBoard(var answer: (String) -> Raw) : Transport {
  val calls = CopyOnWriteArrayList<String>()

  override fun request(fleet: String, post: Boolean, path: String, csrf: String?, body: String?): Raw {
    calls += "${if (post) "POST" else "GET"} $path"
    return answer(path)
  }
}

@OptIn(ExperimentalCoroutinesApi::class)
class DocModelTest {
  @Before fun main() = Dispatchers.setMain(Dispatchers.Unconfined)

  @After fun reset() = Dispatchers.resetMain()

  private fun model(board: Transport, path: String = "reports/a.md") = DocModel(FLEET, path, board)

  @Test
  fun aFolderIsReadByItsEncodedPathAndAPageNumber() = runBlocking {
    val board = DocBoard { folderNode(1, 3, "a.md", "b.md") }
    val model = model(board, "reports/a b é")
    model.load()
    model.load(2)
    assertEquals(
      listOf("GET /api/v1/doc/reports/a%20b%20%C3%A9", "GET /api/v1/doc/reports/a%20b%20%C3%A9?page=2"),
      board.calls.toList()
    )
    assertEquals(DocBody.Folder, docBody(model.state.value))
    assertEquals(listOf("a.md", "b.md"), model.state.value.node?.entries?.map { it.name })
  }

  @Test
  fun markdownIsReadWhole_ThenParsedIntoBlocks() = runBlocking {
    val board =
      DocBoard {
        if (it.endsWith("?raw")) Raw(200, "# Title\nline one\nline two\n---\nafter\n")
        else fileNode("markdown", true)
      }
    val model = model(board)
    model.load()
    assertEquals(
      listOf("GET /api/v1/doc/reports/a.md", "GET /api/v1/doc/reports/a.md?raw"),
      board.calls.toList()
    )
    assertEquals(DocBody.Markdown, docBody(model.state.value))
    assertEquals(
      listOf("MdHeading", "MdParagraph", "MdRule", "MdParagraph"),
      model.state.value.blocks.map { it::class.simpleName }
    )
    assertEquals(2, (model.state.value.blocks[1] as MdParagraph).lines.size)
  }

  @Test
  fun textIsReadInWindowsAndMoreAppendsTheNextOne() = runBlocking {
    val board =
      DocBoard {
        when {
          it.endsWith("from=1&lines=500") -> window(1, listOf("one", "two"), next = 3)
          it.endsWith("from=3&lines=500") -> window(3, listOf("three"))
          else -> fileNode("text", true, "a.txt")
        }
      }
    val model = model(board, "reports/a.txt")
    model.load()
    assertEquals(DocBody.Text, docBody(model.state.value))
    assertEquals(listOf("one", "two"), model.state.value.lines)
    assertEquals(3, model.state.value.next)
    model.loadMore()
    assertEquals(listOf("one", "two", "three"), model.state.value.lines)
    assertNull(model.state.value.next)
    assertFalse(model.state.value.paging)
    model.loadMore()
    assertEquals(3, board.calls.size)
  }

  @Test
  fun csvAndTsvAreShownAsText_ButOtherKindsGetTheNoticeAndNoFurtherRead() = runBlocking {
    for (kind in listOf("csv", "tsv")) {
      val board = DocBoard { if (it.contains("from=")) window(1, listOf("a,b")) else fileNode(kind, true) }
      val model = model(board)
      model.load()
      assertEquals(kind, DocBody.Text, docBody(model.state.value))
    }
    for (kind in listOf("image", "svg", "html", "pdf", "video", "audio", "binary")) {
      val board = DocBoard { fileNode(kind, kind == "svg" || kind == "html") }
      val model = model(board)
      model.load()
      assertEquals(kind, DocBody.Notice, docBody(model.state.value))
      assertEquals(kind, 1, board.calls.size)
    }
    val board = DocBoard { fileNode("text", false) }
    val model = model(board)
    model.load()
    assertEquals(DocBody.Notice, docBody(model.state.value))
  }

  @Test
  fun aLateReadDoesNotOverwriteANewerOne() = runBlocking {
    val calls = AtomicInteger()
    val entered = CountDownLatch(1)
    val release = CountDownLatch(1)
    val board =
      DocBoard {
        if (calls.incrementAndGet() == 1) {
          entered.countDown()
          release.await()
          folderNode(1, 1, "old.md")
        } else {
          folderNode(1, 1, "new.md")
        }
      }
    val model = model(board, "reports")
    val first = launch(Dispatchers.Default) { model.load() }
    entered.await()
    model.load()
    assertEquals(listOf("new.md"), model.state.value.node?.entries?.map { it.name })
    release.countDown()
    first.join()
    assertEquals(listOf("new.md"), model.state.value.node?.entries?.map { it.name })
    assertFalse(model.state.value.loading)
  }

  @Test
  fun aLateFailureDoesNotOverwriteANewerSuccess() = runBlocking {
    val calls = AtomicInteger()
    val entered = CountDownLatch(1)
    val release = CountDownLatch(1)
    val board =
      DocBoard {
        if (calls.incrementAndGet() == 1) {
          entered.countDown()
          release.await()
          throw BoardError.Unreachable
        }
        folderNode(1, 1, "new.md")
      }
    val model = model(board, "reports")
    val first = launch(Dispatchers.Default) { model.load() }
    entered.await()
    model.load()
    release.countDown()
    first.join()
    assertNull(model.state.value.problem)
    assertEquals(DocBody.Folder, docBody(model.state.value))
  }

  @Test
  fun aLateMoreDoesNotAppendToAFreshRead() = runBlocking {
    val entered = CountDownLatch(1)
    val release = CountDownLatch(1)
    val board =
      DocBoard {
        when {
          it.endsWith("from=2&lines=500") -> {
            entered.countDown()
            release.await()
            window(2, listOf("stale"))
          }
          it.endsWith("from=1&lines=500") -> window(1, listOf("fresh"), next = 2)
          else -> fileNode("text", true, "a.txt")
        }
      }
    val model = model(board, "reports/a.txt")
    model.load()
    val more = launch(Dispatchers.Default) { model.loadMore() }
    entered.await()
    model.load()
    release.countDown()
    more.join()
    assertEquals(listOf("fresh"), model.state.value.lines)
    assertEquals(2, model.state.value.next)
    assertFalse(model.state.value.paging)
  }

  @Test
  fun anErrorIsClearedByALaterSuccess() = runBlocking {
    val board = DocBoard { throw BoardError.TimedOut }
    val model = model(board, "reports")
    model.load()
    assertEquals(DocBody.Failed, docBody(model.state.value))
    assertEquals("The Board did not answer in time.", model.state.value.problem)
    board.answer = { folderNode(1, 1, "a.md") }
    model.load()
    assertNull(model.state.value.problem)
    assertEquals(DocBody.Folder, docBody(model.state.value))
  }

  @Test
  fun aFailedReadKeepsTheContentAsStaleAndASuccessClearsTheMarker() = runBlocking {
    val board = DocBoard { if (it.endsWith("?raw")) Raw(200, "text") else fileNode("markdown", true) }
    val model = model(board)
    model.load()
    board.answer = { throw BoardError.Lost }
    model.load()
    val state = model.state.value
    assertEquals(DocBody.Markdown, docBody(state))
    assertEquals(1, state.blocks.size)
    assertEquals("The Board could not be reached.", state.problem)
    board.answer = { if (it.endsWith("?raw")) Raw(200, "again") else fileNode("markdown", true) }
    model.load()
    assertNull(model.state.value.problem)
  }

  @Test
  fun anUnreachableHostNeverDrawsAsTooOldOrUnpaired() = runBlocking {
    val failures =
      listOf(
        BoardError.Unreachable,
        BoardError.Lost,
        BoardError.TimedOut,
        BoardError.Busy,
        BoardError.NoHost,
        BoardError.BoardNotRunning,
        BoardError.Malformed,
        BoardError.TooLarge,
        BoardError.Internal("boom"),
        BoardError.Other(500, null, null),
        BoardError.Other(502, "bad_gateway", "x"),
        BoardError.UnsupportedVersion(2)
      )
    for (failure in failures) {
      val model = model(DocBoard { throw failure })
      model.load()
      val state = model.state.value
      assertFalse(failure.toString(), state.tooOld)
      assertFalse(failure.toString(), state.unpaired)
      assertNull(failure.toString(), state.refused)
      assertNotNull(failure.toString(), state.problem)
      assertEquals(failure.toString(), DocBody.Failed, docBody(state))
    }
  }

  @Test
  fun anOlderHostIsToldApartFromAMissingFileAndTheBoardsSentenceIsKept() = runBlocking {
    val old = model(DocBoard { Raw(404, "404 page not found") })
    old.load()
    assertTrue(old.state.value.tooOld)
    assertEquals(DocBody.TooOld, docBody(old.state.value))
    val noRoute = model(DocBoard { Raw(405, "") })
    noRoute.load()
    assertTrue(noRoute.state.value.tooOld)
    val sentence = "this file has more than one hard link, so the Board does not show it; copy it to show it"
    for ((raw, message) in
      listOf(
        refusal(403, "forbidden", sentence) to sentence,
        refusal(404, "not_found", "no such file") to "no such file",
        refusal(409, "conflict", "changed while opened") to "changed while opened",
        refusal(400, "bad_request", "not text") to "not text"
      )) {
      val model = model(DocBoard { raw })
      model.load()
      assertFalse(model.state.value.tooOld)
      assertEquals(message, model.state.value.refused)
      assertEquals(DocBody.Refused, docBody(model.state.value))
    }
    val blank = model(DocBoard { refusal(403, "forbidden", "") })
    blank.load()
    assertEquals("The Board does not show reports/a.md.", blank.state.value.refused)
  }

  @Test
  fun anUnpairedCompanionIsToldSo_AndRetryReadsAgain() = runBlocking {
    val board = DocBoard { throw BoardError.Unpaired }
    val model = model(board, "reports")
    model.load()
    assertEquals(DocBody.Unpaired, docBody(model.state.value))
    board.answer = { folderNode(1, 1, "a.md") }
    model.load()
    assertFalse(model.state.value.unpaired)
  }

  @Test
  fun aRefusedReadIsLeftByARetryThatSucceeds() = runBlocking {
    val board = DocBoard { refusal(403, "forbidden", "hidden") }
    val model = model(board, "reports")
    model.load()
    assertEquals(DocBody.Refused, docBody(model.state.value))
    board.answer = { folderNode(1, 1, "a.md") }
    model.load()
    assertNull(model.state.value.refused)
    assertEquals(DocBody.Folder, docBody(model.state.value))
  }

  @Test
  fun everyStartClearsTheEarlierRefusalOldHostAndProblemEvenWithContentHeld() = runBlocking {
    val board = DocBoard { folderNode(1, 1, "a.md") }
    val model = model(board, "reports")
    model.load()
    board.answer = { refusal(403, "forbidden", "hidden") }
    model.load()
    assertEquals("hidden", model.state.value.refused)
    board.answer = { throw BoardError.Lost }
    model.load()
    assertNull(model.state.value.refused)
    assertFalse(model.state.value.tooOld)
    assertEquals(DocBody.Folder, docBody(model.state.value))
    board.answer = { Raw(404, "not found") }
    model.load()
    assertNull(model.state.value.problem)
    assertTrue(model.state.value.tooOld)
    board.answer = { throw BoardError.Lost }
    model.load()
    assertFalse(model.state.value.tooOld)
    assertEquals("The Board could not be reached.", model.state.value.problem)
  }

  @Test
  fun aSecondLoadReadsAgainAndReplacesWhatWasHeld() = runBlocking {
    val board = DocBoard { folderNode(2, 3, "a.md") }
    val model = model(board, "reports")
    model.load(2)
    board.answer = { folderNode(1, 3, "b.md") }
    model.load()
    assertEquals(2, board.calls.size)
    assertEquals(1, model.state.value.node?.page)
    assertEquals("b.md", model.state.value.node?.entries?.single()?.name)
  }

  @Test
  fun everyStartClearsTheUnpairedFlag() = runBlocking {
    val board = DocBoard { throw BoardError.Unpaired }
    val model = model(board, "reports")
    model.load()
    assertTrue(model.state.value.unpaired)
    val seen = CopyOnWriteArrayList<DocState>()
    board.answer = { seen += model.state.value; throw BoardError.Lost }
    model.load()
    assertFalse(seen.single().unpaired)
    assertFalse(model.state.value.unpaired)
  }

  @Test
  fun everyStartClearsAMoreStillInFlight() = runBlocking {
    val started = CountDownLatch(1)
    val gate = CountDownLatch(1)
    val seen = CopyOnWriteArrayList<DocState>()
    lateinit var paged: DocModel
    val board = DocBoard {
      when {
        it.contains("from=2") -> {
          started.countDown()
          gate.await()
          window(2, listOf("b"))
        }
        it.contains("from=") -> window(1, listOf("a"), 2)
        else -> {
          seen += paged.state.value
          fileNode("text", true, "a.txt")
        }
      }
    }
    paged = model(board, "reports/a.txt")
    paged.load()
    val more = launch(Dispatchers.Default) { paged.loadMore() }
    started.await()
    assertTrue(paged.state.value.paging)
    paged.load()
    gate.countDown()
    more.join()
    assertFalse(seen.last().paging)
    assertFalse(paged.state.value.paging)
    assertEquals(1, paged.state.value.lines.size)
  }

  @Test
  fun aFailedMoreKeepsTheLinesAndOffersRetryOfThatMore() = runBlocking {
    var fail = false
    val board =
      DocBoard {
        when {
          it.endsWith("from=1&lines=500") -> window(1, listOf("one"), next = 2)
          it.endsWith("from=2&lines=500") -> if (fail) throw BoardError.Lost else window(2, listOf("two"))
          else -> fileNode("text", true, "a.txt")
        }
      }
    val model = model(board, "reports/a.txt")
    model.load()
    fail = true
    model.loadMore()
    assertEquals(listOf("one"), model.state.value.lines)
    assertEquals("The Board could not be reached.", model.state.value.moreProblem)
    assertNull(model.state.value.problem)
    assertFalse(model.state.value.paging)
    fail = false
    model.loadMore()
    assertEquals(listOf("one", "two"), model.state.value.lines)
    assertNull(model.state.value.moreProblem)
  }

  @Test
  fun aLineIsDrawnInert() {
    assertEquals("a    b", docLine("a\tb"))
    assertEquals("␛[31mred", docLine("\u001b[31mred"))
    assertEquals("x␡y", docLine("x\u007fy"))
    assertEquals("a�b�", docLine("a‮b⁧"))
    assertEquals("plain é 日本", docLine("plain é 日本"))
  }

  @Test
  fun aCutLineSaysHowMuchWasLeftOut() = runBlocking {
    val board =
      DocBoard {
        if (it.contains("from=")) {
          envelope("""{"path":"a.txt","name":"a.txt","from":1,"lines":["x","long"],"cut":[{"line":2,"bytes":9000}]}""")
        } else {
          fileNode("text", true, "a.txt")
        }
      }
    val model = model(board, "reports/a.txt")
    model.load()
    assertEquals(listOf("x", "long … 9000 more bytes"), model.state.value.lines)
  }

  @Test
  fun sizesAreShortWords() {
    assertEquals("0 B", docSize(0))
    assertEquals("1023 B", docSize(1023))
    assertEquals("1 KB", docSize(1024))
    assertEquals("1023 KB", docSize(1024 * 1024 - 1))
    assertEquals("1.5 MB", docSize(1572864))
  }

  @Test
  fun theRouteEncodesEverySegmentAndKeepsTheSlashes() {
    assertEquals("reports/a%20b/%C3%A9%2B.md", docRoute("reports/a b/é+.md"))
    assertEquals("a%3Fb%23c.md", docRoute("a?b#c.md"))
  }
}
