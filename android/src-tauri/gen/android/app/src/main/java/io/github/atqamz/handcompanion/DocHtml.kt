package io.github.atqamz.handcompanion

import android.webkit.RenderProcessGoneDetail
import android.webkit.WebResourceRequest
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.AndroidView

const val HTML_LIMIT = 8L * 1024 * 1024

const val HTML_POLICY =
  "default-src 'none'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'"

private const val HTML_META = "<meta http-equiv=\"Content-Security-Policy\" content=\"$HTML_POLICY\">"

private fun commentEnd(source: String, start: Int): Int? {
  val body = start + 4
  if (source.startsWith(">", body)) return body + 1
  if (source.startsWith("->", body)) return body + 2
  var at = source.indexOf("--", body)
  while (at >= 0) {
    if (source.startsWith(">", at + 2)) return at + 3
    if (source.startsWith("!>", at + 2)) return at + 4
    at = source.indexOf("--", at + 1)
  }
  return null
}

private fun headLength(source: String): Int {
  var at = if (source.startsWith("\uFEFF")) 1 else 0
  while (at < source.length) {
    if (source[at] in " \t\n\r\u000C") {
      at++
    } else if (source.startsWith("<!--", at)) {
      at = commentEnd(source, at) ?: return 0
    } else {
      break
    }
  }
  if (!source.startsWith("<!doctype", at, ignoreCase = true)) return 0
  val end = source.indexOf('>', at)
  return if (end < 0) 0 else end + 1
}

fun htmlPage(source: String): String {
  val head = headLength(source)
  return source.substring(0, head) + HTML_META + source.substring(head)
}

interface PreviewSettings {
  var javaScriptEnabled: Boolean
  var allowFileAccess: Boolean
  var allowContentAccess: Boolean
  var blockNetworkLoads: Boolean
  var domStorageEnabled: Boolean
  var javaScriptCanOpenWindowsAutomatically: Boolean
  var supportMultipleWindows: Boolean
  var cacheMode: Int
}

interface PreviewTarget {
  val settings: PreviewSettings

  fun client(client: WebViewClient)

  fun dropDownloads()

  fun load(page: String)
}

fun prepare(target: PreviewTarget, gone: () -> Unit, page: String) {
  lockDown(target.settings)
  target.client(PreviewClient(gone))
  target.dropDownloads()
  target.load(page)
}

fun lockDown(settings: PreviewSettings) {
  settings.javaScriptEnabled = false
  settings.allowFileAccess = false
  settings.allowContentAccess = false
  settings.blockNetworkLoads = true
  settings.domStorageEnabled = false
  settings.javaScriptCanOpenWindowsAutomatically = false
  settings.supportMultipleWindows = false
  settings.cacheMode = WebSettings.LOAD_NO_CACHE
}

private class WebPreviewSettings(private val web: WebSettings) : PreviewSettings {
  override var javaScriptEnabled by web::javaScriptEnabled
  override var allowFileAccess by web::allowFileAccess
  override var allowContentAccess by web::allowContentAccess
  override var blockNetworkLoads by web::blockNetworkLoads
  override var domStorageEnabled by web::domStorageEnabled
  override var javaScriptCanOpenWindowsAutomatically by web::javaScriptCanOpenWindowsAutomatically
  override var supportMultipleWindows
    get() = web.supportMultipleWindows()
    set(value) = web.setSupportMultipleWindows(value)
  override var cacheMode by web::cacheMode
}

class PreviewClient(private val gone: () -> Unit) : WebViewClient() {
  override fun shouldOverrideUrlLoading(view: WebView?, request: WebResourceRequest?) = true

  override fun onRenderProcessGone(view: WebView?, detail: RenderProcessGoneDetail?): Boolean {
    gone()
    return true
  }
}

private class WebPreviewTarget(private val web: WebView) : PreviewTarget {
  override val settings: PreviewSettings = WebPreviewSettings(web.settings)

  override fun client(client: WebViewClient) {
    web.webViewClient = client
  }

  override fun dropDownloads() = web.setDownloadListener { _, _, _, _, _ -> }

  override fun load(page: String) = web.loadDataWithBaseURL(null, page, "text/html", "utf-8", null)
}

@Composable
fun HtmlPreview(page: String, gone: () -> Unit, modifier: Modifier) {
  AndroidView(
    {
      WebView(it).apply { prepare(WebPreviewTarget(this), gone, page) }
    },
    modifier,
    onRelease = WebView::destroy
  )
}
