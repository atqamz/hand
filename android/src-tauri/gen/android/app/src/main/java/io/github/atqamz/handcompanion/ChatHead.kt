package io.github.atqamz.handcompanion

import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

enum class HeadControl(val label: String) {
  Interrupt("Interrupt"),
  Stop("Stop"),
  Resume("Resume"),
  Start("Start new")
}

data class HeadNote(
  val text: String,
  val tone: Tone = Tone.Neutral,
  val warn: String? = null,
  val token: Long = 0
)

data class ChatHead(
  val title: String,
  val controls: List<HeadControl>,
  val stopArmed: Boolean = false,
  val reason: String? = null,
  val busy: Boolean = false,
  val note: HeadNote? = null
) {
  val enabled: Boolean
    get() = !busy && reason == null

  val sendPrimary: Boolean
    get() = HeadControl.Resume !in controls && HeadControl.Start !in controls
}

const val STOP_ARM_MILLIS = 4_000L
const val RECEIPT_MILLIS = 4_000L
const val ERROR_MILLIS = 8_000L
const val WARN_MILLIS = 12_000L

const val NOTHING_TO_RESUME = "There is nothing to resume."
const val CONTROLS_OFF = "The Board's controls are off."
const val CONTROL_UNREACHABLE = "Your host is not reachable."

fun supervisorWords(supervisor: Supervisor?): String =
  when {
    supervisor == null -> "No supervisor"
    supervisor.live && supervisor.status != "running" -> "starting…"
    supervisor.status == "running" -> {
      val model = supervisor.model?.takeIf(String::isNotEmpty) ?: supervisor.harness
      listOfNotNull(model, supervisor.effort?.takeIf(String::isNotEmpty)).joinToString(" · ")
    }
    else -> "${supervisor.ref} ${supervisor.status}"
  }

fun headControls(supervisor: Supervisor?, agentState: String): List<HeadControl> =
  when {
    supervisor == null -> listOf(HeadControl.Start)
    supervisor.live && supervisor.status != "running" -> emptyList()
    supervisor.status == "running" ->
      buildList {
        if (agentState == "working" && !supervisor.blocked) add(HeadControl.Interrupt)
        add(HeadControl.Stop)
      }
    else ->
      buildList {
        if (supervisor.resumable) add(HeadControl.Resume)
        add(HeadControl.Start)
      }
  }

fun chatHead(
  supervisor: Supervisor?,
  agentState: String,
  reachable: Boolean = true,
  reason: String? = null,
  busy: Boolean = false,
  stopArmed: Boolean = false,
  note: HeadNote? = null
): ChatHead =
  ChatHead(
    title = supervisorWords(supervisor),
    controls = headControls(supervisor, agentState),
    stopArmed = stopArmed,
    reason = if (reachable) null else reason ?: CONTROL_UNREACHABLE,
    busy = busy,
    note = note
  )

fun FleetState.head() =
  chatHead(
    supervisor = supervisor,
    agentState = agentState,
    reachable = reachable,
    reason = band?.title,
    busy = controlBusy != null,
    stopArmed = stopArmedUntil != null,
    note = headNote
  )

data class StopPress(val armedUntil: Long?, val send: Boolean)

fun stopPress(armedUntil: Long?, now: Long, armMillis: Long = STOP_ARM_MILLIS): StopPress =
  if (armedUntil != null && now < armedUntil) StopPress(null, true)
  else StopPress(now + armMillis, false)

fun controlPath(control: HeadControl): String =
  when (control) {
    HeadControl.Interrupt -> "supervisor/interrupt"
    HeadControl.Stop -> "supervisor/stop"
    HeadControl.Resume -> "supervisor/resume"
    HeadControl.Start -> "supervisor/start"
  }

fun controlBody(control: HeadControl, ref: String): String? =
  when (control) {
    HeadControl.Interrupt,
    HeadControl.Stop -> buildJsonObject { put("ref", ref) }.toString()
    HeadControl.Resume,
    HeadControl.Start -> null
  }

fun headNoteMillis(
  note: HeadNote,
  receiptMillis: Long = RECEIPT_MILLIS,
  errorMillis: Long = ERROR_MILLIS,
  warnMillis: Long = WARN_MILLIS
): Long =
  when {
    note.warn != null -> warnMillis
    note.tone == Tone.Fail -> errorMillis
    else -> receiptMillis
  }

fun headFailure(control: HeadControl, error: BoardError): String =
  when {
    error is BoardError.Conflict ->
      error.message?.takeIf(String::isNotBlank) ?: "The Board refused it; nothing was sent."
    error is BoardError.NotFound && control == HeadControl.Resume -> NOTHING_TO_RESUME
    error is BoardError.Forbidden -> CONTROLS_OFF
    error == BoardError.Unreachable || error == BoardError.NoHost -> CONTROL_UNREACHABLE
    error == BoardError.Lost || error == BoardError.TimedOut ->
      "Not sure the control reached your host."
    else -> error.message?.takeIf(String::isNotBlank) ?: "The control did not run."
  }
