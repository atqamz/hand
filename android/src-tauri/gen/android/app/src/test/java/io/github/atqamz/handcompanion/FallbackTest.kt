package io.github.atqamz.handcompanion

import kotlin.coroutines.Continuation
import kotlin.coroutines.resume
import kotlin.coroutines.suspendCoroutine
import kotlin.concurrent.thread
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private const val FLEET = "f000000000003"
private const val COOKIE = "hand_phone_f000000000003=7.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

private class Jar(private val events: MutableList<String>, private val stores: Boolean = true) : Cookies {
  override suspend fun set(url: String, cookie: String): Boolean {
    events += "$url $cookie"
    return stores
  }
}

private class NeverJar : Cookies {
  override suspend fun set(url: String, cookie: String): Boolean = suspendCoroutine<Boolean> {}
}

private class LateJar : Cookies {
  private var waiting: Continuation<Boolean>? = null
  val reached = CompletableDeferred<Unit>()
  var resumed = false

  override suspend fun set(url: String, cookie: String): Boolean {
    reached.complete(Unit)
    val stored = suspendCoroutine { waiting = it }
    resumed = true
    return stored
  }

  fun resolve() {
    waiting?.resume(true)
  }
}

private class SpyJar(private val gate: CompletableDeferred<Unit>? = null) : CookieJar {
  val events = mutableListOf<String>()
  val threads = mutableListOf<Thread>()
  val reached = CompletableDeferred<Unit>()
  val flushing = CompletableDeferred<Unit>()
  lateinit var callbackThread: Thread
  private var waiting: ((Boolean) -> Unit)? = null

  override fun setCookie(url: String, cookie: String, done: (Boolean) -> Unit) {
    events += "setCookie"
    waiting = done
    reached.complete(Unit)
  }

  override fun flush() {
    events += "flush"
    threads += Thread.currentThread()
    flushing.complete(Unit)
    gate?.let { runBlocking { it.await() } }
  }

  fun answer() {
    callbackThread = Thread.currentThread()
    waiting?.invoke(true)
  }
}

class FallbackTest {
  @Test
  fun theFleetsCookieIsSetFromTheStoreBeforeThePageIsGiven() {
    val events = mutableListOf<String>()
    val fallback = { fleet: String ->
      events += "store $fleet"
      """{"port":50123,"cookie":"$COOKIE"}"""
    }
    val page = runBlocking { openBoard("/$FLEET/task/t3#r5", fallback, Jar(events)) }
    assertEquals(Page.Ready("http://localhost:50123/$FLEET/task/t3#r5", 50123), page)
    assertEquals(
      listOf(
        "store $FLEET",
        "http://localhost/ $COOKIE; Path=/; Max-Age=7776000; HttpOnly; SameSite=Lax"
      ),
      events
    )
  }

  @Test
  fun aCookieTheWebViewRefusesGivesNoPage() {
    val events = mutableListOf<String>()
    val page =
      runBlocking {
        openBoard("/$FLEET/", { """{"port":50123,"cookie":"$COOKIE"}""" }, Jar(events, stores = false))
      }
    assertEquals(Page.Failed(COOKIE_REFUSED), page)
  }

  @Test
  fun aFleetTheStoreLacksOpensNothingAndSetsNoCookie() {
    for (answer in listOf("{}", null, "", """{"port":50123}""", """{"cookie":"$COOKIE"}""")) {
      val events = mutableListOf<String>()
      val page = runBlocking { openBoard("/$FLEET/", { answer }, Jar(events)) }
      assertEquals(Page.Failed(NOT_PAIRED_HERE), page)
      assertTrue(events.toString(), events.isEmpty())
    }
  }

  @Test
  fun onlyThePageOfThePortLoadsInTheViewAndOnlyWebAddressesGoToTheBrowser() {
    val port = 49152
    val stay =
      listOf(
        "http://localhost:49152/",
        "http://localhost:49152",
        "http://localhost:49152/f0123456789ab/pair#v1.abc",
        "http://localhost:49152/static/app.js",
        "http://localhost:49152/f0123456789ab/task/t3#r5",
        "http://LOCALHOST:49152/"
      )
    val browser =
      listOf(
        "http://127.0.0.1:49152/",
        "http://localhost:49153/",
        "https://localhost:49152/",
        "http://localhost.:49152/",
        "http://evil.localhost:49152/",
        "http://evil.test/",
        "http://localhost:49152@evil.test/",
        "https://github.com/atqamz/hand",
        "mailto:board@example.test",
        "tel:+15550100"
      )
    val drop =
      listOf(
        "file:///data/user/0/io.github.atqamz.handcompanion/files/page.html",
        "content://io.github.atqamz.handcompanion.fileprovider/x",
        "handboard://c#v1.abc",
        "javascript:alert(1)",
        "intent://scan#Intent;end",
        "http://bad host/",
        ""
      )
    for (url in browser) {
      assertEquals(url, Nav.Drop, navigation(url, port, mainFrame = false, gesture = true))
      assertEquals(url, Nav.Drop, navigation(url, port, mainFrame = true, gesture = false))
    }
    for (url in browser) assertEquals(url, Nav.Drop, navigation(url, port, redirect = true))
    for (url in stay) assertEquals(url, Nav.Stay, navigation(url, port, redirect = true))
    for (url in stay) assertEquals(url, Nav.Stay, navigation(url, port, mainFrame = false, gesture = false))
    for (url in stay) assertEquals(url, Nav.Stay, navigation(url, port))
    for (url in browser) assertEquals(url, Nav.Browser, navigation(url, port))
    for (url in drop) assertEquals(url, Nav.Drop, navigation(url, port))
  }

  @Test(timeout = 2_000)
  fun anOpenThatNeverAnswersGivesTheTimeoutPageWithRetry() {
    val page =
      runBlocking {
        openBoard(
          "/$FLEET/",
          { """{"port":50123,"cookie":"$COOKIE"}""" },
          NeverJar(),
          timeoutMillis = 50
        )
      }
    assertEquals(Page.Failed(BOARD_TIMED_OUT, retry = true), page)
  }

  @Test(timeout = 2_000)
  fun aBridgeThatNeverAnswersGivesTheTimeoutPageWithRetry() = runBlocking {
    var page: Page? = null
    val jar = SpyJar()
    val opener = launch {
      page =
        openBoard(
          "/$FLEET/",
          { """{"port":50123,"cookie":"$COOKIE"}""" },
          Cookies { url, cookie -> storeCookie(jar, url, cookie) },
          timeoutMillis = 100
        )
    }
    withTimeout(1_000) { jar.reached.await() }
    opener.join()
    assertEquals(Page.Failed(BOARD_TIMED_OUT, retry = true), page)
    assertEquals(listOf("setCookie"), jar.events)
  }

  @Test(timeout = 3_000)
  fun cancellingTheBridgeReleasesTheParkedCall() = runBlocking {
    val jar = SpyJar()
    val job = CoroutineScope(Dispatchers.Default).launch { storeCookie(jar, COOKIE_URL, COOKIE) }
    withTimeout(1_000) { jar.reached.await() }
    job.cancel()
    withTimeout(1_000) { job.join() }
    assertEquals(listOf("setCookie"), jar.events)
  }

  @Test(timeout = 3_000)
  fun aCookieThatArrivesAfterTheDeadlineWritesNothing() = runBlocking {
    var page: Page? = null
    val jar = SpyJar()
    val opener = launch {
      page =
        openBoard(
          "/$FLEET/",
          { """{"port":50123,"cookie":"$COOKIE"}""" },
          Cookies { url, cookie -> storeCookie(jar, url, cookie) },
          timeoutMillis = 100
        )
    }
    withTimeout(1_000) { jar.reached.await() }
    opener.join()
    assertEquals(Page.Failed(BOARD_TIMED_OUT, retry = true), page)
    assertEquals(listOf("setCookie"), jar.events)
    jar.answer()
    delay(50)
    assertEquals(listOf("setCookie"), jar.events)
    assertEquals(Page.Failed(BOARD_TIMED_OUT, retry = true), page)
  }

  @Test(timeout = 5_000)
  fun theFlushRunsOffTheCallbackThreadAndBeforeThePageIsReady() = runBlocking {
    var page: Page? = null
    val gate = CompletableDeferred<Unit>()
    val jar = SpyJar(gate)
    val opener = launch {
      page =
        openBoard(
          "/$FLEET/",
          { """{"port":50123,"cookie":"$COOKIE"}""" },
          Cookies { url, cookie -> storeCookie(jar, url, cookie) }
        )
    }
    withTimeout(1_000) { jar.reached.await() }
    val callback = thread(name = "cookie-callback") { jar.answer() }
    withTimeout(1_000) { jar.flushing.await() }
    delay(50)
    assertNull(page)
    gate.complete(Unit)
    opener.join()
    callback.join()
    assertEquals(Page.Ready("http://localhost:50123/$FLEET/", 50123), page)
    assertEquals(listOf("setCookie", "flush"), jar.events)
    assertNotEquals(jar.callbackThread, jar.threads.single())
  }

  @Test(timeout = 3_000)
  fun aPageThatResolvesAfterTheTimeoutDoesNotReplaceIt() = runBlocking {
    var page: Page? = null
    val jar = LateJar()
    val opener = launch {
      page =
        openBoard(
          "/$FLEET/",
          { """{"port":50123,"cookie":"$COOKIE"}""" },
          jar,
          timeoutMillis = 500
        )
    }
    withTimeout(1_000) { jar.reached.await() }
    withTimeout(2_000) { while (page == null) delay(5) }
    assertEquals(Page.Failed(BOARD_TIMED_OUT, retry = true), page)
    jar.resolve()
    opener.join()
    withTimeout(1_000) { while (!jar.resumed) delay(5) }
    delay(50)
    assertEquals(Page.Failed(BOARD_TIMED_OUT, retry = true), page)
  }
}
