package io.github.atqamz.handcompanion

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import androidx.core.app.NotificationCompat

object Notifier {
  const val SERVICE_ID = 1
  private const val NOTICE_ID = 2

  fun ensureChannels(context: Context) {
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
    val manager = context.getSystemService(NotificationManager::class.java)
    manager.createNotificationChannels(
      channelSpecs.map { NotificationChannel(it.id, it.name, it.importance) }
    )
  }

  fun permanent(context: Context): Notification =
    NotificationCompat.Builder(context, Channels.SERVICE)
      .setSmallIcon(R.drawable.ic_stat)
      .setContentTitle("Hand is watching for alerts")
      .setOngoing(true)
      .setContentIntent(open(context, null))
      .build()

  fun post(context: Context, spec: NoticeSpec): Boolean {
    val manager = context.getSystemService(NotificationManager::class.java)
    return deliver(manager.areNotificationsEnabled()) {
      ensureChannels(context)
      manager.notify(spec.tag, NOTICE_ID, notification(context, spec))
    }
  }

  private fun notification(context: Context, spec: NoticeSpec): Notification {
    val hidden =
      NotificationCompat.Builder(context, spec.channel)
        .setSmallIcon(R.drawable.ic_stat)
        .setContentTitle("Hand: alert")
        .build()
    return NotificationCompat.Builder(context, spec.channel)
      .setSmallIcon(R.drawable.ic_stat)
      .setContentTitle(spec.title)
      .setContentText(spec.text)
      .setStyle(NotificationCompat.BigTextStyle().bigText(spec.text))
      .setVisibility(spec.visibility)
      .setPublicVersion(hidden)
      .setAutoCancel(true)
      .setContentIntent(open(context, spec))
      .build()
  }

  private fun open(context: Context, spec: NoticeSpec?): PendingIntent {
    val link = spec?.tapUri
    val intent =
      if (link != null && BuildConfig.DEBUG && decisionRoute(link) != null) {
        Intent(context, HandActivity::class.java).setData(Uri.parse(link))
      } else if (link != null) {
        Intent(Intent.ACTION_VIEW, Uri.parse(link)).setPackage(context.packageName)
      } else {
        context.packageManager.getLaunchIntentForPackage(context.packageName)!!
      }
    intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
    return PendingIntent.getActivity(
      context,
      spec?.tag.hashCode(),
      intent,
      PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
    )
  }
}
