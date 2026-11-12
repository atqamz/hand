package io.github.atqamz.handcompanion

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.compose.runtime.getValue
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import java.io.File
import kotlinx.coroutines.launch

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

  override fun onCreate(savedInstanceState: Bundle?) {
    enableEdgeToEdge()
    super.onCreate(savedInstanceState)
    if (Core.start(applicationContext) != null) {
      finish()
      return
    }
    lifecycleScope.launch { repeatOnLifecycle(Lifecycle.State.STARTED) { model.poll() } }
    setContent {
      HandTheme {
        val state by model.state.collectAsStateWithLifecycle()
        FleetsScreen(state, model::retry, {})
      }
    }
  }
}
