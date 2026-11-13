package io.github.atqamz.handcompanion

import android.Manifest
import android.app.Activity
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.provider.Settings
import android.view.WindowManager
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.LifecycleResumeEffect
import com.google.zxing.BarcodeFormat
import com.journeyapps.barcodescanner.BarcodeView
import com.journeyapps.barcodescanner.CameraPreview
import com.journeyapps.barcodescanner.DefaultDecoderFactory
import kotlinx.coroutines.delay

const val CAMERA_REFUSED = "Allow the camera to scan the code, or paste the link."
const val START_MILLIS = 15_000L

enum class Scan(val message: String) {
  Starting("Starting the camera…"),
  Preview("Point the camera at the code on your laptop."),
  Refused(CAMERA_REFUSED),
  Failed("The camera could not start. Try again, or paste the link."),
  TimedOut("The camera took too long to start. Try again, or paste the link.")
}

@Composable
internal fun Secure() {
  val window = (LocalContext.current as? Activity)?.window
  DisposableEffect(window) {
    window?.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
    onDispose { window?.clearFlags(WindowManager.LayoutParams.FLAG_SECURE) }
  }
}

@Composable
private fun Bar(
  title: String,
  icon: ImageVector,
  label: String,
  onClick: () -> Unit,
  color: Color = LocalHand.current.fg,
  enabled: Boolean = true
) {
  Row(
    Modifier.fillMaxWidth().height(56.dp).padding(end = 16.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(8.dp)
  ) {
    IconButton(onClick, enabled = enabled) { Icon(icon, label, Modifier.size(20.dp), color) }
    Text(title, style = type.headlineSmall, color = color)
  }
}

@Composable
private fun Field(
  value: String,
  onChange: (String) -> Unit,
  modifier: Modifier = Modifier,
  hint: String? = null,
  error: String? = null,
  keyboard: KeyboardOptions = KeyboardOptions.Default,
  onDone: () -> Unit = {}
) {
  OutlinedTextField(
    value,
    onChange,
    modifier.fillMaxWidth(),
    placeholder = hint?.let { { Text(it, color = LocalHand.current.muted) } },
    isError = error != null,
    supportingText = error?.let { { Text(it, color = LocalHand.current.ink(Tone.Fail)) } },
    singleLine = true,
    shape = MaterialTheme.shapes.large,
    keyboardOptions = keyboard.copy(imeAction = ImeAction.Done),
    keyboardActions = KeyboardActions(onDone = { onDone() })
  )
}

@Composable
fun AddFleetScreen(
  state: PairState,
  onEdit: (String) -> Unit,
  onScan: () -> Unit,
  onContinue: () -> Unit,
  onBack: () -> Unit
) {
  val hand = LocalHand.current
  Column(Modifier.fillMaxSize().safeDrawingPadding().verticalScroll(rememberScrollState())) {
    Bar("Add a fleet", HandIcons.Back, "Back", onBack)
    HorizontalDivider(color = hand.line)
    Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
      Text(
        buildAnnotatedString {
          append("On your laptop, open the fleet's Phone page and choose ")
          withStyle(SpanStyle(fontWeight = FontWeight.SemiBold)) { append("Pair the Hand app") }
          append(". Scan the code it shows.")
        },
        style = type.bodyLarge
      )
      Button(
        onScan,
        Modifier.fillMaxWidth().height(48.dp),
        shape = MaterialTheme.shapes.medium
      ) {
        Icon(HandIcons.Scan, null, Modifier.size(18.dp))
        Text("Scan the QR code", Modifier.padding(start = 8.dp), style = type.titleMedium)
      }
      Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        HorizontalDivider(Modifier.weight(1f), color = hand.line)
        Meta("or paste the link")
        HorizontalDivider(Modifier.weight(1f), color = hand.line)
      }
      Text("Pairing link", style = type.labelLarge)
      Field(
        state.link,
        onEdit,
        hint = "handboard://c#…",
        error = state.error,
        keyboard = KeyboardOptions(keyboardType = KeyboardType.Uri, autoCorrectEnabled = false),
        onDone = onContinue
      )
      Action("Continue", onContinue, Modifier.fillMaxWidth().height(48.dp))
      Meta("A code works once, for 5 minutes. Screenshots are off on this screen.")
    }
  }
}

@Composable
fun ConfirmScreen(
  state: PairState,
  onName: (String) -> Unit,
  onPair: () -> Unit,
  onCancel: () -> Unit
) {
  val hand = LocalHand.current
  val confirm = state.confirm ?: return
  Column(Modifier.fillMaxSize().safeDrawingPadding().verticalScroll(rememberScrollState())) {
    Bar("Pair with this laptop?", HandIcons.Close, "Cancel", onCancel, enabled = !state.busy)
    HorizontalDivider(color = hand.line)
    Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
      Column {
        Meta("Laptop")
        Text(confirm.laptop, style = HandMono.copy(fontSize = 28.sp, lineHeight = 36.sp))
        Meta("Check these characters against the Phone page on that laptop.")
      }
      confirm.notice?.let {
        Column(
          Modifier.fillMaxWidth()
            .background(hand.fill(Tone.Wait), MaterialTheme.shapes.large)
            .border(1.dp, hand.ink(Tone.Wait).copy(alpha = 0.25f), MaterialTheme.shapes.large)
            .padding(16.dp),
          verticalArrangement = Arrangement.spacedBy(4.dp)
        ) {
          Text("This replaces your current laptop", style = type.titleMedium)
          Text(it, style = type.bodyMedium)
        }
      }
      Text("Name for this phone", style = type.labelLarge)
      Field(state.name, onName, onDone = onPair)
      state.error?.let { Text(it, color = hand.ink(Tone.Fail), style = type.bodyMedium) }
      Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
        Action("Cancel", onCancel, Modifier.weight(1f).height(48.dp), !state.busy)
        Button(
          onPair,
          Modifier.weight(1f).height(48.dp),
          enabled = !state.busy,
          shape = MaterialTheme.shapes.medium,
          colors = ButtonDefaults.buttonColors()
        ) {
          Text(if (state.busy) "Connecting…" else confirm.action, style = type.titleMedium)
        }
      }
    }
  }
}

@Composable
private fun Frame() {
  Canvas(Modifier.size(280.dp)) {
    val stroke = Stroke(3.dp.toPx(), cap = StrokeCap.Round)
    val arm = 56.dp.toPx()
    val far = size.width
    for ((x, y, dx, dy) in listOf(
      listOf(0f, 0f, 1f, 1f),
      listOf(far, 0f, -1f, 1f),
      listOf(0f, far, 1f, -1f),
      listOf(far, far, -1f, -1f)
    )) {
      drawLine(Color.White, Offset(x, y), Offset(x + dx * arm, y), stroke.width, stroke.cap)
      drawLine(Color.White, Offset(x, y), Offset(x, y + dy * arm), stroke.width, stroke.cap)
    }
  }
}

@Composable
fun ScanScreen(
  scan: Scan,
  onClose: () -> Unit,
  onPaste: () -> Unit,
  onAllow: () -> Unit,
  onSettings: () -> Unit,
  onRetry: () -> Unit,
  preview: @Composable () -> Unit
) {
  Box(Modifier.fillMaxSize().background(Color.Black)) {
    if (scan == Scan.Starting || scan == Scan.Preview) preview()
    Column(Modifier.fillMaxSize().safeDrawingPadding()) {
      Bar("Scan the QR code", HandIcons.Close, "Close", onClose, Color.White)
      Column(
        Modifier.weight(1f).fillMaxWidth(),
        verticalArrangement = Arrangement.spacedBy(24.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally
      ) {
        Frame()
        Text(
          scan.message,
          Modifier.padding(horizontal = 32.dp),
          color = Color.White,
          textAlign = TextAlign.Center,
          style = type.bodyLarge
        )
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
          when (scan) {
            Scan.Refused -> {
              ScanAction("Allow camera", onAllow)
              ScanAction("Open settings", onSettings)
            }
            Scan.Failed,
            Scan.TimedOut -> ScanAction("Try again", onRetry)
            else -> Unit
          }
        }
      }
      OutlinedButton(
        onPaste,
        Modifier.fillMaxWidth().padding(16.dp).height(48.dp),
        shape = MaterialTheme.shapes.medium,
        border = BorderStroke(1.dp, Color.White)
      ) {
        Text("Paste the link instead", color = Color.White, style = type.bodyMedium)
      }
    }
  }
}

@Composable
private fun ScanAction(label: String, onClick: () -> Unit) =
  OutlinedButton(
    onClick,
    shape = MaterialTheme.shapes.medium,
    border = BorderStroke(1.dp, Color.White)
  ) {
    Text(label, color = Color.White, style = type.bodyMedium)
  }

private fun granted(context: Context) =
  context.checkSelfPermission(Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED

@Composable
fun Scanner(onCode: (String) -> Unit, onClose: () -> Unit, onPaste: () -> Unit) {
  val context = LocalContext.current
  var allowed by remember { mutableStateOf(granted(context)) }
  var scan by remember { mutableStateOf(Scan.Starting) }
  var attempt by remember { mutableIntStateOf(0) }
  val ask =
    rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {
      allowed = it
      scan = if (it) Scan.Starting else Scan.Refused
    }
  LaunchedEffect(Unit) { if (!allowed) ask.launch(Manifest.permission.CAMERA) }
  LifecycleEventEffect(Lifecycle.Event.ON_RESUME) {
    if (granted(context)) allowed = true
  }
  LaunchedEffect(attempt, allowed) {
    if (allowed) {
      scan = Scan.Starting
      delay(START_MILLIS)
      if (scan == Scan.Starting) scan = Scan.TimedOut
    }
  }
  ScanScreen(
    scan,
    onClose,
    onPaste,
    onAllow = { ask.launch(Manifest.permission.CAMERA) },
    onSettings = {
      context.startActivity(
        Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:${context.packageName}"))
      )
    },
    onRetry = { attempt++ }
  ) {
    if (allowed) {
      key(attempt) {
        CameraView(onStarted = { scan = Scan.Preview }, onFailed = { scan = Scan.Failed }, onCode)
      }
    }
  }
}

@Composable
private fun CameraView(onStarted: () -> Unit, onFailed: () -> Unit, onCode: (String) -> Unit) {
  val context = LocalContext.current
  val started by rememberUpdatedState(onStarted)
  val failed by rememberUpdatedState(onFailed)
  val code by rememberUpdatedState(onCode)
  val view =
    remember {
      var delivered = false
      BarcodeView(context).apply {
        decoderFactory = DefaultDecoderFactory(listOf(BarcodeFormat.QR_CODE))
        addStateListener(
          object : CameraPreview.StateListener {
            override fun previewSized() {}

            override fun previewStarted() = started()

            override fun previewStopped() {}

            override fun cameraError(error: Exception) = failed()

            override fun cameraClosed() {}
          }
        )
        decodeContinuous { result ->
          val text = result.text
          if (!delivered && text != null) {
            delivered = true
            stopDecoding()
            code(text)
          }
        }
      }
    }
  AndroidView({ view }, Modifier.fillMaxSize())
  LifecycleResumeEffect(view) {
    view.resume()
    onPauseOrDispose { view.pause() }
  }
}
