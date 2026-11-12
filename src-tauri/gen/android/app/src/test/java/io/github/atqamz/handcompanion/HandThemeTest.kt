package io.github.atqamz.handcompanion

import androidx.compose.ui.graphics.Color
import org.junit.Assert.assertEquals
import org.junit.Test

private fun colors(vararg hex: Long) = hex.map { Color(0xFF000000 or it) }

class HandThemeTest {
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
      colors(0x111317, 0xE8EAEE, 0x15181D, 0x9BA2AD, 0x262B32, 0x6B7482, 0xE8EAEE, 0x111317),
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
      colors(0xFF6B6B, 0xE6AC45, 0x56C77A, 0x7EA6FF, 0x9BA2AD),
      listOf(Tone.Fail, Tone.Wait, Tone.Pass, Tone.Accent, Tone.Neutral).map(hand::ink)
    )
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
