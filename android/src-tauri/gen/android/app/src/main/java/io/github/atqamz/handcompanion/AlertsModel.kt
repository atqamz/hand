package io.github.atqamz.handcompanion

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import java.util.TimeZone
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class AlertReceipt(
  val seq: Long,
  val at: String = "",
  val kind: String = "",
  val ref: String = "",
  val text: String = "",
  @SerialName("fetched_at") val fetchedAt: String = "",
  @SerialName("acked_at") val ackedAt: String = "",
  @SerialName("delay_seconds") val delaySeconds: Long? = null,
  val slow: Boolean = false
)

@Serializable
data class AlertStats(
  val total: Int = 0,
  val unacked: Int = 0,
  val slow: Int = 0,
  @SerialName("p50_seconds") val p50: Long? = null,
  @SerialName("p95_seconds") val p95: Long? = null,
  @SerialName("max_seconds") val max: Long? = null
)

@Serializable
data class AlertsData(
  val days: Int = 7,
  @SerialName("slow_after_seconds") val slowAfter: Int = 60,
  val stats: AlertStats = AlertStats(),
  val alerts: List<AlertReceipt> = emptyList()
)

data class AlertsState(
  val data: AlertsData? = null,
  val loading: Boolean = true,
  val problem: String? = null,
  val unpaired: Boolean = false,
  val tooOld: Boolean = false
)

const val ALERTS_TOO_OLD = "This host is too old to list alerts. Update Hand on the host."

const val ALERTS_NONE = "No alerts in the last 7 days."

const val ALERTS_NOTE =
  "The last 7 days. Delay is the time from raised to acknowledged. Over 1 minute counts as slow."

data class AlertLine(
  val seq: Long,
  val raised: String,
  val kind: String,
  val ref: String,
  val text: String,
  val delivered: String,
  val acknowledged: String,
  val delay: String,
  val slow: Boolean
)

fun alertDuration(seconds: Long): String {
  val hours = seconds / 3600
  val minutes = seconds % 3600 / 60
  val rest = seconds % 60
  return when {
    hours > 0 -> "${hours}h${minutes}m${rest}s"
    minutes > 0 -> "${minutes}m${rest}s"
    else -> "${rest}s"
  }
}

fun alertLine(alert: AlertReceipt, zone: TimeZone): AlertLine =
  AlertLine(
    alert.seq,
    historyWhen(alert.at, zone),
    alert.kind,
    alert.ref,
    alert.text,
    alert.fetchedAt.takeIf(String::isNotEmpty)?.let { historyWhen(it, zone) } ?: "not delivered",
    alert.ackedAt.takeIf(String::isNotEmpty)?.let { historyWhen(it, zone) } ?: "not acknowledged",
    alert.delaySeconds?.let(::alertDuration) ?: "no delay yet",
    alert.slow
  )

fun alertStatCells(stats: AlertStats): List<Pair<String, String>> =
  buildList {
    add("alerts" to stats.total.toString())
    add("not acknowledged" to stats.unacked.toString())
    add("slow" to stats.slow.toString())
    if (stats.p50 != null && stats.p95 != null && stats.max != null) {
      add("median delay" to alertDuration(stats.p50))
      add("95th percentile delay" to alertDuration(stats.p95))
      add("longest delay" to alertDuration(stats.max))
    }
  }

fun alertsNewestFirst(data: AlertsData): AlertsData =
  data.copy(alerts = data.alerts.sortedByDescending { it.seq })

class AlertsModel(
  fleet: String,
  transport: Transport,
  io: CoroutineDispatcher = Dispatchers.IO
) : ViewModel() {
  private val mutableState = MutableStateFlow(AlertsState())
  val state: StateFlow<AlertsState> = mutableState

  private val client = BoardClient(fleet, transport, io)
  private val lock = Mutex()

  fun reload() {
    viewModelScope.launch { load() }
  }

  suspend fun load() {
    if (!lock.tryLock()) return
    try {
      withContext(NonCancellable) { read() }
    } finally {
      lock.unlock()
    }
  }

  private suspend fun read() {
    mutableState.update { it.copy(loading = true, problem = null) }
    try {
      if ("alerts" !in client.features()) {
        mutableState.update { it.copy(loading = false, tooOld = true, data = null) }
        return
      }
      val data = alertsNewestFirst(client.read("alerts", AlertsData.serializer()).data)
      mutableState.update {
        it.copy(data = data, loading = false, problem = null, unpaired = false, tooOld = false)
      }
    } catch (error: BoardError) {
      mutableState.update {
        if (error == BoardError.Unpaired) it.copy(loading = false, unpaired = true)
        else it.copy(loading = false, problem = historyProblem(error))
      }
    }
  }
}
