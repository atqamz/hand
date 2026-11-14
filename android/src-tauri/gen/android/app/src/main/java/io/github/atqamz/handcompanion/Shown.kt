package io.github.atqamz.handcompanion

import java.util.Locale

private fun bidi(code: Int) =
  code in 0x202A..0x202E || code in 0x2066..0x2069 || code == 0x200E || code == 0x200F || code == 0x061C

fun shown(text: String): String {
  if (text.isEmpty()) return text
  val normalized = text.replace("\r\n", "\n")
  val out = StringBuilder(normalized.length)
  var index = 0
  while (index < normalized.length) {
    val code = normalized.codePointAt(index)
    if (
      bidi(code) ||
        (Character.isISOControl(code) && code != '\n'.code && code != '\t'.code) ||
        code == 0x2028 ||
        code == 0x2029
    ) {
      out.append("U+").append(code.toString(16).uppercase(Locale.ROOT).padStart(4, '0'))
    } else {
      out.appendCodePoint(code)
    }
    index += Character.charCount(code)
  }
  return out.toString()
}
