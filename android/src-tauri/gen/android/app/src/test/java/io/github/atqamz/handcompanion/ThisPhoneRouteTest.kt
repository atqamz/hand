package io.github.atqamz.handcompanion

import android.view.View
import androidx.compose.material3.Text
import androidx.compose.ui.InternalComposeUiApi
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.platform.ViewRootForTest
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import app.cash.paparazzi.Snapshot
import app.cash.paparazzi.SnapshotHandler
import java.awt.image.BufferedImage
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

private class Frames : SnapshotHandler {
  var onFrame: () -> Unit = {}

  override fun newFrameHandler(snapshot: Snapshot, frameCount: Int, fps: Int) =
    object : SnapshotHandler.FrameHandler {
      override fun handle(image: BufferedImage) = onFrame()

      override fun close() = Unit
    }

  override fun close() = Unit
}

@OptIn(InternalComposeUiApi::class)
private class Taps(private val view: View) {
  private fun walk(node: SemanticsNode): List<SemanticsNode> =
    listOf(node) + node.children.flatMap(::walk)

  private fun nodes() = walk((view as ViewRootForTest).semanticsOwner.rootSemanticsNode)

  private fun names(node: SemanticsNode) =
    node.config.getOrNull(SemanticsProperties.ContentDescription).orEmpty() +
      node.config.getOrNull(SemanticsProperties.Text).orEmpty().map { it.text }

  fun labels() = nodes().flatMap(::names)

  fun tap(label: String) {
    val node = nodes().first { label in names(it) && SemanticsActions.OnClick in it.config }
    checkNotNull(node.config[SemanticsActions.OnClick].action).invoke()
  }
}

private class Calls {
  val asks = mutableListOf<Boolean>()
  var forgets = 0
  val signings = mutableListOf<String?>()
  var settings = 0
  val pages = mutableSetOf<String>()
  var labels = emptyList<String>()
}

class ThisPhoneRouteTest {
  private val frames = Frames()

  @get:Rule
  val paparazzi =
    Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 2200), snapshotHandler = frames)

  private fun shown(phone: ThisPhone, signing: String?, act: Taps.() -> Unit): Calls {
    val calls = Calls()
    lateinit var view: View
    frames.onFrame = {
      val taps = Taps(view)
      calls.labels = taps.labels()
      taps.act()
    }
    paparazzi.snapshot {
      HandTheme(false) {
        view = LocalView.current
        ThisPhoneRoute(
          phone,
          signing,
          { calls.signings += it },
          {},
          { calls.asks += it },
          { calls.forgets++ },
          { calls.settings++ }
        ) { path ->
          calls.pages += path
          Text(path)
        }
      }
    }
    return calls
  }

  @Test
  fun tappingForgetRaisesTheQuestionAndDoesNotForget() {
    val calls = shown(phone(several), null) { tap("Forget") }
    assertEquals(listOf(true), calls.asks)
    assertEquals(0, calls.forgets)
    assertTrue(calls.signings.isEmpty())
  }

  @Test
  fun eachSignOutNamesItsOwnFleet() {
    val calls = shown(phone(several), null) {
      tap("Sign out of atlas")
      tap("Sign out of harbor")
    }
    assertEquals(listOf("f000000000003", "f000000000017"), calls.signings)
    assertTrue(calls.asks.isEmpty())
    assertEquals(0, calls.forgets)
  }

  @Test
  fun settingsOpensTheNotificationSettingsAndNothingElse() {
    val calls = shown(phone(several), null) { tap("Settings") }
    assertEquals(1, calls.settings)
    assertTrue(calls.asks.isEmpty())
    assertTrue(calls.signings.isEmpty())
  }

  @Test
  fun aFleetBeingSignedOutShowsItsPhonePageInTheFallbackAndCloseGoesBack() {
    val calls = shown(phone(several), "f000000000005") { tap("Close") }
    assertEquals(setOf("/f000000000005/connect"), calls.pages)
    assertTrue("orchard · connect" in calls.labels)
    assertTrue("Forget" !in calls.labels)
    assertEquals(listOf<String?>(null), calls.signings)
  }

  @Test
  fun noFleetBeingSignedOutShowsThePhoneAndNoWebPage() {
    val calls = shown(phone(several), null) {}
    assertTrue(calls.pages.isEmpty())
    assertTrue("This phone" in calls.labels)
  }
}
