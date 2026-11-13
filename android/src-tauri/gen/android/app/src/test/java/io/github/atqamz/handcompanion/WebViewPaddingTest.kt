package io.github.atqamz.handcompanion

import org.junit.Assert.assertEquals
import org.junit.Test

class WebViewPaddingTest {
  @Test
  fun noInsetsAddNothing() {
    assertEquals(WebViewPadding(0, 0), webViewPadding(0, 0, 0, 0))
  }

  @Test
  fun statusBarPadsTheTop() {
    assertEquals(WebViewPadding(96, 0), webViewPadding(96, 0, 0, 0))
  }

  @Test
  fun tallerCutoutBeatsTheStatusBar() {
    assertEquals(WebViewPadding(120, 0), webViewPadding(96, 0, 120, 0))
  }

  @Test
  fun shorterCutoutLeavesTheStatusBar() {
    assertEquals(WebViewPadding(96, 0), webViewPadding(96, 0, 48, 0))
  }

  @Test
  fun keyboardBeatsTheNavigationBar() {
    assertEquals(WebViewPadding(96, 900), webViewPadding(96, 48, 0, 900))
  }

  @Test
  fun navigationBarStaysWhenTheKeyboardCloses() {
    assertEquals(WebViewPadding(120, 48), webViewPadding(96, 48, 120, 0))
  }
}
