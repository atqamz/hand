package io.github.atqamz.handcompanion

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

class PollReceiver : BroadcastReceiver() {
  override fun onReceive(context: Context, intent: Intent) = AlertService.alarm(context)
}
