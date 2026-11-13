package io.github.atqamz.handcompanion

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp

@Composable
private fun KeyDot(key: String, on: Boolean) {
  val hand = LocalHand.current
  val accent = hand.ink(Tone.Accent)
  Box(
    Modifier.size(28.dp)
      .background(if (on) accent else Color.Transparent, CircleShape)
      .border(BorderStroke(1.5.dp, if (on) accent else hand.lineStrong), CircleShape),
    contentAlignment = Alignment.Center
  ) {
    Text(key, color = if (on) hand.bg else hand.fg, style = type.labelMedium)
  }
}

@Composable
private fun Options(options: List<ChoiceOption>, picked: String?, answered: Boolean, onPick: (String) -> Unit) {
  val hand = LocalHand.current
  val shape = MaterialTheme.shapes.large
  Column(Modifier.fillMaxWidth().clip(shape).border(1.dp, hand.line, shape).selectableGroup()) {
    options.forEachIndexed { index, option ->
      val on = option.key == picked
      if (index > 0) HorizontalDivider(color = hand.line)
      Row(
        Modifier.fillMaxWidth()
          .background(if (on) hand.fill(Tone.Accent) else Color.Transparent)
          .selectable(on, !answered, Role.RadioButton) { onPick(option.key) }
          .heightIn(min = 48.dp)
          .padding(16.dp, 12.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp)
      ) {
        KeyDot(option.key, on)
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
          Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(
              option.label,
              Modifier.weight(1f, fill = false),
              color = if (answered && !on) hand.muted else hand.fg,
              style = type.titleMedium
            )
            when {
              answered && on -> Chip("Your answer", Tone.Accent, 20)
              option.recommended -> Chip("Recommended", Tone.Pass, 20)
            }
          }
          if (option.note.isNotEmpty()) Text(option.note, color = hand.muted, style = type.bodyMedium)
        }
      }
    }
  }
}

@Composable
private fun Problem(text: String) =
  Text(
    text,
    Modifier.semantics { liveRegion = LiveRegionMode.Polite },
    color = LocalHand.current.ink(Tone.Fail),
    style = type.bodyMedium
  )

@Composable
private fun Answered(detail: DecisionDetail) {
  val hand = LocalHand.current
  val info = detail.decision
  val by = if (info.answeredBy.isEmpty()) "" else " by ${info.answeredBy}"
  Meta(if (info.status == "withdrawn") "Withdrawn by the supervisor" else "Answered$by")
  val note = answerNote(detail.options, info.answer)
  if (note.isNotEmpty()) {
    Text(
      note,
      Modifier.fillMaxWidth().background(hand.panel, MaterialTheme.shapes.large).padding(12.dp),
      style = type.bodyMedium
    )
  }
}

@Composable
fun ChoiceForm(
  ref: String,
  state: DecisionState,
  onPick: (String) -> Unit,
  onNote: (String) -> Unit,
  onSend: () -> Unit,
  onRetry: () -> Unit,
  onOpen: (() -> Unit)? = null
) {
  val detail = state.detail
  val hand = LocalHand.current
  when {
    state.unpaired -> Text("No longer paired", color = hand.ink(Tone.Fail), style = type.titleMedium)
    detail == null -> if (state.failed) BandView(Band("Could not read $ref."), onRetry)
    else ->
      Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        if (state.answered) {
          state.message?.let { Problem(it) }
          Options(detail.options, chosenKey(detail.options, detail.decision.answer), true, onPick)
          Answered(detail)
        } else {
          Options(detail.options, state.picked, false, onPick)
          val other = detail.options.firstOrNull { it.key == state.picked }?.other == true
          OutlinedTextField(
            state.note,
            onNote,
            Modifier.fillMaxWidth(),
            enabled = !state.sending,
            placeholder = {
              Text(
                when {
                  other -> "Write your answer"
                  detail.options.isEmpty() -> "Your answer"
                  else -> "Add a note (optional)"
                }
              )
            },
            shape = MaterialTheme.shapes.medium
          )
          Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(
              onSend,
              Modifier.weight(1f).heightIn(min = 48.dp),
              enabled = !state.sending,
              shape = MaterialTheme.shapes.medium
            ) {
              Text(if (state.sending) "Sending…" else "Send answer")
            }
            onOpen?.let { Action("Open $ref", it, Modifier.height(48.dp)) }
          }
          state.message?.let { Problem(it) }
        }
      }
  }
}

@Composable
fun DecisionScreen(
  ref: String,
  state: DecisionState,
  onBack: () -> Unit,
  onPick: (String) -> Unit,
  onNote: (String) -> Unit,
  onSend: () -> Unit,
  onRetry: () -> Unit
) {
  val hand = LocalHand.current
  Column(Modifier.fillMaxSize().safeDrawingPadding()) {
    Row(
      Modifier.fillMaxWidth().height(56.dp).padding(end = 16.dp),
      verticalAlignment = Alignment.CenterVertically,
      horizontalArrangement = Arrangement.spacedBy(8.dp)
    ) {
      IconButton(onBack) { Icon(HandIcons.Back, "Back", Modifier.size(20.dp)) }
      Text(ref, color = hand.muted, style = type.titleMedium)
    }
    HorizontalDivider(color = hand.line)
    Column(
      Modifier.weight(1f).verticalScroll(rememberScrollState()).padding(16.dp),
      verticalArrangement = Arrangement.spacedBy(16.dp)
    ) {
      state.detail?.let { detail ->
        val info = detail.decision
        Text(info.headline, style = type.headlineMedium)
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
          Chip(info.status, if (info.status == "open") Tone.Wait else Tone.Neutral, 24)
          info.createdAt?.let(::localTime)?.let { Meta(it) }
        }
        prose(detail.body, detail.options).takeIf { it.isNotEmpty() }?.let {
          Text(it, style = type.bodyLarge)
        }
        Meta("for ${detail.task.ref} ${detail.task.title}")
      }
      ChoiceForm(ref, state, onPick, onNote, onSend, onRetry)
    }
  }
}
