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

  private fun model(
    board: Transport,
    path: String = "reports/a.md",
    files: FileSource = FileSource { _, _ -> null }
  ) = DocModel(FLEET, path, board, files = files)

  private fun logBoard(total: Int, name: String = "a.txt") =
    DocBoard { path ->
      val from = Regex("from=(\\d+)").find(path)?.groupValues?.get(1)?.toInt()
      if (from == null) {
        fileNode("text", true, name)
      } else {
        val count = Regex("lines=(\\d+)").find(path)?.groupValues?.get(1)?.toInt() ?: DOC_LINES
        val last = minOf(total, from + count - 1)
        window(from, (from..last).map { "line $it" }, if (last < total) last + 1 else null)
      }
    }

  private fun sized(kind: String, size: Long, name: String) =
    envelope("""{"path":"reports/$name","name":"$name","kind":"$kind","text":false,"size":$size}""")

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
    for (kind in listOf("svg", "html", "pdf", "video", "audio", "binary")) {
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

  @Test
  fun anImageIsFetchedWholeByItsEncodedRawPath_AndNoWindowIsRead() = runBlocking {
    val asked = CopyOnWriteArrayList<String>()
    val board = DocBoard { sized("image", 2048, "p q.png") }
    val model =
      model(board, "reports/p q.png", FileSource { fleet, path ->
        asked += "$fleet $path"
        byteArrayOf(1, 2, 3)
      })
    model.load()
    assertEquals(DocBody.Image, docBody(model.state.value))
    assertEquals(listOf(1, 2, 3), model.state.value.image?.map { it.toInt() })
    assertEquals(listOf("$FLEET /api/v1/doc/reports/p%20q.png?raw"), asked.toList())
    assertEquals(listOf("GET /api/v1/doc/reports/p%20q.png"), board.calls.toList())
  }

  @Test
  fun anImageOverTheLimitIsNeverFetched() = runBlocking {
    val asked = AtomicInteger()
    val board = DocBoard { sized("image", IMAGE_LIMIT + 1, "big.png") }
    val model = model(board, "reports/big.png", FileSource { _, _ -> asked.incrementAndGet(); byteArrayOf(1) })
    model.load()
    assertEquals(DocBody.ImageTooLarge, docBody(model.state.value))
    assertEquals(0, asked.get())
    val atLimit = model(DocBoard { sized("image", IMAGE_LIMIT, "ok.png") }, "reports/ok.png", FileSource { _, _ -> byteArrayOf(1) })
    atLimit.load()
    assertEquals(DocBody.Image, docBody(atLimit.state.value))
  }

  @Test
  fun anImageThatFailsToLoadIsAProblemNotAnOldHostOrAnUnpairedCompanion_AndRetryClearsIt() = runBlocking {
    var bytes: ByteArray? = null
    val model = model(DocBoard { sized("image", 10, "p.png") }, "reports/p.png", FileSource { _, _ -> bytes })
    model.load()
    val failed = model.state.value
    assertEquals(DocBody.Failed, docBody(failed))
    assertNotNull(failed.problem)
    assertFalse(failed.tooOld)
    assertFalse(failed.unpaired)
    assertNull(failed.refused)
    bytes = byteArrayOf(9)
    model.load()
    assertNull(model.state.value.problem)
    assertEquals(DocBody.Image, docBody(model.state.value))
  }

  @Test
  fun copyingMarkdownGivesTheSourceItWasReadFromAndReadsNothingMore() = runBlocking {
    val source = "# Title\n\n**bold** and `code`\n"
    val board = DocBoard { if (it.endsWith("?raw")) Raw(200, source) else fileNode("markdown", true) }
    val model = model(board)
    model.load()
    val before = board.calls.size
    assertEquals(source, model.contents())
    assertEquals(before, board.calls.size)
  }

  @Test
  fun copyingTextReadsTheWholeFileRaw_AndAFailureIsSaidThenClearedByASuccess() = runBlocking {
    var answer: () -> Raw = { throw BoardError.Unreachable }
    val board =
      DocBoard {
        when {
          it.endsWith("?raw") -> answer()
          it.contains("from=") -> window(1, listOf("a"))
          else -> fileNode("text", true, "a.txt")
        }
      }
    val model = model(board, "reports/a.txt")
    model.load()
    assertNull(model.contents())
    assertEquals("The Board could not be reached.", model.state.value.copyProblem)
    answer = { throw BoardError.TooLarge }
    assertNull(model.contents())
    assertEquals("The file is too large to copy here.", model.state.value.copyProblem)
    answer = { Raw(200, "line 1\nline 2\n") }
    assertEquals("line 1\nline 2\n", model.contents())
    assertNull(model.state.value.copyProblem)
  }

  @Test
  fun wrapToggles_AndSurvivesARotationReloadAndARetry() = runBlocking {
    val model = model(logBoard(10), "reports/a.txt")
    model.load()
    assertTrue(model.state.value.wrap)
    model.toggleWrap()
    assertFalse(model.state.value.wrap)
    model.load()
    assertFalse(model.state.value.wrap)
    model.retry()
    assertFalse(model.state.value.wrap)
    model.toggleWrap()
    assertTrue(model.state.value.wrap)
  }

  @Test
  fun findInLoadedLinesReadsNothingAndCountsHits() = runBlocking {
    val board = logBoard(100)
    val model = model(board, "reports/a.txt")
    model.load()
    val before = board.calls.size
    model.search("LINE 1")
    val find = model.state.value.find
    assertEquals(before, board.calls.size)
    assertEquals(listOf(1) + (10..19) + listOf(100), find?.hits)
    assertEquals(0, find?.at)
    assertEquals(1, model.state.value.jump?.line)
  }

  @Test
  fun findReadsOnlyTheWindowsItNeedsAndStopsAtTheFirstHit() = runBlocking {
    val board = logBoard(5000)
    val model = model(board, "reports/a.txt")
    model.load()
    model.search("line 1200")
    val state = model.state.value
    assertEquals(listOf(1200), state.find?.hits)
    assertEquals(1200, state.jump?.line)
    assertEquals(
      listOf(
        "GET /api/v1/doc/reports/a.txt",
        "GET /api/v1/doc/reports/a.txt?from=1&lines=500",
        "GET /api/v1/doc/reports/a.txt?from=501&lines=500",
        "GET /api/v1/doc/reports/a.txt?from=1001&lines=500"
      ),
      board.calls.toList()
    )
    assertEquals(1501, state.next)
    assertEquals(1500, state.lines.size)
  }

  @Test
  fun aSearchThatFindsNothingStopsAfterTheWindowCapAndNextKeepsLooking() = runBlocking {
    val board = logBoard(50000)
    val model = model(board, "reports/a.txt")
    model.load()
    model.search("nowhere")
    assertEquals(2 + FIND_WINDOWS, board.calls.size)
    assertTrue(model.state.value.find!!.hits.isEmpty())
    assertNotNull(model.state.value.next)
    val read = board.calls.size
    model.move(true)
    assertEquals(read + FIND_WINDOWS, board.calls.size)
  }

  @Test
  fun nextAtTheLastLoadedHitReadsOnAndAtTheEndWrapsToTheFirst_PreviousWrapsToTheLast() = runBlocking {
    val board = logBoard(1200)
    val model = model(board, "reports/a.txt")
    model.load()
    model.search("line 4")
    assertEquals(listOf(4) + (40..49) + (400..499), model.state.value.find?.hits)
    model.move(true)
    assertEquals(1, model.state.value.find?.at)
    model.move(false)
    model.move(false)
    assertEquals(model.state.value.find!!.hits.size - 1, model.state.value.find?.at)
    assertEquals(499, model.state.value.jump?.line)
    model.move(true)
    assertNull(model.state.value.next)
    assertEquals(499, model.state.value.jump?.line)
    assertEquals(model.state.value.find!!.hits.size - 1, model.state.value.find?.at)
    model.move(true)
    assertEquals(0, model.state.value.find?.at)
    assertEquals(4, model.state.value.jump?.line)
  }

  @Test
  fun aLateFindAnswerDoesNotOverwriteANewerSearch() = runBlocking {
    val entered = CountDownLatch(1)
    val release = CountDownLatch(1)
    val board =
      DocBoard {
        when {
          it.endsWith("from=2&lines=500") -> {
            entered.countDown()
            release.await()
            window(2, listOf("zzz late"), 3)
          }
          it.endsWith("from=1&lines=500") -> window(1, listOf("alpha"), 2)
          else -> fileNode("text", true, "a.txt")
        }
      }
    val model = model(board, "reports/a.txt")
    model.load()
    val first = launch(Dispatchers.Default) { model.search("zzz") }
    entered.await()
    model.search("alpha")
    release.countDown()
    first.join()
    val state = model.state.value
    assertEquals("alpha", state.find?.query)
    assertEquals(listOf(1), state.find?.hits)
    assertEquals(listOf("alpha"), state.lines)
    assertEquals(2, state.next)
    assertFalse(state.paging)
  }

  @Test
  fun aFindFailureIsKeptAsTheSearchsProblemAndTheNextSuccessClearsIt() = runBlocking {
    var down = true
    val base = logBoard(2000)
    val board = DocBoard { if (down && it.contains("from=501")) throw BoardError.TimedOut else base.answer(it) }
    val model = model(board, "reports/a.txt")
    model.load()
    model.search("line 1500")
    assertEquals("The Board did not answer in time.", model.state.value.find?.problem)
    assertFalse(model.state.value.paging)
    down = false
    model.move(true)
    assertNull(model.state.value.find?.problem)
    assertEquals(listOf(1500), model.state.value.find?.hits)
  }

  @Test
  fun closingFindDropsAnAnswerStillInFlight() = runBlocking {
    val entered = CountDownLatch(1)
    val release = CountDownLatch(1)
    val board =
      DocBoard {
        when {
          it.endsWith("from=2&lines=500") -> {
            entered.countDown()
            release.await()
            window(2, listOf("late"), 3)
          }
          it.endsWith("from=1&lines=500") -> window(1, listOf("alpha"), 2)
          else -> fileNode("text", true, "a.txt")
        }
      }
    val model = model(board, "reports/a.txt")
    model.load()
    val search = launch(Dispatchers.Default) { model.search("zzz") }
    entered.await()
    model.closeFind()
    release.countDown()
    search.join()
    assertNull(model.state.value.find)
    assertEquals(listOf("alpha"), model.state.value.lines)
  }

  @Test
  fun goToInsideTheLoadedLinesReadsNothing_AndLineOneIsTheFirstLine() = runBlocking {
    val board = logBoard(2000)
    val model = model(board, "reports/a.txt")
    model.load()
    val before = board.calls.size
    model.jumpTo(1)
    assertEquals(DocJump(1, 1), model.state.value.jump)
    model.jumpTo(500)
    assertEquals(DocJump(500, 2), model.state.value.jump)
    assertEquals(before, board.calls.size)
  }

  @Test
  fun goToBeyondTheLoadedLinesReadsAWindowStartingAtThatLine() = runBlocking {
    val board = logBoard(2000)
    val model = model(board, "reports/a.txt")
    model.load()
    model.jumpTo(501)
    val state = model.state.value
    assertEquals("GET /api/v1/doc/reports/a.txt?from=501&lines=500", board.calls.last())
    assertEquals(501, state.first)
    assertEquals("line 501", state.lines.first())
    assertEquals(500, state.lines.size)
    assertEquals(1001, state.next)
    assertEquals(501, state.jump?.line)
    model.jumpTo(1700)
    assertEquals(1700, model.state.value.first)
    assertEquals("line 1700", model.state.value.lines.first())
  }

  @Test
  fun goToBeforeTheFirstLoadedLineReadsFromThatLine() = runBlocking {
    val board = logBoard(2000)
    val model = model(board, "reports/a.txt")
    model.load()
    model.jumpTo(900)
    model.jumpTo(300)
    assertEquals(300, model.state.value.first)
    assertEquals("line 300", model.state.value.lines.first())
  }

  @Test
  fun goToRefusesLineZeroAndALinePastTheEnd_AndASuccessClearsTheSentence() = runBlocking {
    val board = logBoard(10)
    val model = model(board, "reports/a.txt")
    model.load()
    val before = board.calls.size
    model.jumpTo(0)
    assertEquals("Line numbers start at 1.", model.state.value.goProblem)
    assertEquals(before, board.calls.size)
    model.jumpTo(11)
    assertEquals("The file has fewer than 11 lines.", model.state.value.goProblem)
    assertEquals(listOf("line 1"), model.state.value.lines.take(1))
    assertEquals(1, model.state.value.first)
    model.jumpTo(5)
    assertNull(model.state.value.goProblem)
    assertEquals(5, model.state.value.jump?.line)
  }

  @Test
  fun aGoToFailureIsSaidAndALaterSuccessClearsIt() = runBlocking {
    var down = true
    val base = logBoard(2000)
    val board = DocBoard { if (down && it.contains("from=900")) throw BoardError.Unreachable else base.answer(it) }
    val model = model(board, "reports/a.txt")
    model.load()
    model.jumpTo(900)
    assertEquals("The Board could not be reached.", model.state.value.goProblem)
    assertFalse(model.state.value.paging)
    assertEquals(1, model.state.value.first)
    down = false
    model.jumpTo(900)
    assertNull(model.state.value.goProblem)
    assertEquals(900, model.state.value.first)
  }

  @Test
  fun aLateGoToAnswerDoesNotReplaceANewerOne() = runBlocking {
    val entered = CountDownLatch(1)
    val release = CountDownLatch(1)
    val base = logBoard(3000)
    val board =
      DocBoard {
        if (it.contains("from=1000&")) {
          entered.countDown()
          release.await()
        }
        base.answer(it)
      }
    val model = model(board, "reports/a.txt")
    model.load()
    val slow = launch(Dispatchers.Default) { model.jumpTo(1000) }
    entered.await()
    model.jumpTo(2000)
    release.countDown()
    slow.join()
    assertEquals(2000, model.state.value.first)
    assertEquals(2000, model.state.value.jump?.line)
    assertFalse(model.state.value.paging)
  }

  @Test
  fun earlierLinesAreReadUpToTheLoadedWindowAndJoinItWithoutAGap() = runBlocking {
    val board = logBoard(3000)
    val model = model(board, "reports/a.txt")
    model.load()
    model.jumpTo(1200)
    model.loadEarlier()
    assertEquals("GET /api/v1/doc/reports/a.txt?from=700&lines=500", board.calls.last())
    assertEquals(700, model.state.value.first)
    assertEquals((700..1699).map { "line $it" }, model.state.value.lines)
    model.jumpTo(900)
    model.jumpTo(300)
    model.loadEarlier()
    assertEquals(1, model.state.value.first)
    assertEquals("GET /api/v1/doc/reports/a.txt?from=1&lines=299", board.calls.last())
    assertEquals(799, model.state.value.lines.size)
  }

  @Test
  fun matchesAreCaseInsensitiveAndDoNotOverlap() {
    assertEquals(listOf(0..1, 3..4), docMatches("Ab ab", "ab"))
    assertEquals(listOf(0..1), docMatches("aaa", "aa"))
    assertEquals(emptyList<IntRange>(), docMatches("abc", ""))
    assertEquals(listOf(3, 5), docHits(3, listOf("x", "y", "x"), "X"))
  }
}
