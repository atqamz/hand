package io.github.atqamz.handcompanion

import android.content.Context

object Core {
  init {
    System.loadLibrary("hand_android_lib")
  }

  fun start(context: Context): String? {
    val app = context.applicationContext
    return nativeStart(app.dataDir.absolutePath, AppHost(app), app)
  }

  @JvmStatic private external fun nativeStart(dataDir: String, host: Any, context: Context): String?

  @JvmStatic external fun releaseContext()

  @JvmStatic external fun watch()

  @JvmStatic external fun unwatch()
}
