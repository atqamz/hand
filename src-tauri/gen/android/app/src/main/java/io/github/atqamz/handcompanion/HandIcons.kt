package io.github.atqamz.handcompanion

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.addPathNodes
import androidx.compose.ui.unit.dp
import java.util.Locale
import kotlin.math.cos
import kotlin.math.sin

private class Shape(val data: String, val fill: Boolean = false, val stroke: Boolean = true)

private fun icon(name: String, vararg shapes: Shape): ImageVector {
  val builder = ImageVector.Builder(name, 16.dp, 16.dp, 16f, 16f)
  for (shape in shapes) {
    builder.addPath(
      addPathNodes(shape.data),
      fill = if (shape.fill) SolidColor(Color.Black) else null,
      stroke = if (shape.stroke) SolidColor(Color.Black) else null,
      strokeLineWidth = 1.5f,
      strokeLineCap = StrokeCap.Round,
      strokeLineJoin = StrokeJoin.Round
    )
  }
  return builder.build()
}

private const val RING = "M8 1.75a6.25 6.25 0 1 0 0 12.5a6.25 6.25 0 1 0 0-12.5z"

private fun dashedRing(): String =
  (0 until 9).joinToString(" ") { index ->
    fun point(degrees: Int): String {
      val radians = Math.toRadians(degrees.toDouble())
      val x = 8 + 6.25 * sin(radians)
      val y = 8 - 6.25 * cos(radians)
      return String.format(Locale.ROOT, "%.2f %.2f", x, y)
    }
    "M${point(index * 40)} A6.25 6.25 0 0 1 ${point(index * 40 + 20)}"
  }

object HandIcons {
  val Mark =
    icon(
      "mark",
      Shape("M8 1.5a6.5 6.5 0 1 0 0 13a6.5 6.5 0 1 0 0-13z"),
      Shape("M8 3.3v1"),
      Shape("M8 8l2.9-3.9"),
      Shape("M8 7.2a.8.8 0 1 0 0 1.6a.8.8 0 1 0 0-1.6z", fill = true)
    )
  val Failure = icon("failure", Shape(RING), Shape("M5.75 5.75l4.5 4.5M10.25 5.75l-4.5 4.5"))
  val Running =
    icon(
      "running",
      Shape("M8 1.75a6.25 6.25 0 1 0 6.25 6.25"),
      Shape("M8 5.25a2.75 2.75 0 1 0 0 5.5a2.75 2.75 0 1 0 0-5.5z", fill = true)
    )
  val Passing = icon("passing", Shape(RING), Shape("M5.25 8.25l1.9 1.9 3.6-4"))
  val Idle = icon("idle", Shape(dashedRing()))
  val More =
    icon(
      "more",
      Shape(
        "M8 2.25a1.25 1.25 0 1 0 0 2.5a1.25 1.25 0 1 0 0-2.5z" +
          "M8 6.75a1.25 1.25 0 1 0 0 2.5a1.25 1.25 0 1 0 0-2.5z" +
          "M8 11.25a1.25 1.25 0 1 0 0 2.5a1.25 1.25 0 1 0 0-2.5z",
        fill = true,
        stroke = false
      )
    )
}
