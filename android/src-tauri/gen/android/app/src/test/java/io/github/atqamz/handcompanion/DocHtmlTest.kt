package io.github.atqamz.handcompanion

import android.webkit.WebResourceRequest
import android.webkit.WebSettings
import android.webkit.WebViewClient
import java.util.concurrent.CopyOnWriteArrayList
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

private const val FLEET = "f000000000007"

private val META = "<meta http-equiv=\"Content-Security-Policy\" content=\"$HTML_POLICY\">"

private fun pageNode(size: Long) =
  Raw(
    200,
    """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":{"path":"reports/page one.html","name":"page one.html","kind":"html","text":true,"size":$size}}"""
  )

private fun pageWindow() =
  Raw(
    200,
    """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":{"path":"reports/page one.html","name":"page one.html","from":1,"lines":["<p>one</p>","<p>two</p>"]}}"""
  )

private class PageBoard(val size: Long) : Transport {
  val calls = CopyOnWriteArrayList<String>()

  override fun request(fleet: String, post: Boolean, path: String, csrf: String?, body: String?): Raw {
    calls += path
    return if (path.contains("from=")) pageWindow() else pageNode(size)
  }
}

private class Settings : PreviewSettings {
  override var javaScriptEnabled = true
  override var allowFileAccess = true
  override var allowContentAccess = true
  override var blockNetworkLoads = false
  override var domStorageEnabled = true
  override var javaScriptCanOpenWindowsAutomatically = true
  override var supportMultipleWindows = true
  override var cacheMode = WebSettings.LOAD_DEFAULT
}

@OptIn(ExperimentalCoroutinesApi::class)
class DocHtmlTest {
  @Before fun main() = Dispatchers.setMain(Dispatchers.Unconfined)

  @After fun reset() = Dispatchers.resetMain()

  private val fetched = CopyOnWriteArrayList<String>()

  private fun model(size: Long = 2000, answer: ByteArray? = "<h1>hi</h1>".toByteArray(), board: PageBoard = PageBoard(size)) =
    DocModel(
      FLEET,
      "reports/page one.html",
      board,
      files =
        FileSource { fleet, route ->
          fetched += "$fleet $route"
          answer
        }
    )

  @Test
  fun thePageIsFetchedFramedAndPrefixedWithThePolicy() = runBlocking {
    val model = model()
    model.load()
    assertEquals(listOf("$FLEET /api/v1/doc/reports/page%20one.html?raw&frame"), fetched.toList())
    assertEquals(htmlPage("<h1>hi</h1>"), model.state.value.html)
    assertTrue(model.state.value.html!!.startsWith("<meta http-equiv=\"Content-Security-Policy\""))
    assertEquals(DocBody.Html, docBody(model.state.value))
    assertEquals(listOf("<p>one</p>", "<p>two</p>").size, model.state.value.lines.size)
  }

  @Test
  fun theSourceIsTheFallbackAndTheToggleSwitchesBetweenTheTwo() = runBlocking {
    val model = model()
    model.load()
    model.toggleRendered()
    assertFalse(model.state.value.rendered)
    assertEquals(DocBody.Text, docBody(model.state.value))
    model.toggleRendered()
    assertEquals(DocBody.Html, docBody(model.state.value))
  }

  @Test
  fun aPageAtTheLimitRendersAndOneByteOverShowsSourceWithANoticeWithoutAFetch() = runBlocking {
    model(HTML_LIMIT).also { it.load() }
    assertEquals(1, fetched.size)
    fetched.clear()
    val over = model(HTML_LIMIT + 1)
    over.load()
    assertTrue(fetched.isEmpty())
    assertNull(over.state.value.html)
    assertEquals(DocBody.Text, docBody(over.state.value))
    assertTrue(over.state.value.notice!!.detail.contains("8.0 MB"))
  }

  @Test
  fun aBodyLargerThanTheLimitIsRefusedWhateverTheMetaSaid() = runBlocking {
    val model = model(10, ByteArray((HTML_LIMIT + 1).toInt()))
    model.load()
    assertNull(model.state.value.html)
    assertEquals(DocBody.Text, docBody(model.state.value))
  }

  @Test
  fun anEmptyOrMissingAnswerShowsTheSourceWithANotice() = runBlocking {
    for (answer in listOf(null, ByteArray(0))) {
      val model = model(answer = answer)
      model.load()
      assertNull(model.state.value.html)
      assertEquals("The preview could not be loaded.", model.state.value.notice?.title)
      assertEquals(DocBody.Text, docBody(model.state.value))
    }
  }

  @Test
  fun aLaterSuccessClearsThePreviewNotice() = runBlocking {
    val failing = model(answer = null)
    failing.load()
    assertEquals("The preview could not be loaded.", failing.state.value.notice?.title)
    val model = model()
    model.load()
    assertNull(model.state.value.notice)
  }

  @Test
  fun aRenderProcessThatIsGoneClosesTheViewAndShowsSourceWithANotice() = runBlocking {
    val model = model()
    model.load()
    model.viewGone()
    assertNull(model.state.value.html)
    assertEquals("The preview stopped working.", model.state.value.notice?.title)
    assertEquals(DocBody.Text, docBody(model.state.value))
    model.load()
    assertEquals(DocBody.Html, docBody(model.state.value))
    assertNull(model.state.value.notice)
  }

  @Test
  fun thePolicyAllowsNoScriptNoFrameAndNoRemoteLoadAndIsFirst() {
    val policy = HTML_POLICY.split(';').map { it.trim() }
    assertTrue(policy.contains("default-src 'none'"))
    assertTrue(policy.none { it.startsWith("script-src") })
    assertTrue(policy.contains("img-src data:"))
    assertTrue(policy.contains("base-uri 'none'"))
    assertTrue(policy.contains("form-action 'none'"))
    assertFalse(HTML_POLICY.contains("http"))
    assertFalse(HTML_POLICY.contains("*"))
    assertTrue(htmlPage("<p>x</p>").startsWith(META))
  }

  private fun assertMetaFirstOf(page: String, worker: String) {
    val at = page.indexOf(META)
    assertTrue(page, at >= 0)
    assertEquals(page, page.indexOf(META), page.lastIndexOf(META))
    assertEquals(worker, page.substring(0, at) + page.substring(at + META.length))
  }

  @Test
  fun aLeadingDoctypeStaysFirstAndThePolicyFollowsIt() {
    assertEquals("<!doctype html>$META<p>x</p>", htmlPage("<!doctype html><p>x</p>"))
    assertEquals("<!DOCTYPE html PUBLIC \"a\" \"b\">$META<html>", htmlPage("<!DOCTYPE html PUBLIC \"a\" \"b\"><html>"))
    assertEquals("\uFEFF \n<!-- c -->\t<!doctype html>$META<p>", htmlPage("\uFEFF \n<!-- c -->\t<!doctype html><p>"))
    assertEquals("<!--a--><!--b--><!doctype html>$META", htmlPage("<!--a--><!--b--><!doctype html>"))
    assertEquals("<!--a--!><!doctype html>$META", htmlPage("<!--a--!><!doctype html>"))
    assertEquals("<!doctype html <script>$META" + "x</script>", htmlPage("<!doctype html <script>x</script>"))
  }

  @Test
  fun thePolicyPrecedesEveryWorkerTagWhateverComesBeforeIt() {
    val hostile =
      listOf(
        "<script>x</script><!doctype html>",
        "<!doctype html><script>x</script>",
        "<!-- <script> --><script>x</script>",
        "<!--> <script>x</script><!doctype html>",
        "<!---> <script>x</script><!doctype html>",
        "<!-- unterminated <script>x</script><!doctype html>",
        "<!--a--!> <script>x</script><!doctype html>",
        "<p><!doctype html>",
        "<?xml version=\"1.0\"?><!doctype html>",
        "<!doctypex><script>x</script>",
        "",
        "   "
      )
    for (worker in hostile) {
      val page = htmlPage(worker)
      assertMetaFirstOf(page, worker)
      val tags = Regex("<script|<p>|<\\?xml").findAll(page).map { it.range.first }.toList()
      val at = page.indexOf(META)
      assertTrue(worker, tags.none { it < at })
    }
  }

  @Test
  fun aCommentThatClosesAtOnceDoesNotHideTheScriptAfterIt() {
    val page = htmlPage("<!--> <script>x</script> --><!doctype html>")
    assertTrue(page, page.indexOf(META) < page.indexOf("<script>"))
    val bang = htmlPage("<!-- a --!> <script>x</script> --><!doctype html>")
    assertTrue(bang, bang.indexOf(META) < bang.indexOf("<script>"))
  }

  @Test
  fun aHugeHostileHeadIsScannedInLinearTime() {
    val comments = "<!--a-->".repeat(200_000)
    val started = System.nanoTime()
    assertTrue(htmlPage(comments + "<p>x</p>").startsWith(META))
    assertTrue(htmlPage("$comments<!doctype html>").startsWith("$comments<!doctype html>$META"))
    assertTrue(htmlPage("<!--a-->".repeat(200_000) + "<!--").startsWith(META))
    assertTrue(htmlPage("-->".repeat(300_000)).startsWith(META))
    assertTrue(System.nanoTime() - started < 5_000_000_000L)
  }

  private class Target : PreviewTarget {
    val steps = mutableListOf<String>()
    val locked = Settings()
    var client: WebViewClient? = null
    var downloads = false
    var loaded: String? = null
    var lockedAtLoad: Boolean? = null

    override val settings: PreviewSettings = locked

    override fun client(client: WebViewClient) {
      steps += "client"
      this.client = client
    }

    override fun dropDownloads() {
      steps += "downloads"
      downloads = true
    }

    override fun load(page: String) {
      steps += "load"
      loaded = page
      lockedAtLoad = !locked.javaScriptEnabled && !locked.allowFileAccess && locked.blockNetworkLoads
    }
  }

  @Test
  fun preparingAViewLocksItDownSetsTheClientDropsDownloadsAndLoadsLast() {
    val target = Target()
    var gone = 0
    prepare(target, { gone++ }, "<p>x</p>")
    assertFalse(target.locked.javaScriptEnabled)
    assertFalse(target.locked.allowFileAccess)
    assertFalse(target.locked.allowContentAccess)
    assertTrue(target.locked.blockNetworkLoads)
    assertTrue(target.client is PreviewClient)
    assertTrue((target.client as PreviewClient).shouldOverrideUrlLoading(null, null as WebResourceRequest?))
    assertTrue(target.client!!.onRenderProcessGone(null, null))
    assertEquals(1, gone)
    assertTrue(target.downloads)
    assertEquals("<p>x</p>", target.loaded)
    assertEquals(true, target.lockedAtLoad)
    assertEquals("load", target.steps.last())
    assertEquals(setOf("client", "downloads", "load"), target.steps.toSet())
  }

  @Test
  fun theLockdownTurnsEveryOpenSettingOff() {
    val settings = Settings()
    lockDown(settings)
    assertFalse(settings.javaScriptEnabled)
    assertFalse(settings.allowFileAccess)
    assertFalse(settings.allowContentAccess)
    assertTrue(settings.blockNetworkLoads)
    assertFalse(settings.domStorageEnabled)
    assertFalse(settings.javaScriptCanOpenWindowsAutomatically)
    assertFalse(settings.supportMultipleWindows)
    assertEquals(WebSettings.LOAD_NO_CACHE, settings.cacheMode)
  }

  @Test
  fun everyNavigationIsDropped() {
    val client = PreviewClient {}
    assertTrue(client.shouldOverrideUrlLoading(null, null as WebResourceRequest?))
  }

  @Test
  fun aGoneRenderProcessIsReportedAndHandled() {
    var gone = 0
    val client = PreviewClient { gone++ }
    assertTrue(client.onRenderProcessGone(null, null))
    assertEquals(1, gone)
  }
}
