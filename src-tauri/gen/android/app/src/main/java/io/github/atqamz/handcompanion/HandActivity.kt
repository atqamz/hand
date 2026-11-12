package io.github.atqamz.handcompanion

import android.content.Intent
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import java.io.File

private val decisionLink = Regex("handboard://open/(f[0-9a-f]{12})/decision/(d[1-9][0-9]{0,8})")

fun decisionRoute(link: String?): Pair<String, String>? =
  link?.let(decisionLink::matchEntire)?.destructured?.let { (fleet, ref) -> fleet to ref }

class HandActivity : ComponentActivity() {
  private var pending by mutableStateOf<Pair<String, String>?>(null)

  private val model by viewModels<FleetsModel> {
    viewModelFactory {
      initializer {
        val laptop = File(applicationContext.dataDir, "laptop")
        FleetsModel(
          CoreTransport,
          { pairedFleets(Core.fleets()) },
          { laptopId(laptop) },
          Core::unsaved
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

  private var link by mutableStateOf<String?>(null)

  private fun take(intent: Intent) {
    val data = intent.data ?: return
    if (data.scheme == "handboard" && data.host == "c") {
      link = data.toString()
    } else {
      pending = decisionRoute(data.toString())
    }
    setIntent(Intent())
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

  override fun onCreate(savedInstanceState: Bundle?) {
    enableEdgeToEdge()
    super.onCreate(savedInstanceState)
    if (Core.start(applicationContext) != null) {
      finish()
      return
    }
    if (savedInstanceState == null) take(intent)
    setContent {
      HandTheme {
        var open by rememberSaveable { mutableStateOf<String?>(null) }
        var decision by rememberSaveable { mutableStateOf<String?>(null) }
        var adding by rememberSaveable { mutableStateOf(false) }
        var scanning by remember { mutableStateOf(false) }
        val pairing by pair.state.collectAsStateWithLifecycle()
        LaunchedEffect(pending, pairing.busy) {
          pending?.takeUnless { pairing.busy }?.let { (fleet, ref) ->
            pair.cancel()
            adding = false
            scanning = false
            open = fleet
            decision = ref
            pending = null
          }
        }
        val fleet = open?.let(::fleetModel)
        LaunchedEffect(open) {
          lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            if (fleet != null) fleet.poll() else model.poll()
          }
        }
        LaunchedEffect(link, pairing.busy, pairing.done) {
          link?.takeUnless { pairing.busy || pairing.done }?.let {
            link = null
            open = null
            decision = null
            scanning = false
            adding = true
            pair.offer(it)
          }
        }
        LaunchedEffect(pairing.done) {
          if (pairing.done) {
            adding = false
            pair.finish()
            model.retry()
          }
        }
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
          FleetsScreen(state, model::retry, { adding = true }, {
            fleetModel(it).enter()
            open = it
          })
        } else {
          val state by fleet.state.collectAsStateWithLifecycle()
          BackHandler { if (decision != null) decision = null else open = null }
          val shown = decision
          if (shown != null) {
            Decision(open.orEmpty(), shown) { decision = null }
          } else {
            FleetScreen(
              open.orEmpty(),
              state,
              { open = null },
              fleet::retry,
              fleet::select,
              fleet::toggle,
              fleet::markRead,
              { startActivity(Intent(this, MainActivity::class.java)) }
            ) { ref ->
              Choice(open.orEmpty(), ref) { decision = ref }
            }
          }
        }
      }
    }
  }
}
