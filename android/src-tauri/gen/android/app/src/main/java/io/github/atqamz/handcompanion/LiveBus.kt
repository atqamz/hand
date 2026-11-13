package io.github.atqamz.handcompanion

import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

data class LiveEvent(
  val fleet: String,
  val topic: String,
  val json: String,
  val gap: Boolean = false
)

data class LinkState(val state: String, val since: Long)

class LiveBus(capacity: Int = 64) {
  companion object {
    val shared = LiveBus()
  }

  private val wire = Json { ignoreUnknownKeys = true }
  private val mutable =
    MutableSharedFlow<LiveEvent>(
      extraBufferCapacity = capacity,
      onBufferOverflow = BufferOverflow.DROP_OLDEST
    )
  val events: SharedFlow<LiveEvent> = mutable.asSharedFlow()

  private val mutableLink =
    MutableSharedFlow<LinkState>(
      extraBufferCapacity = 16,
      onBufferOverflow = BufferOverflow.DROP_OLDEST
    )
  val links: SharedFlow<LinkState> = mutableLink.asSharedFlow()

  private val pages = mutableMapOf<String, Int>()

  @Synchronized
  fun publish(fleet: String, topic: String, json: String) {
    if (topic == "hello") pages.remove(fleet)
    val gap = topic == "timeline" && gap(fleet, json)
    mutable.tryEmit(LiveEvent(fleet, topic, json, gap))
  }

  @Synchronized
  fun link(state: String, since: Long) {
    mutableLink.tryEmit(LinkState(state, since))
  }

  @Synchronized
  fun reset() {
    pages.clear()
  }

  private fun gap(fleet: String, json: String): Boolean {
    val root =
      try {
        wire.parseToJsonElement(json).jsonObject
      } catch (_: Exception) {
        return false
      }
    val from = root["from"]?.jsonPrimitive?.intOrNull ?: return false
    val to = root["to"]?.jsonPrimitive?.intOrNull ?: return false
    val snapshot = root["snapshot"]?.jsonPrimitive?.booleanOrNull == true
    val previous = pages[fleet]
    pages[fleet] = to
    return !snapshot && previous != null && previous != from
  }
}
