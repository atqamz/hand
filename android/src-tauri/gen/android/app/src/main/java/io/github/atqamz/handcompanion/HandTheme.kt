package io.github.atqamz.handcompanion

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.Surface
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp

enum class Tone {
  Fail,
  Wait,
  Pass,
  Accent,
  Neutral
}

class HandColors(
  val bg: Color,
  val fg: Color,
  val panel: Color,
  val card: Color,
  val muted: Color,
  val line: Color,
  val lineStrong: Color,
  val primary: Color,
  val onPrimary: Color,
  val screen: Color,
  val screenFg: Color,
  val tints: List<Color>,
  private val ink: Map<Tone, Color>,
  private val fill: Map<Tone, Color>
) {
  fun ink(tone: Tone) = ink.getValue(tone)

  fun fill(tone: Tone) = fill.getValue(tone)
}

private fun tones(fail: Long, wait: Long, pass: Long, neutral: Long, accent: Long) =
  mapOf(
    Tone.Fail to Color(fail),
    Tone.Wait to Color(wait),
    Tone.Pass to Color(pass),
    Tone.Neutral to Color(neutral),
    Tone.Accent to Color(accent)
  )

private fun tints(vararg values: Long) = values.map { Color(it) }

val LightHand =
  HandColors(
    bg = Color(0xFFFFFFFF),
    fg = Color(0xFF15171C),
    panel = Color(0xFFF6F7F9),
    card = Color(0xFFF1F3F5),
    muted = Color(0xFF5B626E),
    line = Color(0xFFE3E6EA),
    lineStrong = Color(0xFF80889A),
    primary = Color(0xFF15171C),
    onPrimary = Color(0xFFFFFFFF),
    screen = Color(0xFF111418),
    screenFg = Color(0xFFD8DDE4),
    tints =
      tints(
        0xFF407ACC, 0xFF736AD7, 0xFF9F58D3, 0xFFCA38BE, 0xFFCE4880, 0xFFCD4F44,
        0xFFA1702B, 0xFF777F22, 0xFF4D8724, 0xFF258B2D, 0xFF24895F, 0xFF25848D
      ),
    ink = tones(0xFFC42B32, 0xFF8A5B00, 0xFF1A7A3E, 0xFF5B626E, 0xFF2457C5),
    fill = tones(0xFFFDEEEE, 0xFFFCF3DC, 0xFFE4F4E9, 0xFFEEF0F3, 0xFFEBF1FD)
  )

val DarkHand =
  HandColors(
    bg = Color(0xFF111317),
    fg = Color(0xFFE8EAEE),
    panel = Color(0xFF15181D),
    card = Color(0xFF1B1F25),
    muted = Color(0xFF9BA2AD),
    line = Color(0xFF262B32),
    lineStrong = Color(0xFF6B7482),
    primary = Color(0xFFE8EAEE),
    onPrimary = Color(0xFF111317),
    screen = Color(0xFF08090B),
    screenFg = Color(0xFFD8DDE4),
    tints =
      tints(
        0xFF5B8FE0, 0xFF8B82EA, 0xFFB06FE3, 0xFFD955CC, 0xFFE0628F, 0xFFE06A5E,
        0xFFC18A3C, 0xFF9AA13A, 0xFF6EA73D, 0xFF45AD50, 0xFF3FAC7C, 0xFF3FA6B0
      ),
    ink = tones(0xFFFF6B6B, 0xFFE6AC45, 0xFF56C77A, 0xFF9BA2AD, 0xFF7EA6FF),
    fill = tones(0xFF2C1719, 0xFF2A2212, 0xFF13281B, 0xFF1E232A, 0xFF172238)
  )

val LocalHand = staticCompositionLocalOf { LightHand }

fun tint(fleet: String): Int {
  var hash = 0x811C9DC5.toInt()
  for (byte in fleet.toByteArray()) hash = (hash xor (byte.toInt() and 0xFF)) * 0x01000193
  return (hash.toUInt() % 12u).toInt()
}

internal fun scheme(hand: HandColors, base: ColorScheme): ColorScheme {
  val accent = hand.ink(Tone.Accent)
  val accentFill = hand.fill(Tone.Accent)
  val neutralFill = hand.fill(Tone.Neutral)
  return base.copy(
    primary = hand.primary,
    onPrimary = hand.onPrimary,
    primaryContainer = neutralFill,
    onPrimaryContainer = hand.fg,
    inversePrimary = hand.bg,
    secondary = hand.muted,
    onSecondary = hand.bg,
    secondaryContainer = hand.line,
    onSecondaryContainer = hand.fg,
    tertiary = accent,
    onTertiary = hand.bg,
    tertiaryContainer = accentFill,
    onTertiaryContainer = accent,
    background = hand.bg,
    onBackground = hand.fg,
    surface = hand.bg,
    onSurface = hand.fg,
    surfaceVariant = hand.panel,
    onSurfaceVariant = hand.muted,
    surfaceTint = hand.primary,
    inverseSurface = hand.fg,
    inverseOnSurface = hand.bg,
    error = hand.ink(Tone.Fail),
    onError = hand.bg,
    errorContainer = hand.fill(Tone.Fail),
    onErrorContainer = hand.ink(Tone.Fail),
    outline = hand.lineStrong,
    outlineVariant = hand.line,
    scrim = Color.Black,
    surfaceBright = hand.bg,
    surfaceDim = hand.panel,
    surfaceContainerLowest = hand.bg,
    surfaceContainerLow = hand.bg,
    surfaceContainer = hand.panel,
    surfaceContainerHigh = hand.panel,
    surfaceContainerHighest = hand.panel,
    primaryFixed = neutralFill,
    primaryFixedDim = hand.line,
    onPrimaryFixed = hand.fg,
    onPrimaryFixedVariant = hand.muted,
    secondaryFixed = hand.panel,
    secondaryFixedDim = hand.line,
    onSecondaryFixed = hand.fg,
    onSecondaryFixedVariant = hand.muted,
    tertiaryFixed = accentFill,
    tertiaryFixedDim = hand.line,
    onTertiaryFixed = accent,
    onTertiaryFixedVariant = accent
  )
}

private fun style(
  size: Int,
  line: Int,
  weight: FontWeight,
  spacing: Float = 0f,
  features: String? = null
) =
  TextStyle(
    fontSize = size.sp,
    lineHeight = line.sp,
    fontWeight = weight,
    letterSpacing = spacing.em,
    fontFeatureSettings = features
  )

val HandType =
  Typography(
    headlineMedium = style(22, 30, FontWeight.SemiBold, -0.01f),
    headlineSmall = style(18, 24, FontWeight.SemiBold, -0.01f),
    titleMedium = style(15, 22, FontWeight.SemiBold),
    bodyLarge = style(15, 24, FontWeight.Normal),
    bodyMedium = style(14, 20, FontWeight.Normal),
    labelLarge = style(14, 20, FontWeight.SemiBold),
    labelMedium = style(12, 18, FontWeight.SemiBold, features = "tnum"),
    bodySmall = style(12, 18, FontWeight.Normal, features = "tnum")
  )

val HandMono =
  style(12, 18, FontWeight.Normal, features = "tnum").copy(fontFamily = FontFamily.Monospace)

val HandShapes =
  Shapes(
    extraSmall = RoundedCornerShape(3.dp),
    small = RoundedCornerShape(4.dp),
    medium = RoundedCornerShape(6.dp),
    large = RoundedCornerShape(10.dp)
  )

@Composable
fun HandTheme(dark: Boolean = isSystemInDarkTheme(), content: @Composable () -> Unit) {
  val hand = if (dark) DarkHand else LightHand
  CompositionLocalProvider(LocalHand provides hand) {
    MaterialTheme(
      colorScheme = scheme(hand, if (dark) darkColorScheme() else lightColorScheme()),
      typography = HandType,
      shapes = HandShapes
    ) {
      Surface(color = hand.bg, content = content)
    }
  }
}
