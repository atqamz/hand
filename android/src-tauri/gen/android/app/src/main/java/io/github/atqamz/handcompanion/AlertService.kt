package io.github.atqamz.handcompanion

import android.app.AlarmManager
import android.app.PendingIntent
import android.app.Service
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import android.os.BatteryManager
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.PowerManager
import android.os.SystemClock
import android.util.Log
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import java.util.concurrent.Executors
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.atomic.AtomicBoolean

class AlertService : Service(), IdleEffects {
  private var watching = false
  private var started = false
  private var listening = false
  private val handler = Handler(Looper.getMainLooper())
  private val pending = AtomicBoolean(false)
  private val worker = Executors.newSingleThreadExecutor()
  private val idle = IdleCycle(this)
  private val tell = Runnable {
    pending.set(false)
    if (!idle.asleep) Core.networkChange()
  }
  private val workLock by lazy {
    getSystemService(PowerManager::class.java)
      .newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "$TAG:work")
      .apply { setReferenceCounted(false) }
  }
  private val alarmIntent by lazy {
    PendingIntent.getBroadcast(
      this,
      0,
      Intent(this, PollReceiver::class.java),
      PendingIntent.FLAG_IMMUTABLE
    )
  }
  private val graceIntent by lazy {
    PendingIntent.getBroadcast(
      this,
      1,
      Intent(this, PollReceiver::class.java).setAction(ACTION_GRACE),
      PendingIntent.FLAG_IMMUTABLE
    )
  }

  private val inputs =
    object : BroadcastReceiver() {
      override fun onReceive(context: Context, intent: Intent) {
        when (intent.action) {
          Intent.ACTION_SCREEN_ON -> idle.onScreen(true)
          Intent.ACTION_SCREEN_OFF -> idle.onScreen(false)
          Intent.ACTION_USER_PRESENT -> idle.onUnlocked()
          Intent.ACTION_POWER_CONNECTED -> idle.onCharging(true)
          Intent.ACTION_POWER_DISCONNECTED -> idle.onCharging(false)
        }
      }
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
    current = this
    if (!started) {
      started = true
      awaken()
      watchNetwork()
      watchInputs()
    }
    return true
  }

  private fun watchInputs() {
    val filter =
      IntentFilter().apply {
        addAction(Intent.ACTION_SCREEN_ON)
        addAction(Intent.ACTION_SCREEN_OFF)
        addAction(Intent.ACTION_USER_PRESENT)
        addAction(Intent.ACTION_POWER_CONNECTED)
        addAction(Intent.ACTION_POWER_DISCONNECTED)
      }
    ContextCompat.registerReceiver(this, inputs, filter, ContextCompat.RECEIVER_NOT_EXPORTED)
    listening = true
    val plugged =
      registerReceiver(null, IntentFilter(Intent.ACTION_BATTERY_CHANGED))
        ?.getIntExtra(BatteryManager.EXTRA_PLUGGED, 0) ?: 0
    idle.start(
      screenOn = getSystemService(PowerManager::class.java).isInteractive,
      charging = plugged != 0
    )
  }

  override fun armGrace(delayMs: Long) = arm(graceIntent, delayMs)

  override fun cancelGrace() = getSystemService(AlarmManager::class.java).cancel(graceIntent)

  override fun sleep() {
    Log.i(TAG, "idle sleep")
    held { Core.sleep() }
  }

  override fun wake() {
    Log.i(TAG, "idle wake")
    awaken()
  }

  private fun awaken() {
    worker.execute {
      if (Core.wake()) {
        Core.watch()
      } else {
        onMain {
          if (!idle.onWakeFailed()) Log.w(TAG, "wake failed, giving up until the next idle cycle")
        }
      }
    }
  }

  override fun armAlarm(delayMs: Long) = arm(alarmIntent, delayMs)

  private fun arm(intent: PendingIntent, delayMs: Long) =
    getSystemService(AlarmManager::class.java)
      .setAndAllowWhileIdle(
        AlarmManager.ELAPSED_REALTIME_WAKEUP,
        SystemClock.elapsedRealtime() + delayMs,
        intent
      )

  override fun cancelAlarm() = getSystemService(AlarmManager::class.java).cancel(alarmIntent)

  override fun poll() {
    held {
      val reached = Core.poll()
      onMain { idle.onPolled(reached) }
    }
  }

  private fun onMain(task: () -> Unit) {
    handler.post { if (current === this) task() }
  }

  private fun held(task: () -> Unit) {
    workLock.acquire(WORK_LOCK_MS)
    try {
      worker.execute {
        try {
          task()
        } finally {
          if (workLock.isHeld) workLock.release()
        }
      }
    } catch (error: RejectedExecutionException) {
      if (workLock.isHeld) workLock.release()
    }
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
    if (current === this) current = null
    if (listening) {
      unregisterReceiver(inputs)
      listening = false
    }
    if (started) {
      cancelAlarm()
      cancelGrace()
    }
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
    worker.execute { Core.wake() }
    worker.shutdown()
    Log.i(TAG, "service stop")
    super.onDestroy()
  }

  companion object {
    private const val TAG = "HandAlerts"
    private const val COALESCE_MS = 1000L
    private const val WORK_LOCK_MS = 60_000L
    const val ACTION_GRACE = "io.github.atqamz.handcompanion.GRACE"

    @Volatile private var current: AlertService? = null

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

    fun alarm(context: Context) {
      val service = current
      if (service == null) start(context) else service.idle.onAlarm()
    }

    fun graceAlarm(context: Context) {
      val service = current
      if (service == null) start(context) else service.idle.onGrace()
    }

    fun stop(context: Context) {
      running = false
      context.stopService(Intent(context, AlertService::class.java))
    }
  }
}
