package io.github.atqamz.handcompanion

import android.content.res.Configuration
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

@Composable
internal fun AttachChips(
  attached: List<Attached>,
  uploading: List<String>,
  enabled: Boolean,
  onRemove: (Int) -> Unit
) {
  val hand = LocalHand.current
  val coarse = LocalConfiguration.current.touchscreen != Configuration.TOUCHSCREEN_NOTOUCH
  val shape = RoundedCornerShape(6.dp)
  Row(
    Modifier.horizontalScroll(rememberScrollState()).padding(horizontal = 10.dp, vertical = 4.dp),
    horizontalArrangement = Arrangement.spacedBy(8.dp)
  ) {
    attached.forEachIndexed { index, file ->
      val label = file.label.ifEmpty { file.name }
      Row(
        Modifier.clip(shape)
          .background(hand.card)
          .border(1.dp, hand.line, shape)
          .heightIn(min = if (coarse) 44.dp else 32.dp)
          .padding(start = 10.dp),
        verticalAlignment = Alignment.CenterVertically
      ) {
        Text("${index + 1}", color = hand.muted, style = type.labelLarge)
        Text(
          label,
          Modifier.padding(start = 8.dp).widthIn(max = 160.dp),
          color = hand.fg,
          style = type.bodyMedium,
          maxLines = 1,
          overflow = TextOverflow.Ellipsis
        )
        Box(
          Modifier.heightIn(min = if (coarse) 44.dp else 32.dp)
            .widthIn(min = if (coarse) 44.dp else 32.dp)
            .semantics { contentDescription = "Remove $label" }
            .clickable(enabled = enabled, role = Role.Button) { onRemove(index + 1) },
          contentAlignment = Alignment.Center
        ) {
          Text("×", color = hand.muted, style = type.titleMedium)
        }
      }
    }
    uploading.forEach { name ->
      Row(
        Modifier.clip(shape)
          .border(1.dp, hand.line, shape)
          .heightIn(min = if (coarse) 44.dp else 32.dp)
          .padding(horizontal = 10.dp),
        verticalAlignment = Alignment.CenterVertically
      ) {
        Text(
          name,
          Modifier.widthIn(max = 160.dp),
          color = hand.muted,
          style = type.bodyMedium,
          maxLines = 1,
          overflow = TextOverflow.Ellipsis
        )
        Text("  uploading", color = hand.muted, style = type.bodySmall)
      }
    }
  }
}
