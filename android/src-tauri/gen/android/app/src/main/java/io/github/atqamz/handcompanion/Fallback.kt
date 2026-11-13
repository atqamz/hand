package io.github.atqamz.handcompanion

import java.net.URI
import java.net.URISyntaxException
import kotlin.coroutines.coroutineContext
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

private const val NINETY_DAYS = 90 * 24 * 60 * 60
private val BROWSER = setOf("http", "https", "mailto", "tel")

const val NOT_PAIRED_HERE = "This phone is not paired with this fleet."
const val COOKIE_REFUSED = "Can't set the saved login for the web Board. Close this and try again."
const val BOARD_TIMED_OUT = "The web Board took too long to open. Try again."
const val OPEN_MILLIS = 30_000L

sealed interface Page {
  data class Failed(val message: String, val retry: Boolean = false) : Page

  data class Ready(val url: String, val port: Int) : Page
}

enum class Nav {
  Stay,
  Browser,
  Drop
}

fun interface Cookies {
  suspend fun set(url: String, cookie: String): Boolean
}

suspend fun openBoard(
  path: String,
  fallback: (String) -> String?,
  cookies: Cookies,
  timeoutMillis: Long = OPEN_MILLIS
): Page {
  val answer = CompletableDeferred<Page>()
  val worker =
    CoroutineScope(coroutineContext.minusKey(Job))
      .launch(start = CoroutineStart.UNDISPATCHED) {
        try {
          answer.complete(openPage(path, fallback, cookies))
        } catch (error: Throwable) {
          answer.completeExceptionally(error)
        }
      }
  try {
    return withTimeoutOrNull(timeoutMillis) { answer.await() }
      ?: run {
        answer.cancel()
        Page.Failed(BOARD_TIMED_OUT, retry = true)
      }
  } finally {
    worker.cancel()
  }
}

private suspend fun openPage(
  path: String,
  fallback: (String) -> String?,
  cookies: Cookies
): Page {
  val fleet = path.removePrefix("/").substringBefore('/')
  val answer = withContext(Dispatchers.IO) { parse(fallback(fleet)) }
  val port = answer.field("port")?.toIntOrNull()
  val cookie = answer.field("cookie")
  if (port == null || cookie == null) return Page.Failed(NOT_PAIRED_HERE)
  if (!cookies.set(COOKIE_URL, "$cookie; Path=/; Max-Age=$NINETY_DAYS; HttpOnly; SameSite=Lax")) {
    return Page.Failed(COOKIE_REFUSED)
  }
  return Page.Ready("http://localhost:$port$path", port)
}

fun navigation(
  url: String,
  port: Int,
  mainFrame: Boolean = true,
  gesture: Boolean = true,
  redirect: Boolean = false
): Nav {
  val uri =
    try {
      URI(url)
    } catch (_: URISyntaxException) {
      return Nav.Drop
    }
  val scheme = uri.scheme?.lowercase()
  return when {
    scheme == "http" && uri.host.equals("localhost", true) && uri.port == port -> Nav.Stay
    scheme in BROWSER && mainFrame && gesture && !redirect -> Nav.Browser
    else -> Nav.Drop
  }
}
