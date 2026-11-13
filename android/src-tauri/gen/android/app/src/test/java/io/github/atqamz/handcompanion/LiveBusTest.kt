package io.github.atqamz.handcompanion

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.onSubscription
import kotlinx.coroutines.flow.take
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

private const val FLEET = "f000000000003"

private fun timeline(snapshot: Boolean, from: Int, to: Int) =
  """{"snapshot":$snapshot,"from":$from,"to":$to,"entries":[]}"""

class LiveBusTest {
  @Test
  fun theOldestEventDropsWhenTheBufferOverflows() = runBlocking {
    val bus = LiveBus(2)
    val gate = CompletableDeferred<Unit>()
    val ready = CompletableDeferred<Unit>()
    val seen = mutableListOf<String>()
    val collector =
      launch(Dispatchers.Default) {
        bus.events.onSubscription { ready.complete(Unit) }.collect {
          seen += it.topic
          if (seen.size == 1) gate.await()
        }
      }
    ready.await()
    bus.publish(FLEET, "e0", "{}")
    withTimeout(5_000) { while (seen.isEmpty()) delay(5) }
    for (topic in listOf("e1", "e2", "e3", "e4")) bus.publish(FLEET, topic, "{}")
    gate.complete(Unit)
    withTimeout(5_000) { while (seen.size < 3) delay(5) }
    collector.cancelAndJoin()
    assertEquals(listOf("e0", "e3", "e4"), seen)
  }

  @Test
  fun aTimelineThatDoesNotStartWhereTheLastEndedIsAGap() = runBlocking {
    val bus = LiveBus(8)
    val seen = mutableListOf<LiveEvent>()
    val ready = CompletableDeferred<Unit>()
    val collector =
      launch(Dispatchers.Default) {
        bus.events.onSubscription { ready.complete(Unit) }.take(3).toList(seen)
      }
    ready.await()
    bus.publish(FLEET, "timeline", timeline(true, 0, 5))
    bus.publish(FLEET, "timeline", timeline(false, 5, 8))
    bus.publish(FLEET, "timeline", timeline(false, 6, 9))
    collector.join()
    assertEquals(listOf(false, false, true), seen.map { it.gap })
  }

  @Test
  fun aFreshConnectionMakesTheNextTimelineASnapshotAgain() = runBlocking {
    val bus = LiveBus(8)
    val seen = mutableListOf<LiveEvent>()
    val ready = CompletableDeferred<Unit>()
    val collector =
      launch(Dispatchers.Default) {
        bus.events.onSubscription { ready.complete(Unit) }.take(3).toList(seen)
      }
    ready.await()
    bus.publish(FLEET, "timeline", timeline(true, 0, 5))
    bus.publish(FLEET, "hello", """{"version":1}""")
    bus.publish(FLEET, "timeline", timeline(true, 40, 44))
    collector.join()
    assertFalse(seen[2].gap)
  }

  @Test
  fun theLinkStateCrossesTheBus() = runBlocking {
    val bus = LiveBus(8)
    val seen = mutableListOf<LinkState>()
    val ready = CompletableDeferred<Unit>()
    val collector =
      launch(Dispatchers.Default) {
        bus.links.onSubscription { ready.complete(Unit) }.take(2).toList(seen)
      }
    ready.await()
    bus.link("live", 0)
    bus.link("reconnecting", 1_700_000_000_000)
    collector.join()
    assertEquals(
      listOf(LinkState("live", 0), LinkState("reconnecting", 1_700_000_000_000)),
      seen
    )
  }

  @Test
  fun aGapIsOnlyReportedForTheFleetThatMoved() = runBlocking {
    val bus = LiveBus(8)
    val other = "f999999999999"
    val seen = mutableListOf<LiveEvent>()
    val ready = CompletableDeferred<Unit>()
    val collector =
      launch(Dispatchers.Default) {
        bus.events.onSubscription { ready.complete(Unit) }.take(3).toList(seen)
      }
    ready.await()
    bus.publish(FLEET, "timeline", timeline(true, 0, 5))
    bus.publish(other, "timeline", timeline(true, 0, 9))
    bus.publish(FLEET, "timeline", timeline(false, 7, 9))
    collector.join()
    assertTrue(seen[2].gap)
  }
}
