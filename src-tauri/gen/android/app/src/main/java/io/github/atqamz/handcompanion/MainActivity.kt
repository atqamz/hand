package io.github.atqamz.handcompanion

import android.Manifest
import android.content.pm.PackageManager
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

  override fun onCreate(savedInstanceState: Bundle?) {
    enableEdgeToEdge()
    super.onCreate(savedInstanceState)
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
