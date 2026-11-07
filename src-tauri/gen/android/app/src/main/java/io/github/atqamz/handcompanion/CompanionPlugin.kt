package io.github.atqamz.handcompanion

import android.app.Activity
import android.content.Intent
import android.graphics.Color
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.view.WindowManager
import android.webkit.WebView
import android.widget.Button
import android.widget.FrameLayout
import android.widget.LinearLayout
import app.tauri.annotation.Command
import app.tauri.annotation.InvokeArg
import app.tauri.annotation.TauriPlugin
import app.tauri.plugin.Invoke
import app.tauri.plugin.Plugin

@InvokeArg
internal class CompanionConfig {
  var localUrl: String = "http://tauri.localhost/"
}

@InvokeArg
internal class SecureArgs {
  var secure: Boolean = false
}

@TauriPlugin
class CompanionPlugin(private val activity: Activity) : Plugin(activity) {
  private var webView: WebView? = null
  private var pending: String? = null
  private var localUrl = "http://tauri.localhost/"
  private var recoverBar: View? = null

  override fun load(webView: WebView) {
    this.webView = webView
    localUrl = configUrl()
    readIntent()
  }

  override fun onNewIntent(intent: Intent) {
    val link = intentLink(intent) ?: return
    pending = link
    intentRead = true
    hideRecoverActions()
    currentWebView()?.let { view -> view.post { view.loadUrl(localUrl) } }
  }

  @Command
  fun takeLink(invoke: Invoke) {
    localUrl = configUrl()
    readIntent()
    val link = pending
    pending = null
    if (link == null) {
      invoke.resolve()
    } else {
      invoke.resolveObject(link)
    }
  }

  @Command
  fun setSecure(invoke: Invoke) {
    val secure = invoke.parseArgs(SecureArgs::class.java).secure
    activity.runOnUiThread {
      if (secure) {
        activity.window.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
      } else {
        activity.window.clearFlags(WindowManager.LayoutParams.FLAG_SECURE)
      }
    }
    invoke.resolve()
  }

  @Command
  fun showRecoverActions(invoke: Invoke) {
    activity.runOnUiThread {
      if (recoverBar == null) {
        val root = activity.findViewById<ViewGroup>(android.R.id.content)
        val bar = LinearLayout(activity).apply {
          orientation = LinearLayout.HORIZONTAL
          gravity = Gravity.CENTER
          setPadding(24, 24, 24, 24)
          setBackgroundColor(Color.rgb(17, 24, 39))
        }
        val again = Button(activity).apply {
          text = "Pair again"
          setOnClickListener {
            hideRecoverActions()
            currentWebView()?.loadUrl(localUrl)
          }
        }
        val forget = Button(activity).apply {
          text = "Forget this laptop"
          setOnClickListener {
            hideRecoverActions()
            currentWebView()?.loadUrl("$localUrl?forget=1")
          }
        }
        bar.addView(again, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        bar.addView(forget, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        root.addView(
          bar,
          FrameLayout.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT,
            ViewGroup.LayoutParams.WRAP_CONTENT,
            Gravity.BOTTOM
          )
        )
        recoverBar = bar
      }
    }
    invoke.resolve()
  }

  private fun hideRecoverActions() {
    recoverBar?.let { (it.parent as? ViewGroup)?.removeView(it) }
    recoverBar = null
  }

  private companion object {
    var intentRead = false
  }

  private fun readIntent() {
    if (intentRead) return
    intentRead = true
    pending = intentLink(activity.intent)
  }

  private fun configUrl(): String =
    runCatching { getConfig(CompanionConfig::class.java).localUrl }.getOrDefault(localUrl)

  private fun currentWebView(): WebView? {
    webView?.let { return it }
    val root = activity.findViewById<ViewGroup>(android.R.id.content) ?: return null
    return findWebView(root).also { webView = it }
  }

  private fun findWebView(group: ViewGroup): WebView? {
    for (index in 0 until group.childCount) {
      val child = group.getChildAt(index)
      if (child is WebView) return child
      if (child is ViewGroup) findWebView(child)?.let { return it }
    }
    return null
  }

  private fun intentLink(intent: Intent?): String? {
    val data = intent?.data ?: return null
    if (data.scheme != "handboard") return null
    return data.toString()
  }
}
