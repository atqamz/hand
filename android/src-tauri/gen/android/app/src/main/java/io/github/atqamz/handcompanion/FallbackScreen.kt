package io.github.atqamz.handcompanion

import android.content.ActivityNotFoundException
import android.content.Intent
import android.webkit.CookieManager
import android.webkit.RenderProcessGoneDetail
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import kotlin.coroutines.resume
import kotlin.coroutines.suspendCoroutine

@Composable
fun FallbackScreen(
  fleet: String,
  name: String?,
  path: String,
  onClose: () -> Unit,
  body: @Composable () -> Unit
) {
  val hand = LocalHand.current
  val page = path.removePrefix("/$fleet/")
  Column(Modifier.fillMaxSize().safeDrawingPadding()) {
    Row(
      Modifier.fillMaxWidth().height(64.dp).padding(end = 16.dp),
      verticalAlignment = Alignment.CenterVertically,
      horizontalArrangement = Arrangement.spacedBy(8.dp)
    ) {
      IconButton(onClose) { Icon(HandIcons.Close, "Close", Modifier.size(20.dp)) }
      Column(Modifier.weight(1f)) {
        Text("Web Board", style = type.titleMedium)
        Meta(listOf(name ?: fleet, page).filter(String::isNotEmpty).joinToString(" · "))
      }
    }
    HorizontalDivider(color = hand.line)
    Text(
      "This page has no native screen yet. It is the Board's own page.",
      Modifier.fillMaxWidth().background(hand.panel).padding(16.dp, 10.dp),
      style = type.bodyMedium
    )
    Box(Modifier.weight(1f)) { body() }
  }
}

private object WebCookies : Cookies {
  override suspend fun set(url: String, cookie: String) =
    suspendCoroutine { done ->
      val manager = CookieManager.getInstance()
      manager.setCookie(url, cookie) { stored ->
        manager.flush()
        done.resume(stored)
      }
    }
}

@Composable
fun WebBoard(path: String, onClose: () -> Unit) {
  val page by produceState<Page?>(null, path) { value = openBoard(path, Core::fallback, WebCookies) }
  var view by remember { mutableStateOf<WebView?>(null) }
  BackHandler { view?.takeIf(WebView::canGoBack)?.goBack() ?: onClose() }
  BoardBody(page, onClose) { view = it }
}

@Composable
internal fun BoardBody(page: Page?, onClose: () -> Unit, onView: (WebView) -> Unit) {
  when (val shown = page) {
    null -> Loading()
    is Page.Failed ->
      Text(
        shown.message,
        Modifier.padding(16.dp),
        color = LocalHand.current.ink(Tone.Fail),
        style = type.bodyMedium
      )
    is Page.Ready ->
      AndroidView(
        {
          WebView(it).apply {
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true
            settings.allowFileAccess = false
            settings.allowContentAccess = false
            webViewClient =
              object : WebViewClient() {
                override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest) =
                  when (
                    navigation(
                      request.url.toString(),
                      shown.port,
                      request.isForMainFrame,
                      request.hasGesture(),
                      request.isRedirect
                    )
                  ) {
                    Nav.Stay -> false
                    Nav.Browser -> {
                      try {
                        context.startActivity(
                          Intent(Intent.ACTION_VIEW, request.url).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                        )
                      } catch (_: ActivityNotFoundException) {}
                      true
                    }
                    Nav.Drop -> true
                  }

                override fun onRenderProcessGone(view: WebView, detail: RenderProcessGoneDetail): Boolean {
                  onClose()
                  return true
                }
              }
            loadUrl(shown.url)
            onView(this)
          }
        },
        Modifier.fillMaxSize(),
        onRelease = WebView::destroy
      )
  }
}
