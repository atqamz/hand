package io.github.atqamz.handcompanion

import android.view.View
import androidx.compose.foundation.layout.Column
import androidx.compose.ui.platform.LocalView
import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

private class Presses {
  var signOut = 0
  var forget = 0
  var cancel = 0
  var labels = emptyList<String>()

  fun counts() = Triple(signOut, forget, cancel)
}

class SignOutDialogTest {
  private val frames = Frames()

  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5, snapshotHandler = frames)

  private fun pressed(failure: SignOutFailure?, signing: Boolean = false, act: Taps.(Presses) -> Unit): Presses {
    val presses = Presses()
    lateinit var view: View
    frames.onFrame = {
      val taps = Taps(view)
      presses.labels = taps.labels()
      taps.act(presses)
    }
    paparazzi.snapshot {
      HandTheme(false) {
        view = LocalView.current
        val choices = signOutChoices(failure, signing)
        Column {
          SignOutConfirm(choices, onSignOut = { presses.signOut++ }, onForget = { presses.forget++ })
          SignOutDismiss(choices, signing, onForget = { presses.forget++ }, onCancel = { presses.cancel++ })
        }
      }
    }
    return presses
  }

  @Test
  fun inTheQuestionSignOutSignsOutAndForgetOnlyForgetsAndCancelCancels() {
    val presses = pressed(null) { counts ->
      tap("Sign out")
      assertEquals(Triple(1, 0, 0), counts.counts())
      tap("Forget only")
      assertEquals(Triple(1, 1, 0), counts.counts())
      tap("Cancel")
    }
    assertEquals(Triple(1, 1, 1), presses.counts())
    assertEquals(listOf("Sign out", "Forget only", "Cancel"), presses.labels.filter { it in setOf("Sign out", "Forget only", "Cancel") })
  }

  @Test
  fun afterAFailureThatCanBeRetriedTryAgainSignsOutAndForgetOnlyForgetsAndCancelCancels() {
    val silent = SignOutFailure("did not answer", true)
    val presses = pressed(silent) { counts ->
      tap("Try again")
      assertEquals(Triple(1, 0, 0), counts.counts())
      tap("Forget only")
      assertEquals(Triple(1, 1, 0), counts.counts())
      tap("Cancel")
    }
    assertEquals(Triple(1, 1, 1), presses.counts())
    assertTrue("Sign out" !in presses.labels)
  }

  @Test
  fun afterAFailureThatCannotBeRetriedOnlyForgetOnlyAndCancelRemainAndNeitherSignsOut() {
    val old = SignOutFailure(SIGN_OUT_TOO_OLD, false)
    val presses = pressed(old) { counts ->
      tap("Forget only")
      assertEquals(Triple(0, 1, 0), counts.counts())
      tap("Cancel")
    }
    assertEquals(Triple(0, 1, 1), presses.counts())
    assertEquals(1, presses.labels.count { it == "Forget only" })
  }

  @Test
  fun whileSigningOutNoButtonRuns() {
    val presses = pressed(null, signing = true) { counts ->
      assertTrue(disabled("Sign out"))
      assertTrue(disabled("Forget only"))
      assertTrue(disabled("Cancel"))
    }
    assertEquals(Triple(0, 0, 0), presses.counts())
  }
}
