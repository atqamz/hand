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

fun htmlPage(source: String) = "<meta http-equiv=\"Content-Security-Policy\" content=\"$HTML_POLICY\">$source"

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

@Composable
fun HtmlPreview(page: String, gone: () -> Unit, modifier: Modifier) {
  AndroidView(
    {
      WebView(it).apply {
        lockDown(WebPreviewSettings(settings))
        webViewClient = PreviewClient(gone)
        setDownloadListener { _, _, _, _, _ -> }
        loadDataWithBaseURL(null, page, "text/html", "utf-8", null)
      }
    },
    modifier,
    onRelease = WebView::destroy
  )
}
