package io.github.atqamz.handcompanion

import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.util.Log
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import java.util.concurrent.atomic.AtomicBoolean

class AlertService : Service() {
  private var watching = false
  private val handler = Handler(Looper.getMainLooper())
  private val pending = AtomicBoolean(false)
  private val tell = Runnable {
    pending.set(false)
    Core.networkChange()
  }

  private fun changed() {
    if (pending.compareAndSet(false, true)) handler.postDelayed(tell, COALESCE_MS)
  }

  private val networkChanged =
    object : ConnectivityManager.NetworkCallback() {
      override fun onAvailable(network: Network) = changed()

      override fun onLinkPropertiesChanged(network: Network, linkProperties: LinkProperties) =
        changed()
    }

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
    watchNetwork()
    return true
  }

  private fun watchNetwork() {
    if (watching) return
    try {
      getSystemService(ConnectivityManager::class.java).registerDefaultNetworkCallback(networkChanged)
      watching = true
    } catch (error: Exception) {
      Log.w(TAG, "network callback refused: ${error.javaClass.simpleName}")
    }
  }

  override fun onDestroy() {
    running = false
    if (watching) {
      try {
        getSystemService(ConnectivityManager::class.java).unregisterNetworkCallback(networkChanged)
      } catch (error: IllegalArgumentException) {
        Log.w(TAG, "network callback was not registered")
      }
      watching = false
    }
    handler.removeCallbacks(tell)
    Core.unwatch()
    Log.i(TAG, "service stop")
    super.onDestroy()
  }

  companion object {
    private const val TAG = "HandAlerts"
    private const val COALESCE_MS = 1000L

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
