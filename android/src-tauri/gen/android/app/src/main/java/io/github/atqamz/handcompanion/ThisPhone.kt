package io.github.atqamz.handcompanion

import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject

const val SIGN_OUT_DAYS = 90L
private const val DAY_SECONDS = 24 * 60 * 60L

data class PairedFleet(val id: String, val name: String, val paired: Long)

data class PairedHost(
  val endpoint: String,
  val id: String,
  val name: String,
  val fleets: List<PairedFleet>
)

data class PhoneInfo(val id: String, val core: String, val hosts: List<PairedHost>) {
  val fleets get() = hosts.flatMap(PairedHost::fleets)
}

data class FleetLine(val id: String, val name: String, val dates: String)

data class HostLine(
  val endpoint: String,
  val name: String,
  val id: String,
  val link: String,
  val fleets: Int
) {
  val detail get() =
    listOfNotNull(id.takeIf { it != name }, "$fleets fleet${if (fleets == 1) "" else "s"}", link)
      .joinToString(" · ")
}

data class ThisPhone(
  val loaded: Boolean = true,
  val hosts: List<HostLine> = emptyList(),
  val fleets: List<FleetLine> = emptyList(),
  val alerts: String = "Alerts are on",
  val alertsDetail: String = "",
  val phoneId: String? = null,
  val app: String = "",
  val core: String? = null,
  val asking: Forgetting? = null
)

private fun fleetsOf(root: JsonObject) =
  (root["fleets"] as? JsonArray)
    ?.mapNotNull { item ->
      val fleet = item as? JsonObject ?: return@mapNotNull null
      PairedFleet(
        fleet.field("id") ?: return@mapNotNull null,
        fleet.field("name").orEmpty(),
        fleet.field("paired")?.toLongOrNull() ?: 0L
      )
    }
    .orEmpty()

fun phoneInfo(text: String?): PhoneInfo? {
  val root = parse(text)
  val id = root.field("id") ?: return null
  val hosts =
    (root["hosts"] as? JsonArray)
      ?.mapNotNull { item ->
        val host = item as? JsonObject ?: return@mapNotNull null
        val endpoint = host.field("endpoint") ?: return@mapNotNull null
        val short = host.field("id").orEmpty()
        PairedHost(endpoint, short, host.field("name")?.ifBlank { null } ?: short, fleetsOf(host))
      }
      .orEmpty()
  return PhoneInfo(id, root.field("core").orEmpty(), hosts)
}

fun noticeHost(info: PhoneInfo?, fleet: String): String? =
  info
    ?.hosts
    ?.takeIf { it.size >= 2 }
    ?.firstOrNull { host -> host.fleets.any { it.id == fleet } }
    ?.name

fun signOutSeconds(paired: Long) = paired + SIGN_OUT_DAYS * DAY_SECONDS

fun signOutPath(fleet: String) = "/$fleet/connect"

private fun day(seconds: Long) =
  SimpleDateFormat("MMM d", Locale.getDefault()).format(Date(seconds * 1000))

fun pairingDates(paired: Long) =
  if (paired <= 0) "pairing date not recorded"
  else "paired ${day(paired)} · signs out ${day(signOutSeconds(paired))}"

private fun link(group: HostGroup) =
  when {
    group.unpaired -> "not paired anymore"
    group.band == null -> "connected"
    else -> "not connected"
  }

fun thisPhone(state: FleetsState, app: String): ThisPhone {
  val info = state.phone
  val fleets =
    info?.fleets.orEmpty().map { stored ->
      val live = state.rows.firstOrNull { it.id == stored.id }?.name?.takeIf { it != stored.id }
      FleetLine(stored.id, live ?: stored.name.ifBlank { stored.id }, pairingDates(stored.paired))
    }
  val hosts =
    info?.hosts.orEmpty().map { host ->
      val group = state.hosts.firstOrNull { it.endpoint == host.endpoint } ?: HostGroup(host.endpoint, host.id, host.name)
      HostLine(host.endpoint, host.name, host.id, link(group), host.fleets.size)
    }
  val link =
    when {
      hosts.all { it.link == "connected" } -> "connected"
      hosts.none { it.link == "connected" } -> "not connected"
      else -> "partly connected"
    }
  val (alerts, detail) =
    when {
      state.alertsOff -> "Alerts are off" to "Allow notifications in Settings."
      fleets.isEmpty() -> "Alerts are off" to "No fleet is paired."
      else ->
        "Alerts are on" to
          "Hand is $link · watching ${fleets.size} fleet${if (fleets.size == 1) "" else "s"}"
    }
  return ThisPhone(
    loaded = state.loaded,
    hosts = hosts,
    fleets = fleets,
    alerts = alerts,
    alertsDetail = detail,
    phoneId = info?.id,
    app = app,
    core = info?.core?.ifEmpty { null },
    asking = state.asking
  )
}
