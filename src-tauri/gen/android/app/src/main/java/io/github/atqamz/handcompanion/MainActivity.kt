package io.github.atqamz.handcompanion

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.view.ViewGroup
import android.webkit.WebView
import android.widget.FrameLayout
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.ContextCompat
import androidx.core.graphics.Insets
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.lifecycle.Lifecycle

private val handledInsets =
  WindowInsetsCompat.Type.systemBars() or
    WindowInsetsCompat.Type.displayCutout() or
    WindowInsetsCompat.Type.ime()

class MainActivity : TauriActivity() {
  private var cameraPermissionResult: ((Boolean) -> Unit)? = null
  private val cameraPermission =
    registerForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
      val result = cameraPermissionResult
      cameraPermissionResult = null
      result?.invoke(granted)
    }

  private var askingNotifications = false
  private val notificationPermission =
    registerForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
      askingNotifications = false
      getPreferences(MODE_PRIVATE).edit().putBoolean(ASKED, true).apply()
      if (granted) startAlerts()
    }

  override fun onResume() {
    super.onResume()
    ensureAlerts()
  }

  override fun onCreate(savedInstanceState: Bundle?) {
    enableEdgeToEdge()
    Core.start(applicationContext)
    Core.releaseContext()
    super.onCreate(savedInstanceState)
  }

  @Deprecated("Wry calls it when the web view cannot go back")
  override fun onBackPressed() {
    if (AlertService.running) moveTaskToBack(true) else super.onBackPressed()
  }

  private fun startAlerts() {
    if (lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)) AlertService.start(this)
  }

  fun ensureAlerts() {
    Thread { if (Core.paired()) runOnUiThread { askAndStartAlerts() } }.start()
  }

  private fun askAndStartAlerts() {
    if (!lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)) return
    val needed =
      Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
        ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) !=
          PackageManager.PERMISSION_GRANTED
    if (!needed) {
      startAlerts()
      return
    }
    if (!askingNotifications && !getPreferences(MODE_PRIVATE).getBoolean(ASKED, false)) {
      askingNotifications = true
      notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
    }
  }

  override fun onWebViewCreate(webView: WebView) {
    super.onWebViewCreate(webView)
    webView.settings.allowFileAccess = false
    webView.settings.allowContentAccess = false
    webView.post {
      val container = insetContainer(webView) ?: return@post
      ViewCompat.setOnApplyWindowInsetsListener(container) { view, windowInsets ->
        val bars = windowInsets.getInsets(WindowInsetsCompat.Type.systemBars())
        val cutout = windowInsets.getInsets(WindowInsetsCompat.Type.displayCutout())
        val ime = windowInsets.getInsets(WindowInsetsCompat.Type.ime())
        val padding = webViewPadding(
          systemBarsTop = bars.top,
          systemBarsBottom = bars.bottom,
          displayCutoutTop = cutout.top,
          imeBottom = ime.bottom
        )
        view.setPadding(view.paddingLeft, padding.top, view.paddingRight, padding.bottom)
        WindowInsetsCompat.Builder(windowInsets)
          .setInsets(handledInsets, Insets.NONE)
          .build()
      }
      ViewCompat.requestApplyInsets(container)
    }
  }

  private fun insetContainer(webView: WebView): FrameLayout? {
    val parent = webView.parent as? ViewGroup ?: return null
    val index = parent.indexOfChild(webView)
    val params = webView.layoutParams
    parent.removeViewAt(index)
    val container = FrameLayout(this)
    container.addView(
      webView,
      FrameLayout.LayoutParams(
        ViewGroup.LayoutParams.MATCH_PARENT,
        ViewGroup.LayoutParams.MATCH_PARENT
      )
    )
    parent.addView(container, index, params)
    return container
  }

  private companion object {
    const val ASKED = "notification-permission-asked"
  }

  fun requestCameraPermission(result: (Boolean) -> Unit) {
    if (ContextCompat.checkSelfPermission(this, Manifest.permission.CAMERA) ==
      PackageManager.PERMISSION_GRANTED
    ) {
      result(true)
      return
    }
    cameraPermissionResult = result
    cameraPermission.launch(Manifest.permission.CAMERA)
  }
}
