package io.github.atqamz.handcompanion

enum class AlertStep {
  Idle,
  Ask,
  Start,
  Off
}

fun alertStep(paired: Boolean, allowed: Boolean, asked: Boolean): AlertStep =
  when {
    !paired -> AlertStep.Idle
    allowed -> AlertStep.Start
    !asked -> AlertStep.Ask
    else -> AlertStep.Off
  }
