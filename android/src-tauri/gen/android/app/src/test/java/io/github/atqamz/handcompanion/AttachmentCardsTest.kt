package io.github.atqamz.handcompanion

import android.view.View
import androidx.compose.ui.platform.LocalView
import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

private const val NAME = "20261121-101500-1-broken.png"

class AttachmentCardsTest {
  private val frames = Frames()

  @get:Rule
  val paparazzi =
    Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 600), snapshotHandler = frames)

  private val shot = Shot(n = 1, name = NAME, label = "broken.png", image = true)

  @Test
  fun tappingAFailedCardAsksAgain() {
    var retried = 0
    lateinit var view: View
    frames.onFrame = { Taps(view).tap("could not load, tap to retry") }
    paparazzi.snapshot {
      HandTheme(false) {
        view = LocalView.current
        AttachedLoaded(shot, Loaded.Failed) { retried++ }
      }
    }
    assertEquals(1, retried)
  }

  @Test
  fun aBusyCardIsNotATapTarget() {
    lateinit var view: View
    var labels = emptyList<String>()
    frames.onFrame = { labels = Taps(view).labels() }
    paparazzi.snapshot {
      HandTheme(false) {
        view = LocalView.current
        AttachedLoaded(shot, Loaded.Busy) {}
      }
    }
    assertTrue("loading" in labels)
    assertTrue("could not load, tap to retry" !in labels)
  }

  @Test
  fun aDrawnPictureIsNamedByItsLabel() {
    val bitmap = android.graphics.Bitmap.createBitmap(40, 30, android.graphics.Bitmap.Config.ARGB_8888)
    lateinit var view: View
    var labels = emptyList<String>()
    frames.onFrame = { labels = Taps(view).labels() }
    paparazzi.snapshot {
      HandTheme(false) {
        view = LocalView.current
        AttachedLoaded(shot, Loaded.Ready(bitmap)) {}
      }
    }
    assertTrue(labels.toString(), "broken.png" in labels)
  }
}
