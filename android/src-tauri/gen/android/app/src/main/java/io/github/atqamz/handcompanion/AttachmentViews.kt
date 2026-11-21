package io.github.atqamz.handcompanion

import android.graphics.Bitmap
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

internal val LocalAttachments = compositionLocalOf { appAttachments }

private val PICTURE_WIDTH = 280.dp
private val PICTURE_HEIGHT = 240.dp

internal sealed interface Loaded {
  data object Busy : Loaded

  data object Failed : Loaded

  class Ready(val bitmap: Bitmap) : Loaded
}

@Composable
internal fun AttachedBody(fleet: String, text: String, shots: List<Shot>) {
  val parts = remember(text, shots) { inlineParts(text, shots) }
  Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
    parts.forEach { part ->
      when (part) {
        is Part.Words -> Text(shown(part.text), style = type.bodyMedium)
        is Part.Piece -> AttachedPiece(fleet, part.shot)
      }
    }
  }
}

@Composable
private fun AttachedPiece(fleet: String, shot: Shot) {
  if (shot.missing) {
    AttachedCard(shot, "no longer in the inbox", null)
  } else if (!shot.image) {
    AttachedCard(
      shot,
      listOf(shot.kind, shot.size).filter(String::isNotEmpty).joinToString(" · "),
      null
    )
  } else {
    AttachedPicture(fleet, shot)
  }
}

@Composable
private fun AttachedPicture(fleet: String, shot: Shot) {
  val loader = LocalAttachments.current
  var attempt by remember { mutableIntStateOf(0) }
  val start = { loader.cached(fleet, shot.name)?.let { Loaded.Ready(it) } ?: Loaded.Busy }
  val loaded by
    produceState(start(), fleet, shot.name, attempt) {
      value = start()
      if (value is Loaded.Busy) {
        value = loader.image(fleet, shot.name)?.let { Loaded.Ready(it) } ?: Loaded.Failed
      }
    }
  AttachedLoaded(shot, loaded) { attempt++ }
}

@Composable
internal fun AttachedLoaded(shot: Shot, loaded: Loaded, onRetry: () -> Unit) {
  when (loaded) {
    is Loaded.Ready ->
      Image(
        loaded.bitmap.asImageBitmap(),
        contentDescription = shot.label,
        modifier =
          Modifier.widthIn(max = PICTURE_WIDTH)
            .heightIn(max = PICTURE_HEIGHT)
            .clip(RoundedCornerShape(8.dp)),
        contentScale = ContentScale.Fit
      )
    Loaded.Busy -> AttachedCard(shot, "loading", null)
    Loaded.Failed -> AttachedCard(shot, "could not load, tap to retry", onRetry)
  }
}

@Composable
private fun AttachedCard(shot: Shot, note: String, onClick: (() -> Unit)?) {
  val hand = LocalHand.current
  val shape = RoundedCornerShape(8.dp)
  Column(
    Modifier.widthIn(max = PICTURE_WIDTH)
      .fillMaxWidth()
      .clip(shape)
      .background(hand.card)
      .border(1.dp, hand.line, shape)
      .let { if (onClick != null) it.clickable(role = Role.Button, onClick = onClick) else it }
      .heightIn(min = 48.dp)
      .padding(10.dp, 8.dp),
    verticalArrangement = Arrangement.Center
  ) {
    Text(
      shot.label.ifEmpty { shot.name },
      color = hand.ink(Tone.Accent),
      style = type.bodyMedium,
      maxLines = 1,
      overflow = TextOverflow.Ellipsis
    )
    if (note.isNotEmpty()) Text(note, color = hand.muted, style = type.bodySmall)
  }
}
