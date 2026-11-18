package io.github.atqamz.handcompanion

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

class PollReceiver : BroadcastReceiver() {
  override fun onReceive(context: Context, intent: Intent) =
    if (intent.action == AlertService.ACTION_GRACE) AlertService.graceAlarm(context)
    else AlertService.alarm(context)
}
