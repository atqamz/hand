package io.github.atqamz.handcompanion

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import java.io.File

class HandActivity : ComponentActivity() {
  private val model by viewModels<FleetsModel> {
    viewModelFactory {
      initializer {
        val laptop = File(applicationContext.dataDir, "laptop")
        FleetsModel(
          CoreTransport,
          { pairedFleets(Core.fleets()) },
          { laptopId(laptop) }
        )
      }
    }
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

  override fun onCreate(savedInstanceState: Bundle?) {
    enableEdgeToEdge()
    super.onCreate(savedInstanceState)
    if (Core.start(applicationContext) != null) {
      finish()
      return
    }
    setContent {
      HandTheme {
        var open by rememberSaveable { mutableStateOf<String?>(null) }
        val fleet = open?.let(::fleetModel)
        LaunchedEffect(open) {
          lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            if (fleet != null) fleet.poll() else model.poll()
          }
        }
        if (fleet == null) {
          val state by model.state.collectAsStateWithLifecycle()
          FleetsScreen(state, model::retry, {}, {
            fleetModel(it).enter()
            open = it
          })
        } else {
          val state by fleet.state.collectAsStateWithLifecycle()
          BackHandler { open = null }
          FleetScreen(
            open.orEmpty(),
            state,
            { open = null },
            fleet::retry,
            fleet::select,
            fleet::toggle,
            fleet::markRead,
            { startActivity(Intent(this, MainActivity::class.java)) }
          )
        }
      }
    }
  }
}
