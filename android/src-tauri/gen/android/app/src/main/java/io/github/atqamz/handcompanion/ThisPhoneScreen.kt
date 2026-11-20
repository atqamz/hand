package io.github.atqamz.handcompanion

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
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
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.key
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

@Composable
private fun Section(title: String) {
  Text(
    title,
    Modifier.fillMaxWidth().padding(16.dp, 20.dp, 16.dp, 8.dp),
    color = LocalHand.current.muted,
    style = type.labelMedium.copy(fontWeight = FontWeight.SemiBold)
  )
  HorizontalDivider(color = LocalHand.current.line)
}

@Composable
private fun Line(
  title: String,
  detail: String?,
  mono: Boolean = false,
  monoDetail: Boolean = false,
  action: @Composable () -> Unit = {}
) {
  Row(
    Modifier.fillMaxWidth().heightIn(min = 64.dp).padding(16.dp, 10.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(12.dp)
  ) {
    Column(Modifier.weight(1f)) {
      Text(
        title,
        maxLines = 1,
        overflow = TextOverflow.Ellipsis,
        style = if (mono) HandMono else type.titleMedium
      )
      detail?.takeIf(String::isNotEmpty)?.let {
        if (monoDetail) Text(it, color = LocalHand.current.muted, style = HandMono) else Meta(it)
      }
    }
    action()
  }
  HorizontalDivider(color = LocalHand.current.line)
}

@Composable
fun ThisPhoneScreen(
  phone: ThisPhone,
  onBack: () -> Unit,
  onAsk: (Forgetting?) -> Unit,
  onForget: () -> Unit,
  onSignOut: (String) -> Unit,
  onSettings: () -> Unit
) {
  Column(Modifier.fillMaxSize().safeDrawingPadding()) {
    Row(
      Modifier.fillMaxWidth().height(56.dp).padding(end = 16.dp),
      verticalAlignment = Alignment.CenterVertically,
      horizontalArrangement = Arrangement.spacedBy(8.dp)
    ) {
      IconButton(onBack) { Icon(HandIcons.Back, "Back", Modifier.size(20.dp)) }
      Text("This Companion", style = type.headlineSmall)
    }
    HorizontalDivider(color = LocalHand.current.line)
    phone.notice?.let {
      Text(
        it,
        Modifier.fillMaxWidth().background(LocalHand.current.fill(Tone.Neutral)).padding(16.dp, 12.dp),
        style = type.bodyMedium
      )
      HorizontalDivider(color = LocalHand.current.line)
    }
    if (!phone.loaded) {
      Loading()
    } else {
      Column(Modifier.weight(1f).verticalScroll(rememberScrollState())) {
        Section("Hosts")
        if (phone.hosts.isEmpty()) Line("No host paired", null)
        phone.hosts.forEach { host ->
          Line(host.name, host.detail, mono = true) {
            Action(
              "Forget",
              { onAsk(Forgetting(host.endpoint, host.name, host.fleets)) },
              Modifier.semantics { contentDescription = "Forget ${host.name}" }
            )
          }
        }
        if (phone.hosts.size > 1) {
          Line("Forget everything", "Every host and all ${phone.fleets.size} fleets") {
            Action(
              "Forget",
              { onAsk(Forgetting(null, null, phone.fleets.size)) },
              Modifier.semantics { contentDescription = "Forget everything" }
            )
          }
        }
        Section("Fleets")
        if (phone.fleets.isEmpty()) Line("No fleet is paired", null)
        phone.fleets.forEach { fleet ->
          Line(fleet.name, fleet.dates) {
            Action(
              "Sign out",
              { onSignOut(fleet.id) },
              Modifier.semantics { contentDescription = "Sign out of ${fleet.name}" }
            )
          }
        }
        Section("Alerts")
        Line(phone.alerts, phone.alertsDetail) { Action("Settings", onSettings) }
        Section("About")
        Line("This Companion's ID", phone.phoneId ?: "unknown", monoDetail = true)
        Line("Hand's Companion ${phone.app}", phone.core?.let { "core $it" })
      }
    }
  }
  phone.asking?.let { ForgetDialog(it, onForget) { onAsk(null) } }
}

@Composable
fun ThisPhoneRoute(
  phone: ThisPhone,
  signing: String?,
  onSigning: (String?) -> Unit,
  onBack: () -> Unit,
  onAsk: (Forgetting?) -> Unit,
  onForget: () -> Unit,
  onSettings: () -> Unit,
  web: @Composable (String) -> Unit
) {
  if (signing == null) {
    ThisPhoneScreen(phone, onBack, onAsk, onForget, onSigning, onSettings)
  } else {
    val path = signOutPath(signing)
    key(signing) {
      FallbackScreen(signing, phone.fleets.firstOrNull { it.id == signing }?.name, path, { onSigning(null) }) {
        web(path)
      }
    }
  }
}
