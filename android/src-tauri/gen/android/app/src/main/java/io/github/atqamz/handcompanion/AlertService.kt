package io.github.atqamz.handcompanion

import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.IBinder
import android.util.Log
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat

class AlertService : Service() {
  override fun onBind(intent: Intent?): IBinder? = null

  override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
    if (!begin()) return START_NOT_STICKY
    Log.i(TAG, "service start id=$startId restart=${intent == null}")
    return START_STICKY
  }

  override fun onTaskRemoved(rootIntent: Intent?) {
    if (!running && begin()) Log.i(TAG, "service start after task removal")
  }

  private fun begin(): Boolean {
    Notifier.ensureChannels(this)
    try {
      ServiceCompat.startForeground(
        this,
        Notifier.SERVICE_ID,
        Notifier.permanent(this),
        ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE
      )
    } catch (error: Exception) {
      Log.w(TAG, "foreground start refused: ${error.javaClass.simpleName}")
      stopSelf()
      return false
    }
    if (Core.start(this) != null) {
      Log.w(TAG, "core did not start")
      stopSelf()
      return false
    }
    running = true
    Core.watch()
    return true
  }

  override fun onDestroy() {
    running = false
    Core.unwatch()
    Log.i(TAG, "service stop")
    super.onDestroy()
  }

  companion object {
    private const val TAG = "HandAlerts"

    @Volatile var running = false
      private set

    fun start(context: Context) {
      if (running) return
      try {
        ContextCompat.startForegroundService(context, Intent(context, AlertService::class.java))
      } catch (error: Exception) {
        Log.w(TAG, "foreground start refused: ${error.javaClass.simpleName}")
      }
    }

    fun stop(context: Context) {
      running = false
      context.stopService(Intent(context, AlertService::class.java))
    }
  }
}
