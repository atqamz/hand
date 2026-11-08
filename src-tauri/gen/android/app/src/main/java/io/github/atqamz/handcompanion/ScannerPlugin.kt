package io.github.atqamz.handcompanion

import android.Manifest
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.net.Uri
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.view.Gravity
import android.view.ViewGroup
import android.widget.Button
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.TextView
import androidx.activity.ComponentActivity
import androidx.activity.OnBackPressedCallback
import androidx.core.content.ContextCompat
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import app.tauri.annotation.Command
import app.tauri.annotation.InvokeArg
import app.tauri.annotation.TauriPlugin
import app.tauri.plugin.Invoke
import app.tauri.plugin.JSObject
import app.tauri.plugin.Plugin
import com.google.zxing.BarcodeFormat
import com.journeyapps.barcodescanner.BarcodeCallback
import com.journeyapps.barcodescanner.BarcodeResult
import com.journeyapps.barcodescanner.BarcodeView
import com.journeyapps.barcodescanner.CameraPreview
import com.journeyapps.barcodescanner.DefaultDecoderFactory

@InvokeArg
internal class ScanArgs {
  var permissionMessage: String = ""
  var failedMessage: String = ""
}

private const val SCAN_OPEN = "scan_open"

@TauriPlugin
class ScannerPlugin(private val activity: Activity) : Plugin(activity) {
  private var session: Session? = null
  private var permissionAsked = false

  init {
    (activity as? LifecycleOwner)?.lifecycle?.addObserver(
      object : DefaultLifecycleObserver {
        override fun onPause(owner: LifecycleOwner) {
          session?.pause()
        }

        override fun onResume(owner: LifecycleOwner) {
          session?.resume()
        }
      }
    )
  }

  @Command
  fun scanCode(invoke: Invoke) {
    if (session != null) {
      invoke.reject("A scan is already open.", SCAN_OPEN)
      return
    }
    val args = invoke.parseArgs(ScanArgs::class.java)
    activity.runOnUiThread {
      if (session != null) {
        invoke.reject("A scan is already open.", SCAN_OPEN)
        return@runOnUiThread
      }
      val started = Session(invoke, args.permissionMessage, args.failedMessage)
      session = started
      started.open()
    }
  }

  private enum class State {
    Starting,
    Permission,
    Preview,
    Failed,
  }

  private inner class Session(
    private val invoke: Invoke,
    private val permissionMessage: String,
    private val failedMessage: String,
  ) {
    private var state = State.Starting
    private var finished = false
    private var paused = false
    private var cameraStarted = false
    private var listener: CameraPreview.StateListener? = null
    private var timeout: Runnable? = null
    private val handler = Handler(Looper.getMainLooper())

    private val back = object : OnBackPressedCallback(false) {
      override fun handleOnBackPressed() {
        finish("cancelled", null)
      }
    }

    private val preview = BarcodeView(activity).apply {
      setDecoderFactory(DefaultDecoderFactory(listOf(BarcodeFormat.QR_CODE)))
      layoutParams = FrameLayout.LayoutParams(
        ViewGroup.LayoutParams.MATCH_PARENT,
        ViewGroup.LayoutParams.MATCH_PARENT
      )
    }

    private val status = TextView(activity).apply {
      setTextColor(Color.WHITE)
      textSize = 16f
      gravity = Gravity.CENTER
    }

    private val actions = LinearLayout(activity).apply {
      orientation = LinearLayout.HORIZONTAL
      gravity = Gravity.CENTER
    }

    private val allow = button("Allow camera") { requestPermission() }
    private val settings = button("Open settings") { openSettings() }
    private val retry = button("Try again") { startPreview() }
    private val close = button("Close") { finish(closeStatus(), null) }

    private val panel = LinearLayout(activity).apply {
      orientation = LinearLayout.VERTICAL
      gravity = Gravity.CENTER_HORIZONTAL
      setPadding(32, 32, 32, 64)
      setBackgroundColor(Color.argb(216, 0, 0, 0))
      addView(
        status,
        LinearLayout.LayoutParams(
          ViewGroup.LayoutParams.MATCH_PARENT,
          ViewGroup.LayoutParams.WRAP_CONTENT
        )
      )
      addView(
        actions,
        LinearLayout.LayoutParams(
          ViewGroup.LayoutParams.MATCH_PARENT,
          ViewGroup.LayoutParams.WRAP_CONTENT
        ).apply { topMargin = 16 }
      )
    }

    private val root = FrameLayout(activity).apply {
      setBackgroundColor(Color.BLACK)
      addView(preview)
      addView(
        panel,
        FrameLayout.LayoutParams(
          ViewGroup.LayoutParams.MATCH_PARENT,
          ViewGroup.LayoutParams.WRAP_CONTENT,
          Gravity.BOTTOM
        )
      )
    }

    init {
      ViewCompat.setOnApplyWindowInsetsListener(root) { _, insets ->
        val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars())
        panel.setPadding(32, 32, 32, 64 + bars.bottom)
        insets
      }
    }

    private val callback = object : BarcodeCallback {
      override fun barcodeResult(result: BarcodeResult) {
        if (state != State.Preview || finished) return
        finish("code", result.text)
      }
    }

    fun open() {
      val content = activity.findViewById<ViewGroup>(android.R.id.content)
      content.addView(root)
      (activity as? ComponentActivity)?.onBackPressedDispatcher?.addCallback(back)
      back.isEnabled = true
      ViewCompat.requestApplyInsets(root)
      showStarting()
      if (hasPermission()) {
        startPreview()
      } else if (permissionAsked) {
        showPermission()
      } else {
        permissionAsked = true
        requestPermission()
      }
    }

    fun pause() {
      if (finished || !cameraStarted) return
      paused = true
      preview.pause()
    }

    fun resume() {
      if (finished || !paused) return
      paused = false
      if (cameraStarted) preview.resume()
    }

    private fun hasPermission(): Boolean =
      ContextCompat.checkSelfPermission(activity, Manifest.permission.CAMERA) ==
        PackageManager.PERMISSION_GRANTED

    private fun requestPermission() {
      if (finished) return
      val host = activity as? MainActivity
      if (host == null) {
        showFailed()
        return
      }
      if (hasPermission()) {
        startPreview()
        return
      }
      showStarting()
      scheduleTimeout()
      host.requestCameraPermission { granted ->
        if (finished) return@requestCameraPermission
        if (granted) {
          startPreview()
        } else {
          showPermission()
        }
      }
    }

    private fun startPreview() {
      if (finished) return
      showStarting()
      if (listener == null) {
        listener = object : CameraPreview.StateListener {
          override fun previewSized() {}

          override fun previewStarted() {
            if (finished) return
            clearTimeout()
            showPreview()
            preview.decodeContinuous(callback)
          }

          override fun previewStopped() {}

          override fun cameraError(error: Exception) {
            showFailed()
          }

          override fun cameraClosed() {}
        }
        preview.addStateListener(listener)
      }
      cameraStarted = true
      scheduleTimeout()
      preview.resume()
    }

    private fun showStarting() {
      state = State.Starting
      status.text = "Starting the camera…"
      setActions(listOf(close))
    }

    private fun showPermission() {
      if (finished) return
      clearTimeout()
      state = State.Permission
      status.text = permissionMessage
      setActions(listOf(allow, settings, close))
    }

    private fun showPreview() {
      if (finished) return
      state = State.Preview
      status.text = "Point the camera at the QR code."
      setActions(listOf(close))
    }

    private fun showFailed() {
      if (finished) return
      clearTimeout()
      state = State.Failed
      cameraStarted = false
      preview.stopDecoding()
      preview.pause()
      status.text = failedMessage
      setActions(listOf(retry, close))
    }

    private fun setActions(buttons: List<Button>) {
      actions.removeAllViews()
      for (button in buttons) {
        actions.addView(
          button,
          LinearLayout.LayoutParams(
            ViewGroup.LayoutParams.WRAP_CONTENT,
            ViewGroup.LayoutParams.WRAP_CONTENT
          ).apply { marginStart = 8; marginEnd = 8 }
        )
      }
    }

    private fun scheduleTimeout() {
      clearTimeout()
      val task = Runnable {
        if (!finished && state == State.Starting) finish("failed", null)
      }
      timeout = task
      handler.postDelayed(task, 15000)
    }

    private fun clearTimeout() {
      timeout?.let { handler.removeCallbacks(it) }
      timeout = null
    }

    private fun openSettings() {
      activity.startActivity(
        Intent(
          Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
          Uri.parse("package:${activity.packageName}")
        )
      )
    }

    private fun closeStatus(): String =
      when (state) {
        State.Permission -> "permission"
        State.Failed -> "failed"
        State.Starting, State.Preview -> "cancelled"
      }

    private fun finish(status: String, text: String?) {
      if (finished) return
      finished = true
      clearTimeout()
      back.isEnabled = false
      back.remove()
      cameraStarted = false
      preview.stopDecoding()
      preview.pause()
      (root.parent as? ViewGroup)?.removeView(root)
      if (session === this) session = null
      val reply = JSObject()
      reply.put("status", status)
      if (text != null) reply.put("text", text)
      invoke.resolveObject(reply)
    }

    private fun button(label: String, action: () -> Unit): Button =
      Button(activity).apply {
        text = label
        setOnClickListener { action() }
      }
  }
}
