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

  @JvmStatic external fun paired(): Boolean

  @JvmStatic external fun fleets(): String?

  @JvmStatic external fun unsaved(): Boolean

  @JvmStatic external fun offer(link: String): String?

  @JvmStatic external fun pair(name: String): String?

  @JvmStatic external fun cancel()

  @JvmStatic external fun open(link: String): String?

  @JvmStatic external fun fallback(fleet: String): String?

  @JvmStatic external fun forget(): Boolean

  @JvmStatic external fun watch()

  @JvmStatic external fun unwatch()

  @JvmStatic
  external fun request(
    fleet: String,
    post: Boolean,
    path: String,
    csrf: String?,
    body: ByteArray?
  ): String?
}
