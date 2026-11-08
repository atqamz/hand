package io.github.atqamz.handcompanion

import android.Manifest
import android.content.pm.PackageManager
import android.os.Bundle
import android.webkit.WebView
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.ContextCompat

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
