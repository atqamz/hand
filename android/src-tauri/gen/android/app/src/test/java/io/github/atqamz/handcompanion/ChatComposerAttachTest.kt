package io.github.atqamz.handcompanion

import android.view.View
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

internal val twoFiles =
  listOf(
    Attached("20261121-101500-1-before.png", "before.png", "image", 48_000),
    Attached("20261121-101500-2-run.log", "run.log", "file", 9_000)
  )

internal const val TEXT = "Look at [attachment 1], it should be like [attachment 2]"

internal fun composerOf(
  attached: List<Attached> = twoFiles,
  uploading: List<String> = emptyList(),
  attachError: String = "",
  sending: Boolean = false,
  typed: String = TEXT,
  onAttach: () -> Unit = {},
  onDetach: (Int) -> Unit = {}
): @androidx.compose.runtime.Composable () -> Unit = {
  ChatComposer(
    hint = "",
    blocked = false,
    sending = sending,
    error = "",
    sendNote = "",
    note = "",
    draft = typed,
    selection = TextRange(typed.length),
    attached = attached,
    uploading = uploading,
    attachError = attachError,
    input = remember { mutableStateOf(TextFieldValue(typed)) },
    onEdit = {},
    onSend = {},
    onHint = {},
    onAttach = onAttach,
    onDetach = onDetach
  )
}


class ChatComposerAttachTest {
  private val frames = Frames()

  @get:Rule
  val paparazzi =
    Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 900), snapshotHandler = frames)

  private fun tapped(label: String, content: (View) -> @androidx.compose.runtime.Composable () -> Unit) {
    lateinit var view: View
    frames.onFrame = { Taps(view).tap(label) }
    paparazzi.snapshot {
      HandTheme(false) {
        view = LocalView.current
        content(view)()
      }
    }
  }

  @Test
  fun theAttachButtonAsksForFiles() {
    var asked = 0
    tapped("Attach") { composerOf(onAttach = { asked++ }) }
    assertEquals(1, asked)
  }

  @Test
  fun aChipsCrossRemovesItsNumber() {
    val removed = mutableListOf<Int>()
    tapped("Remove run.log") { composerOf(onDetach = { removed += it }) }
    assertEquals(listOf(2), removed)
  }

  @Test
  fun chipsAreNumberedLikeTheirTokensAndAnUploadHasNoNumber() {
    lateinit var view: View
    var labels = emptyList<String>()
    frames.onFrame = { labels = Taps(view).labels() }
    paparazzi.snapshot {
      HandTheme(false) {
        view = LocalView.current
        composerOf(uploading = listOf("c.png"))()
      }
    }
    assertTrue(labels.toString(), "1" in labels && "2" in labels && "3" !in labels)
    assertTrue(labels.toString(), "before.png" in labels && "c.png" in labels)
  }

  private fun sendDisabled(content: @androidx.compose.runtime.Composable () -> Unit): Boolean {
    lateinit var view: View
    var disabled: Boolean? = null
    frames.onFrame = { disabled = Taps(view).disabled("Send") }
    paparazzi.snapshot {
      HandTheme(false) {
        view = LocalView.current
        content()
      }
    }
    return disabled!!
  }

  @Test
  fun sendIsOfferedForFilesAloneButNotWhileOneUploadsOrIsSending() {
    assertEquals(false, sendDisabled(composerOf()))
    assertEquals(true, sendDisabled(composerOf(uploading = listOf("c.png"))))
    assertEquals(true, sendDisabled(composerOf(sending = true)))
    assertEquals(false, sendDisabled(composerOf(typed = "")))
    assertEquals(true, sendDisabled(composerOf(attached = emptyList(), typed = "")))
  }

  @Test
  fun attachingIsOffByTheTenthChipAndWhileSending() {
    val ten = (1..10).map { Attached("n$it.png", "$it.png", "image", 1) }
    lateinit var view: View
    val states = mutableMapOf<String, Boolean>()
    frames.onFrame = { states["ten"] = Taps(view).disabled("Attach") }
    paparazzi.snapshot { HandTheme(false) { view = LocalView.current; composerOf(attached = ten)() } }
    frames.onFrame = { states["nine"] = Taps(view).disabled("Attach") }
    paparazzi.snapshot { HandTheme(false) { view = LocalView.current; composerOf(attached = ten.take(9))() } }
    frames.onFrame = { states["nineUp"] = Taps(view).disabled("Attach") }
    paparazzi.snapshot {
      HandTheme(false) { view = LocalView.current; composerOf(attached = ten.take(9), uploading = listOf("x"))() }
    }
    frames.onFrame = { states["sending"] = Taps(view).disabled("Attach") }
    paparazzi.snapshot { HandTheme(false) { view = LocalView.current; composerOf(sending = true)() } }
    assertEquals(mapOf("ten" to true, "nine" to false, "nineUp" to true, "sending" to true), states)
  }
}


class ChatComposerScenesTest {
  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 900))

  private fun scene(dark: Boolean) =
    paparazzi.snapshot {
      HandTheme(dark) {
        Column(Modifier.fillMaxWidth().background(LocalHand.current.bg)) {
          composerOf()()
          composerOf(
            attached = twoFiles.take(1),
            uploading = listOf("a-very-long-file-name-for-the-second-picture.png"),
            attachError = "run.log is over 10 MiB."
          )()
        }
      }
    }

  @Test fun chipsLight() = scene(false)

  @Test fun chipsDark() = scene(true)

}
