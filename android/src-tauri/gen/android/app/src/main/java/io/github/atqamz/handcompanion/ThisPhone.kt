package io.github.atqamz.handcompanion

import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject

const val SIGN_OUT_DAYS = 90L
private const val DAY_SECONDS = 24 * 60 * 60L

data class PairedFleet(val id: String, val name: String, val paired: Long)

data class PhoneInfo(val id: String, val core: String, val fleets: List<PairedFleet>)

data class FleetLine(val id: String, val name: String, val dates: String)

data class ThisPhone(
  val loaded: Boolean = true,
  val laptop: String? = null,
  val link: String = "connected",
  val fleets: List<FleetLine> = emptyList(),
  val alerts: String = "Alerts are on",
  val alertsDetail: String = "",
  val phoneId: String? = null,
  val app: String = "",
  val core: String? = null,
  val asking: Boolean = false
)

fun phoneInfo(text: String?): PhoneInfo? {
  val root = parse(text)
  val id = root.field("id") ?: return null
  val fleets =
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
  return PhoneInfo(id, root.field("core").orEmpty(), fleets)
}

fun signOutSeconds(paired: Long) = paired + SIGN_OUT_DAYS * DAY_SECONDS

fun signOutPath(fleet: String) = "/$fleet/connect"

private fun day(seconds: Long) =
  SimpleDateFormat("MMM d", Locale.getDefault()).format(Date(seconds * 1000))

fun pairingDates(paired: Long) =
  if (paired <= 0) "pairing date not recorded"
  else "paired ${day(paired)} · signs out ${day(signOutSeconds(paired))}"

fun thisPhone(state: FleetsState, app: String): ThisPhone {
  val info = state.phone
  val fleets =
    info?.fleets.orEmpty().map { stored ->
      val live = state.rows.firstOrNull { it.id == stored.id }?.name?.takeIf { it != stored.id }
      FleetLine(stored.id, live ?: stored.name.ifBlank { stored.id }, pairingDates(stored.paired))
    }
  val (alerts, detail) =
    when {
      state.alertsOff -> "Alerts are off" to "Allow notifications in Settings."
      fleets.isEmpty() -> "Alerts are off" to "No fleet is paired."
      else -> "Alerts are on" to "watching ${fleets.size} fleet${if (fleets.size == 1) "" else "s"}"
    }
  return ThisPhone(
    loaded = state.loaded,
    laptop = state.laptop,
    link = if (state.band == null || state.band == UNSAVED) "connected" else "not connected",
    fleets = fleets,
    alerts = alerts,
    alertsDetail = detail,
    phoneId = info?.id,
    app = app,
    core = info?.core?.ifEmpty { null },
    asking = state.asking
  )
}
