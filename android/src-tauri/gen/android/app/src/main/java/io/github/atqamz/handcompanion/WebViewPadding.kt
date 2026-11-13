package io.github.atqamz.handcompanion

internal data class WebViewPadding(val top: Int, val bottom: Int)

internal fun webViewPadding(
  systemBarsTop: Int,
  systemBarsBottom: Int,
  displayCutoutTop: Int,
  imeBottom: Int
): WebViewPadding = WebViewPadding(
  top = maxOf(systemBarsTop, displayCutoutTop),
  bottom = maxOf(systemBarsBottom, imeBottom)
)
