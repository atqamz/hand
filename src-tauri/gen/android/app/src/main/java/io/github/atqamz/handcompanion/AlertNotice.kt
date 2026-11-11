package io.github.atqamz.handcompanion

import android.app.Notification
import android.app.NotificationManager

object Channels {
  const val SERVICE = "service"
  const val DECISIONS = "decisions"
  const val NEEDS = "needs"
  const val PROGRESS = "progress"
}

data class ChannelSpec(val id: String, val name: String, val importance: Int)

val channelSpecs =
  listOf(
    ChannelSpec(Channels.SERVICE, "Connection", NotificationManager.IMPORTANCE_LOW),
    ChannelSpec(Channels.DECISIONS, "Decisions", NotificationManager.IMPORTANCE_HIGH),
    ChannelSpec(Channels.NEEDS, "Needs you", NotificationManager.IMPORTANCE_HIGH),
    ChannelSpec(Channels.PROGRESS, "Progress", NotificationManager.IMPORTANCE_DEFAULT)
  )

val channelIds = channelSpecs.map { it.id }

private val NEEDS_KINDS =
  setOf(
    "attempt.blocked",
    "attempt.limited",
    "attempt.failed",
    "supervisor.blocked",
    "supervisor.limited",
    "supervisor.stalled"
  )

private const val OPEN_PREFIX = "handboard://open/"

data class NoticeSpec(
  val tag: String,
  val title: String,
  val text: String,
  val channel: String,
  val tapUri: String?,
  val visibility: Int
)

fun channelFor(kind: String): String =
  when {
    kind == "decision.asked" -> Channels.DECISIONS
    kind in NEEDS_KINDS -> Channels.NEEDS
    else -> Channels.PROGRESS
  }

fun deliver(enabled: Boolean, notify: () -> Unit): Boolean {
  if (!enabled) return false
  notify()
  return true
}

fun alertSpec(
  fleetId: String,
  fleetName: String,
  kind: String,
  reference: String,
  text: String,
  link: String?
): NoticeSpec =
  NoticeSpec(
    tag = "$fleetId/$reference",
    title = "${fleetName.ifBlank { fleetId }} · $reference",
    text = text,
    channel = channelFor(kind),
    tapUri = link?.takeIf { it.startsWith(OPEN_PREFIX) },
    visibility = Notification.VISIBILITY_PRIVATE
  )

fun unpairedSpec(fleetId: String, fleetName: String?): NoticeSpec =
  NoticeSpec(
    tag = "$fleetId/unpaired",
    title = "Hand",
    text = "No longer paired with ${fleetName?.ifBlank { null } ?: fleetId}",
    channel = Channels.NEEDS,
    tapUri = null,
    visibility = Notification.VISIBILITY_PRIVATE
  )
