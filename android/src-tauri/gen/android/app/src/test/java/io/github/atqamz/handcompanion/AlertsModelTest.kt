package io.github.atqamz.handcompanion

import java.util.TimeZone
import java.util.concurrent.CopyOnWriteArrayList
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private const val FLEET = "f000000000004"

private val utc: TimeZone = TimeZone.getTimeZone("UTC")

private fun receipt(seq: Int, at: String, delay: Long?, slow: Boolean, fetched: String, acked: String) =
  """{"seq":$seq,"at":"$at","kind":"attempt.failed","ref":"a$seq","text":"a$seq failed: boom",""" +
    """"fetched_at":"$fetched","acked_at":"$acked","delay_seconds":${delay ?: "null"},"slow":$slow}"""

internal val alertsRows =
  listOf(
    receipt(4, "2026-11-24T09:59:00Z", null, false, "2026-11-24T09:59:02Z", ""),
    receipt(3, "2026-11-24T09:00:00Z", 95, true, "2026-11-24T09:00:03Z", "2026-11-24T09:01:35Z"),
    receipt(2, "2026-11-23T18:30:00Z", 4, false, "2026-11-23T18:30:01Z", "2026-11-23T18:30:04Z"),
    receipt(1, "2026-11-23T08:15:00Z", 4, false, "2026-11-23T08:15:01Z", "2026-11-23T08:15:04Z")
  )

internal fun alertsJson(rows: List<String> = alertsRows, stats: String = STATS) =
  """{"days":7,"slow_after_seconds":60,"stats":$stats,"alerts":${rows.joinToString(",", "[", "]")}}"""

internal const val STATS =
  """{"total":4,"unacked":1,"slow":1,"p50_seconds":4,"p95_seconds":95,"max_seconds":95}"""

internal const val EMPTY_STATS =
  """{"total":0,"unacked":0,"slow":0,"p50_seconds":null,"p95_seconds":null,"max_seconds":null}"""

internal val alertsData = boardJson.decodeFromString(AlertsData.serializer(), alertsJson())

private class AlertsBoard(
  private val features: List<String> = listOf("alerts", "events"),
  var alerts: () -> Raw = { Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"cursor":"c1","data":${alertsJson()}}""") }
) : Transport {
  val calls = CopyOnWriteArrayList<String>()

  override fun request(fleet: String, post: Boolean, path: String, csrf: String?, body: String?): Raw {
    calls += "${if (post) "POST" else "GET"} $path"
    return when (path) {
      "/api/v1/meta" ->
        Raw(200, """{"version":1,"data":{"features":${features.joinToString(",", "[", "]") { "\"$it\"" }}}}""")
      "/api/v1/alerts" -> alerts()
      else -> Raw(404, """{"error":{"status":404,"code":"not_found","message":"not found"}}""")
    }
  }
}

class AlertsModelTest {
  @Test
  fun aReadChecksTheFeatureThenReadsTheAlertsOnly() = runBlocking {
    val board = AlertsBoard()
    val model = AlertsModel(FLEET, board)
    model.load()
    assertEquals(listOf("GET /api/v1/meta", "GET /api/v1/alerts"), board.calls.toList())
    assertFalse(model.state.value.loading)
    assertNull(model.state.value.problem)
    assertEquals(4, model.state.value.data!!.alerts.size)
  }

  @Test
  fun receiptsAreNewestFirstEvenWhenTheBoardSendsThemOldestFirst() = runBlocking {
    val board =
      AlertsBoard(
        alerts = {
          Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":${alertsJson(alertsRows.reversed())}}""")
        }
      )
    val model = AlertsModel(FLEET, board)
    model.load()
    assertEquals(listOf(4L, 3L, 2L, 1L), model.state.value.data!!.alerts.map { it.seq })
  }

  @Test
  fun theStatsCellsCarryTheTotalUnackedSlowAndEachPercentileUnderItsOwnName() {
    assertEquals(
      listOf(
        "alerts" to "4",
        "not acknowledged" to "1",
        "slow" to "1",
        "median delay" to "4s",
        "95th percentile delay" to "1m35s",
        "longest delay" to "1m35s"
      ),
      alertStatCells(alertsData.stats)
    )
  }

  @Test
  fun theMedianAndThe95thPercentileAreNotTheSameCell() {
    val stats = AlertStats(total = 5, unacked = 2, slow = 1, p50 = 10, p95 = 200, max = 3700)
    val cells = alertStatCells(stats).toMap()
    assertEquals("10s", cells["median delay"])
    assertEquals("3m20s", cells["95th percentile delay"])
    assertEquals("1h1m40s", cells["longest delay"])
    assertEquals("2", cells["not acknowledged"])
  }

  @Test
  fun noAcknowledgedAlertShowsOnlyTheCounts() {
    val cells = alertStatCells(boardJson.decodeFromString(AlertStats.serializer(), EMPTY_STATS))
    assertEquals(listOf("alerts", "not acknowledged", "slow"), cells.map { it.first })
  }

  @Test
  fun aReceiptLineSaysWhatWasDeliveredAcknowledgedAndHowLateItWas() {
    val lines = alertsData.alerts.map { alertLine(it, utc) }
    val unacked = lines[0]
    assertEquals("Tue 24 Nov 09:59", unacked.raised)
    assertEquals("Tue 24 Nov 09:59", unacked.delivered)
    assertEquals("not acknowledged", unacked.acknowledged)
    assertEquals("no delay yet", unacked.delay)
    assertFalse(unacked.slow)
    val slow = lines[1]
    assertEquals("1m35s", slow.delay)
    assertTrue(slow.slow)
    assertEquals("Tue 24 Nov 09:01", slow.acknowledged)
    assertEquals("attempt.failed", slow.kind)
    assertEquals("a3", slow.ref)
    assertEquals("a3 failed: boom", slow.text)
    assertEquals("4s", lines[2].delay)
    assertFalse(lines[2].slow)
    assertFalse(lines[3].slow)
  }

  @Test
  fun anUndeliveredReceiptSaysSo() {
    val line = alertLine(AlertReceipt(seq = 9, at = "2026-11-24T10:00:00Z", kind = "decision.asked"), utc)
    assertEquals("not delivered", line.delivered)
  }

  @Test
  fun durationsReadLikeTheWebPage() {
    assertEquals("0s", alertDuration(0))
    assertEquals("59s", alertDuration(59))
    assertEquals("1m0s", alertDuration(60))
    assertEquals("1h0m0s", alertDuration(3600))
  }

  @Test
  fun aBoardWithoutTheAlertsFeatureIsTooOldAndReadsNothing() = runBlocking {
    val board = AlertsBoard(features = listOf("events"))
    val model = AlertsModel(FLEET, board)
    model.load()
    assertTrue(model.state.value.tooOld)
    assertNull(model.state.value.problem)
    assertNull(model.state.value.data)
    assertEquals(listOf("GET /api/v1/meta"), board.calls.toList())
  }

  @Test
  fun theBoardsSentenceForARefusedReadIsShownAsWritten() = runBlocking {
    val board =
      AlertsBoard(
        alerts = { Raw(400, """{"error":{"status":400,"code":"bad_request","message":"the board says no"}}""") }
      )
    val model = AlertsModel(FLEET, board)
    model.load()
    assertEquals("the board says no", model.state.value.problem)
    assertFalse(model.state.value.loading)
    assertNull(model.state.value.data)
  }

  @Test
  fun anUnreachableBoardAfterAReadKeepsTheRowsAndMarksThemStale() = runBlocking {
    val board = AlertsBoard()
    val model = AlertsModel(FLEET, board)
    model.load()
    board.alerts = { throw BoardError.Unreachable }
    model.load()
    assertEquals("The Board could not be reached.", model.state.value.problem)
    assertNotNull(model.state.value.data)
    assertEquals(4, model.state.value.data!!.alerts.size)
  }

  @Test
  fun aRetryAfterAFailureClearsTheProblem() = runBlocking {
    val board = AlertsBoard(alerts = { throw BoardError.TimedOut })
    val model = AlertsModel(FLEET, board)
    model.load()
    assertEquals("The Board did not answer in time.", model.state.value.problem)
    board.alerts = { Raw(200, """{"version":1,"fleet":{"id":"$FLEET","name":"atlas"},"data":${alertsJson(emptyList(), EMPTY_STATS)}}""") }
    model.load()
    assertNull(model.state.value.problem)
    assertTrue(model.state.value.data!!.alerts.isEmpty())
  }

  @Test
  fun anUnpairedCompanionIsToldSoNotGivenAnError() = runBlocking {
    val model = AlertsModel(FLEET, AlertsBoard(alerts = { throw BoardError.Unpaired }))
    model.load()
    assertTrue(model.state.value.unpaired)
    assertNull(model.state.value.problem)
  }
}
