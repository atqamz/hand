package io.github.atqamz.handcompanion

import android.view.View
import androidx.compose.ui.InternalComposeUiApi
import androidx.compose.ui.platform.ViewRootForTest
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import app.cash.paparazzi.Snapshot
import app.cash.paparazzi.SnapshotHandler
import java.lang.reflect.Proxy

internal class Frames : SnapshotHandler {
  var onFrame: () -> Unit = {}

  override fun newFrameHandler(snapshot: Snapshot, frameCount: Int, fps: Int): SnapshotHandler.FrameHandler =
    Proxy.newProxyInstance(
      SnapshotHandler.FrameHandler::class.java.classLoader,
      arrayOf(SnapshotHandler.FrameHandler::class.java)
    ) { _, method, _ ->
      if (method.name == "handle") onFrame()
      null
    } as SnapshotHandler.FrameHandler

  override fun close() = Unit
}

@OptIn(InternalComposeUiApi::class)
internal class Taps(private val view: View) {
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
