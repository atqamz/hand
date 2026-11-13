package io.github.atqamz.handcompanion

import android.Manifest
import android.content.ActivityNotFoundException
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.viewModels
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.key
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import java.io.File
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private const val ASKED = "notification-permission-asked"
private const val REFUSED_MILLIS = 4_000L

@Composable
internal fun StartFailure(message: String) {
  Column(Modifier.fillMaxSize().safeDrawingPadding().padding(24.dp)) {
    Text("Hand can't start", style = type.headlineSmall)
    Text(message, Modifier.padding(top = 12.dp), style = type.bodyMedium)
  }
}

class HandActivity : ComponentActivity() {
  private var incoming by mutableStateOf<String?>(null)
  private var asking = false

  private val model by viewModels<FleetsModel> {
    viewModelFactory {
      initializer {
        val laptop = File(applicationContext.dataDir, "laptop")
        FleetsModel(
          CoreTransport,
          { pairedFleets(Core.fleets()) },
          { laptopId(laptop) },
          Core::unsaved,
          forgetter = Core::forget
        )
      }
    }
  }

  private val pair by viewModels<PairModel> {
    viewModelFactory {
      initializer {
        PairModel(CorePairing, Build.MODEL, {
          val fleets = model.state.value
          Current(fleets.laptop, fleets.rows.map(FleetRow::name))
        })
      }
    }
  }

  private val notificationPermission =
    registerForActivityResult(ActivityResultContracts.RequestPermission()) {
      asking = false
      getPreferences(MODE_PRIVATE).edit().putBoolean(ASKED, true).apply()
      checkAlerts()
    }

  private fun take(intent: Intent) {
    incoming = intent.data?.toString() ?: return
    setIntent(Intent())
  }

  private fun checkAlerts() {
    if (!lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)) return
    lifecycleScope.launch {
      val paired = withContext(Dispatchers.IO) { Core.paired() }
      if (!lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)) return@launch
      val allowed =
        Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU ||
          ContextCompat.checkSelfPermission(this@HandActivity, Manifest.permission.POST_NOTIFICATIONS) ==
            PackageManager.PERMISSION_GRANTED
      val step = alertStep(paired, allowed, getPreferences(MODE_PRIVATE).getBoolean(ASKED, false))
      model.alerts(step == AlertStep.Off)
      when (step) {
        AlertStep.Start -> AlertService.start(this@HandActivity)
        AlertStep.Ask ->
          if (!asking) {
            asking = true
            notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
          }
        else -> Unit
      }
    }
  }

  override fun onResume() {
    super.onResume()
    lifecycleScope.launch { withContext(Dispatchers.IO) { Core.resume() } }
    checkAlerts()
  }

  override fun onNewIntent(intent: Intent) {
    super.onNewIntent(intent)
    take(intent)
  }

  private fun fleetModel(id: String): FleetModel =
    ViewModelProvider(
      this,
      viewModelFactory {
        initializer {
          val laptop = File(applicationContext.dataDir, "laptop")
          FleetModel(id, model.state.value.rows.firstOrNull { it.id == id }?.name, CoreTransport, { laptopId(laptop) })
        }
      }
    )[id, FleetModel::class.java]

  private fun choiceModel(fleet: String, ref: String): DecisionModel =
    ViewModelProvider(
      this,
      viewModelFactory {
        initializer {
          DecisionModel(
            fleet,
            ref,
            CoreTransport,
            PrefsDrafts(applicationContext.getSharedPreferences("drafts", MODE_PRIVATE))
          )
        }
      }
    )["$fleet/$ref", DecisionModel::class.java]

  @Composable
  private fun Choice(fleet: String, ref: String, onOpen: (() -> Unit)?) {
    val model = remember(fleet, ref) { choiceModel(fleet, ref) }
    val state by model.state.collectAsStateWithLifecycle()
    LaunchedEffect(model) { model.load() }
    ChoiceForm(ref, state, model::pick, model::edit, model::send, model::reload, onOpen)
  }

  @Composable
  private fun Decision(fleet: String, ref: String, onBack: () -> Unit) {
    val model = remember(fleet, ref) { choiceModel(fleet, ref) }
    val state by model.state.collectAsStateWithLifecycle()
    LaunchedEffect(model) { model.load() }
    DecisionScreen(ref, state, onBack, model::pick, model::edit, model::send, model::reload)
  }

  private fun taskModel(fleet: String, ref: String): TaskModel =
    ViewModelProvider(
      this,
      viewModelFactory {
        initializer { TaskModel(fleet, ref, CoreTransport) }
      }
    )["$fleet/$ref", TaskModel::class.java]

  @Composable
  private fun Task(
    fleet: String,
    ref: String,
    onBack: () -> Unit,
    onTarget: (ThreadTarget) -> Unit,
    onWeb: () -> Unit
  ) {
    val model = remember(fleet, ref) { taskModel(fleet, ref) }
    val state by model.state.collectAsStateWithLifecycle()
    LaunchedEffect(model) {
      lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) { model.run() }
    }
    TaskScreen(fleet, ref, state, onBack, model::retry, model::markRead, onTarget, onWeb)
  }

  override fun onCreate(savedInstanceState: Bundle?) {
    enableEdgeToEdge()
    super.onCreate(savedInstanceState)
    val failure = Core.start(applicationContext)
    if (failure != null) {
      setContent { HandTheme { StartFailure(failure) } }
      return
    }
    if (savedInstanceState == null) take(intent)
    setContent {
      HandTheme {
        var open by rememberSaveable { mutableStateOf<String?>(null) }
        var decision by rememberSaveable { mutableStateOf<String?>(null) }
        var web by rememberSaveable { mutableStateOf<String?>(null) }
        var taskPath by rememberSaveable { mutableStateOf("") }
        val holder = rememberSaveableStateHolder()
        var adding by rememberSaveable { mutableStateOf(false) }
        var scanning by remember { mutableStateOf(false) }
        val pairing by pair.state.collectAsStateWithLifecycle()
        val listed by model.state.collectAsStateWithLifecycle()
        LaunchedEffect(listed.notice) { if (listed.notice == FORGET_FAILED) checkAlerts() }
        var refused by remember { mutableStateOf<String?>(null) }
        LaunchedEffect(refused) {
          if (refused != null) {
            delay(REFUSED_MILLIS)
            refused = null
          }
        }
        val fleet = open?.let(::fleetModel)
        val live = fleet != null && web == null
        LaunchedEffect(open, live) {
          lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            if (live) fleet.run() else if (fleet == null) model.poll()
          }
        }
        LaunchedEffect(incoming, pairing.busy, pairing.done) {
          val text = incoming
          if (text == null || pairing.busy || pairing.done) return@LaunchedEffect
          incoming = null
          val to = withContext(Dispatchers.IO) { landing(text, Core::open) }
          if (to !is Landing.Pairing && pair.state.value.confirm != null) {
            refused = PAIRING_PENDING
            return@LaunchedEffect
          }
          val show = { id: String ->
            fleetModel(id).enter()
            adding = false
            scanning = false
            decision = null
            web = null
            taskPath = ""
            open = id
          }
          model.say(null)
          when (to) {
            is Landing.Pairing -> {
              open = null
              decision = null
              web = null
              taskPath = ""
              scanning = false
              adding = true
              pair.offer(to.link)
            }
            is Landing.Decision -> {
              show(to.fleet)
              decision = to.ref
            }
            is Landing.Fleet -> show(to.fleet)
            is Landing.Web -> {
              show(to.fleet)
              web = to.path
            }
            is Landing.Refused -> refused = to.message
          }
        }
        LaunchedEffect(pairing.done) {
          if (pairing.done) {
            adding = false
            pair.finish()
            model.retry()
            checkAlerts()
          }
        }
        Box(Modifier.fillMaxSize()) {
          if (adding) {
            Secure()
            val leave = {
              pair.cancel()
              adding = false
            }
            BackHandler {
              when {
                pairing.busy -> Unit
                scanning -> scanning = false
                pairing.confirm != null -> pair.cancel()
                else -> leave()
              }
            }
            when {
              scanning ->
                Scanner(
                  { scanning = false; pair.offer(it) },
                  { scanning = false },
                  { scanning = false }
                )
              pairing.confirm != null -> ConfirmScreen(pairing, pair::rename, pair::pair, pair::cancel)
              else ->
                AddFleetScreen(pairing, pair::edit, { scanning = true }, { pair.offer(pairing.link) }, leave)
            }
          } else if (fleet == null) {
            val state by model.state.collectAsStateWithLifecycle()
            FleetsScreen(
              state,
              model::retry,
              {
                model.say(null)
                adding = true
              },
              {
                model.say(null)
                fleetModel(it).enter()
                taskPath = ""
                open = it
              },
              model::ask,
              model::forget,
              {
                startActivity(
                  Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS)
                    .putExtra(Settings.EXTRA_APP_PACKAGE, packageName)
                )
              }
            )
          } else {
            val state by fleet.state.collectAsStateWithLifecycle()
            LaunchedEffect(state.deadline) {
              if (state.deadline == null) return@LaunchedEffect
              while (true) {
                delay(1_000)
                fleet.tick()
              }
            }
            val page = web
            val shown = decision
            val thread = currentTask(taskPath)
            if (page != null) {
              key(page) {
                FallbackScreen(open.orEmpty(), state.name, page, { web = null }) {
                  WebBoard(page) { web = null }
                }
              }
            } else if (shown != null) {
              BackHandler { decision = null }
              Decision(open.orEmpty(), shown) { decision = null }
            } else if (thread != null) {
              val back = { taskPath = popTask(taskPath) }
              BackHandler(onBack = back)
              val target: (ThreadTarget) -> Unit = { to ->
                when (to) {
                  is ThreadTarget.Task -> taskPath = pushTask(taskPath, to.ref)
                  is ThreadTarget.Decision -> decision = to.ref
                  is ThreadTarget.Web -> web = to.path
                  is ThreadTarget.Browser ->
                    try {
                      startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(to.url)))
                    } catch (_: ActivityNotFoundException) {}
                  ThreadTarget.None -> Unit
                }
              }
              holder.SaveableStateProvider("task:$open:$thread") {
                Task(open.orEmpty(), thread, back, target) { web = "/${open}/task/$thread" }
              }
            } else {
              BackHandler { open = null }
              holder.SaveableStateProvider("fleet:$open") {
                FleetScreen(
                  open.orEmpty(),
                  state,
                  { open = null },
                  fleet::retry,
                  { tab ->
                    fleet.select(tab)
                    if (tab == Tab.Tasks) fleet.openTasks()
                  },
                  fleet::toggle,
                  fleet::markRead,
                  { web = "/${open}/" },
                  { taskPath = pushTask("", it) },
                  fleet::showAll,
                  fleet::press,
                  fleet::force
                ) { ref ->
                  Choice(open.orEmpty(), ref) { decision = ref }
                }
              }
            }
          }
          val tabs = fleet != null && web == null && decision == null && currentTask(taskPath) == null
          refused?.let {
            Snack(it, Modifier.align(Alignment.BottomCenter).padding(bottom = if (tabs) TABS_HEIGHT else 0.dp))
          }
        }
      }
    }
  }
}
