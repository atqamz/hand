package io.github.atqamz.handcompanion

import android.content.Context
import android.os.Handler
import android.os.Looper
import android.webkit.CookieManager
import android.webkit.WebStorage
import android.webkit.WebView
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

const val COOKIE_URL = "http://localhost/"

class AppHost(private val context: Context) {
  fun keystore(operation: Int, data: ByteArray): ByteArray =
    try {
      val output =
        when (operation) {
          PROBE -> ByteArray(0).also { KeystoreSealer.probe() }
          SEAL -> KeystoreSealer.seal(data)
          else -> KeystoreSealer.open(data)
        }
      byteArrayOf(0) + output
    } catch (error: SealError) {
      byteArrayOf(1) + "${error.code}\n${error.message}".toByteArray()
    }

  fun cookies(): String? = CookieManager.getInstance().getCookie(COOKIE_URL)

  fun alert(
    fleetId: String,
    fleetName: String,
    kind: String,
    reference: String,
    text: String,
    link: String?
  ): Boolean = Notifier.post(context, alertSpec(fleetId, fleetName, kind, reference, text, link))

  fun clearCookies(): Boolean {
    val done = CountDownLatch(1)
    val storage = AtomicBoolean(true)
    Handler(Looper.getMainLooper()).post {
      try {
        try {
          WebStorage.getInstance().deleteAllData()
          WebView(context).apply {
            clearCache(true)
            destroy()
          }
        } catch (_: Exception) {
          storage.set(false)
        }
        val manager = CookieManager.getInstance()
        manager.removeAllCookies {
          manager.flush()
          done.countDown()
        }
      } catch (_: Exception) {
        storage.set(false)
        done.countDown()
      }
    }
    return done.await(COOKIE_CLEAR_SECONDS, TimeUnit.SECONDS) && storage.get()
  }

  fun stopService() {
    AlertService.stop(context)
  }

  fun unreadable() {
    Notifier.post(context, unreadableSpec())
  }

  fun unpaired(fleetId: String, fleetName: String?) {
    Notifier.post(context, unpairedSpec(fleetId, fleetName))
  }

  private companion object {
    const val PROBE = 0
    const val SEAL = 1
    const val COOKIE_CLEAR_SECONDS = 5L
  }
}
