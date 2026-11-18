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
      effects.sleep()
      effects.armAlarm(POLL_MS)
    }
  }

  fun onAlarm() {
    if (!asleep) return
    effects.armAlarm(POLL_MS)
    effects.poll()
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
      effects.cancelAlarm()
      effects.wake()
    }
  }

  companion object {
    const val GRACE_MS = 2 * 60 * 1000L
    const val POLL_MS = 5 * 60 * 1000L
  }
}
