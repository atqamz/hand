package io.github.atqamz.handcompanion

import androidx.compose.material3.ColorScheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.toArgb
import kotlin.math.abs
import kotlin.math.pow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

private fun colors(vararg hex: Long) = hex.map { Color(0xFF000000 or it) }

private fun fields(s: ColorScheme) =
  listOf(
    s.primary, s.onPrimary, s.primaryContainer, s.onPrimaryContainer, s.inversePrimary,
    s.secondary, s.onSecondary, s.secondaryContainer, s.onSecondaryContainer,
    s.tertiary, s.onTertiary, s.tertiaryContainer, s.onTertiaryContainer,
    s.background, s.onBackground, s.surface, s.onSurface, s.surfaceVariant, s.onSurfaceVariant,
    s.surfaceTint, s.inverseSurface, s.inverseOnSurface,
    s.error, s.onError, s.errorContainer, s.onErrorContainer,
    s.outline, s.outlineVariant, s.scrim,
    s.surfaceBright, s.surfaceDim, s.surfaceContainerLowest, s.surfaceContainerLow,
    s.surfaceContainer, s.surfaceContainerHigh, s.surfaceContainerHighest,
    s.primaryFixed, s.primaryFixedDim, s.onPrimaryFixed, s.onPrimaryFixedVariant,
    s.secondaryFixed, s.secondaryFixedDim, s.onSecondaryFixed, s.onSecondaryFixedVariant,
    s.tertiaryFixed, s.tertiaryFixedDim, s.onTertiaryFixed, s.onTertiaryFixedVariant
  )

private fun luminance(c: Color): Double {
  val argb = c.toArgb()
  val r = ((argb shr 16) and 0xFF) / 255.0
  val g = ((argb shr 8) and 0xFF) / 255.0
  val b = (argb and 0xFF) / 255.0
  return 0.2126729 * r.pow(2.4) + 0.7151522 * g.pow(2.4) + 0.072175 * b.pow(2.4)
}

private fun soften(y: Double) = if (y > 0.022) y else y + (0.022 - y).pow(1.414)

private fun apcaLc(text: Color, ground: Color): Double {
  val yt = soften(luminance(text))
  val yb = soften(luminance(ground))
  if (abs(yb - yt) < 0.0005) return 0.0
  if (yb > yt) {
    val s = (yb.pow(0.56) - yt.pow(0.57)) * 1.14
    return if (s >= 0.1) (s - 0.027) * 100 else 0.0
  }
  val s = (yb.pow(0.65) - yt.pow(0.62)) * 1.14
  return if (s <= -0.1) (s + 0.027) * 100 else 0.0
}

class HandThemeTest {
  @Test
  fun apcaKnownAnswers() {
    assertEquals(106.04, apcaLc(Color.Black, Color.White), 0.01)
    assertEquals(-107.88, apcaLc(Color.White, Color.Black), 0.01)
  }

  @Test
  fun darkSecondaryTextReachesApcaLc60() {
    val hand = DarkHand
    val texts =
      mapOf(
        "muted" to (hand.muted to hand.fill(Tone.Neutral)),
        "neutral" to (hand.ink(Tone.Neutral) to hand.fill(Tone.Neutral)),
        "accent" to (hand.ink(Tone.Accent) to hand.fill(Tone.Accent)),
        "fail" to (hand.ink(Tone.Fail) to hand.fill(Tone.Fail))
      )
    for ((name, text) in texts) {
      val grounds =
        mapOf(
          "bg" to hand.bg,
          "panel" to hand.panel,
          "neutral fill" to hand.fill(Tone.Neutral),
          "own chip" to text.second
        )
      for ((groundName, ground) in grounds) {
        val lc = abs(apcaLc(text.first, ground))
        assertTrue("$name on $groundName: Lc $lc", lc >= 60.0)
      }
    }
  }

  @Test
  fun everyLightSchemeFieldIsAHandToken() {
    assertEquals(
      colors(
        0x15171C, 0xFFFFFF, 0xEEF0F3, 0x15171C, 0xFFFFFF,
        0x5B626E, 0xFFFFFF, 0xE3E6EA, 0x15171C,
        0x2457C5, 0xFFFFFF, 0xEBF1FD, 0x2457C5,
        0xFFFFFF, 0x15171C, 0xFFFFFF, 0x15171C, 0xF6F7F9, 0x5B626E,
        0x15171C, 0x15171C, 0xFFFFFF,
        0xC42B32, 0xFFFFFF, 0xFDEEEE, 0xC42B32,
        0x80889A, 0xE3E6EA, 0x000000,
        0xFFFFFF, 0xF6F7F9, 0xFFFFFF, 0xFFFFFF,
        0xF6F7F9, 0xF6F7F9, 0xF6F7F9,
        0xEEF0F3, 0xE3E6EA, 0x15171C, 0x5B626E,
        0xF6F7F9, 0xE3E6EA, 0x15171C, 0x5B626E,
        0xEBF1FD, 0xE3E6EA, 0x2457C5, 0x2457C5
      ),
      fields(scheme(LightHand, lightColorScheme()))
    )
  }

  @Test
  fun everyDarkSchemeFieldIsAHandToken() {
    assertEquals(
      colors(
        0xE8EAEE, 0x111317, 0x1E232A, 0xE8EAEE, 0x111317,
        0xB0B6BF, 0x111317, 0x262B32, 0xE8EAEE,
        0x9BB6FF, 0x111317, 0x172238, 0x9BB6FF,
        0x111317, 0xE8EAEE, 0x111317, 0xE8EAEE, 0x15181D, 0xB0B6BF,
        0xE8EAEE, 0xE8EAEE, 0x111317,
        0xFF9999, 0x111317, 0x2C1719, 0xFF9999,
        0x6B7482, 0x262B32, 0x000000,
        0x111317, 0x15181D, 0x111317, 0x111317,
        0x15181D, 0x15181D, 0x15181D,
        0x1E232A, 0x262B32, 0xE8EAEE, 0xB0B6BF,
        0x15181D, 0x262B32, 0xE8EAEE, 0xB0B6BF,
        0x172238, 0x262B32, 0x9BB6FF, 0x9BB6FF
      ),
      fields(scheme(DarkHand, darkColorScheme()))
    )
  }

  @Test
  fun lightColoursAreTheBoards() {
    val hand = LightHand
    assertEquals(
      colors(0xFFFFFF, 0x15171C, 0xF6F7F9, 0x5B626E, 0xE3E6EA, 0x80889A, 0x15171C, 0xFFFFFF),
      listOf(
        hand.bg,
        hand.fg,
        hand.panel,
        hand.muted,
        hand.line,
        hand.lineStrong,
        hand.primary,
        hand.onPrimary
      )
    )
    assertEquals(
      colors(0xC42B32, 0x8A5B00, 0x1A7A3E, 0x2457C5, 0x5B626E),
      listOf(Tone.Fail, Tone.Wait, Tone.Pass, Tone.Accent, Tone.Neutral).map(hand::ink)
    )
    assertEquals(colors(0x111418, 0xD8DDE4), listOf(hand.screen, hand.screenFg))
    assertEquals(colors(0xF1F3F5), listOf(hand.card))
    assertEquals(
      colors(0xFDEEEE, 0xFCF3DC, 0xE4F4E9, 0xEBF1FD, 0xEEF0F3),
      listOf(Tone.Fail, Tone.Wait, Tone.Pass, Tone.Accent, Tone.Neutral).map(hand::fill)
    )
    assertEquals(
      colors(
        0x407ACC, 0x736AD7, 0x9F58D3, 0xCA38BE, 0xCE4880, 0xCD4F44,
        0xA1702B, 0x777F22, 0x4D8724, 0x258B2D, 0x24895F, 0x25848D
      ),
      hand.tints
    )
  }

  @Test
  fun darkColoursAreTheBoards() {
    val hand = DarkHand
    assertEquals(
      colors(0x111317, 0xE8EAEE, 0x15181D, 0xB0B6BF, 0x262B32, 0x6B7482, 0xE8EAEE, 0x111317),
      listOf(
        hand.bg,
        hand.fg,
        hand.panel,
        hand.muted,
        hand.line,
        hand.lineStrong,
        hand.primary,
        hand.onPrimary
      )
    )
    assertEquals(
      colors(0xFF9999, 0xE6AC45, 0x56C77A, 0x9BB6FF, 0xB0B6BF),
      listOf(Tone.Fail, Tone.Wait, Tone.Pass, Tone.Accent, Tone.Neutral).map(hand::ink)
    )
    assertEquals(colors(0x08090B, 0xD8DDE4), listOf(hand.screen, hand.screenFg))
    assertEquals(colors(0x1B1F25), listOf(hand.card))
    assertEquals(
      colors(0x2C1719, 0x2A2212, 0x13281B, 0x172238, 0x1E232A),
      listOf(Tone.Fail, Tone.Wait, Tone.Pass, Tone.Accent, Tone.Neutral).map(hand::fill)
    )
    assertEquals(
      colors(
        0x5B8FE0, 0x8B82EA, 0xB06FE3, 0xD955CC, 0xE0628F, 0xE06A5E,
        0xC18A3C, 0x9AA13A, 0x6EA73D, 0x45AD50, 0x3FAC7C, 0x3FA6B0
      ),
      hand.tints
    )
  }
}
