package io.github.atqamz.handcompanion

interface IdleEffects {
  fun armGrace(delayMs: Long)

  fun cancelGrace()

  fun sleep()

  fun wake()

  fun armAlarm(delayMs: Long)

  fun cancelAlarm()

  fun poll()
}

class IdleCycle(private val effects: IdleEffects) {
  var asleep = false
    private set

  private var screenOn = true
  private var charging = false
  private var grace = false
  private var wakeRetry = false
  private var wakeTries = 0
  private var pollMs = POLL_MS

  private val idle
    get() = !screenOn && !charging

  fun start(screenOn: Boolean, charging: Boolean) {
    this.screenOn = screenOn
    this.charging = charging
    settle()
  }

  fun onScreen(on: Boolean) {
    screenOn = on
    settle()
  }

  fun onUnlocked() {
    screenOn = true
    settle()
  }

  fun onCharging(on: Boolean) {
    charging = on
    settle()
  }

  fun onGrace() {
    grace = false
    if (idle && !asleep) {
      asleep = true
      wakeRetry = false
      pollMs = POLL_MS
      effects.sleep()
      effects.armAlarm(pollMs)
    }
  }

  fun onAlarm() {
    if (wakeRetry) {
      wakeRetry = false
      effects.wake()
      return
    }
    if (!asleep) return
    effects.armAlarm(pollMs)
    effects.poll()
  }

  fun onPolled(reached: Boolean) {
    if (!asleep) return
    pollMs = if (reached) POLL_MS else minOf(pollMs * 2, POLL_MAX_MS)
    effects.armAlarm(pollMs)
  }

  fun onWakeFailed(): Boolean {
    if (asleep || wakeTries >= WAKE_TRIES) return false
    wakeTries++
    wakeRetry = true
    effects.armAlarm(WAKE_RETRY_MS)
    return true
  }

  private fun settle() {
    if (idle) {
      if (!asleep && !grace) {
        grace = true
        effects.armGrace(GRACE_MS)
      }
      return
    }
    if (grace) {
      grace = false
      effects.cancelGrace()
    }
    if (asleep) {
      asleep = false
      pollMs = POLL_MS
      wakeTries = 0
      effects.cancelAlarm()
      effects.wake()
    }
  }

  companion object {
    const val GRACE_MS = 2 * 60 * 1000L
    const val POLL_MS = 5 * 60 * 1000L
    const val POLL_MAX_MS = 40 * 60 * 1000L
    const val WAKE_RETRY_MS = 30 * 1000L
    const val WAKE_TRIES = 3
  }
}
