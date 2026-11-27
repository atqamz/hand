package io.github.atqamz.handcompanion

private val OPAQUE = setOf("mailto", "tel")
private val WEB = setOf("http", "https")
private val NUMERIC_LABEL = Regex("0[xX][0-9a-fA-F]*|[0-9]+")
private val HEX_GROUP = Regex("[0-9a-fA-F]{1,4}")

fun externalTarget(url: String): ThreadTarget {
  if (url.any { it.isISOControl() || it.isWhitespace() }) return ThreadTarget.None
  val scheme = url.substringBefore(':', "").lowercase()
  val reachable =
    when {
      scheme in OPAQUE -> true
      scheme in WEB && url.startsWith("://", scheme.length) -> hostOf(url.substring(scheme.length + 3))?.let { !boardHost(it) } == true
      else -> false
    }
  return if (reachable) ThreadTarget.Browser(url) else ThreadTarget.None
}

private fun hostOf(rest: String): String? {
  val hostPort = rest.takeWhile { it !in "/?#\\" }.substringAfterLast('@')
  val end = if (hostPort.startsWith("[")) hostPort.indexOf(']') + 1 else hostPort.substringBefore(':').length
  if (end == 0 && hostPort.startsWith("[")) return null
  val port = hostPort.substring(end).removePrefix(":")
  return hostPort.substring(0, end).takeIf { port.all(Char::isDigit) && (hostPort.length == end || hostPort[end] == ':') }
}

private fun boardHost(host: String): Boolean {
  if (host.isEmpty() || '%' in host || host.endsWith(".")) return true
  val name = host.lowercase()
  if (name == "localhost" || name.endsWith(".localhost")) return true
  if (name.startsWith("[")) return boardV6(name.removeSurrounding("[", "]"))
  val labels = name.split('.')
  return labels.all(NUMERIC_LABEL::matches) && boardV4(labels)
}

private fun boardV6(text: String): Boolean {
  val groups = v6Groups(text) ?: return true
  val embedded = groups.take(6).all { it == 0 } || (groups.take(5).all { it == 0 } && groups[5] == 0xFFFF)
  return embedded && boardAddress((groups[6].toLong() shl 16) + groups[7]) || groups[0] and 0xFFC0 == 0xFE80
}

private fun v6Groups(text: String): List<Int>? {
  val halves = text.split("::")
  val head = v6Parts(halves[0]) ?: return null
  val tail = if (halves.size == 2) v6Parts(halves[1]) ?: return null else emptyList()
  val missing = 8 - head.size - tail.size
  if (if (halves.size == 2) missing < 1 else missing != 0) return null
  return head + List(missing) { 0 } + tail
}

private fun v6Parts(text: String): List<Int>? {
  if (text.isEmpty()) return emptyList()
  val parts = text.split(':')
  val last = parts.last()
  val tail =
    if ('.' in last) {
      val octets = last.split('.').map { if (it.length in 1..3 && it.all(Char::isDigit)) it.toInt() else -1 }
      if (octets.size != 4 || octets.any { it !in 0..255 }) return null
      listOf(octets[0] * 256 + octets[1], octets[2] * 256 + octets[3])
    } else if (HEX_GROUP.matches(last)) {
      listOf(last.toInt(16))
    } else {
      return null
    }
  val front = parts.dropLast(1)
  if (!front.all(HEX_GROUP::matches)) return null
  return front.map { it.toInt(16) } + tail
}

private fun boardV4(labels: List<String>): Boolean {
  if (labels.size > 4) return true
  val parts = labels.map(::numberOf)
  if (parts.any { it == null }) return true
  val numbers = parts.filterNotNull()
  val head = numbers.dropLast(1)
  if (head.any { it > 255 } || numbers.last() >= 1L shl (8 * (5 - labels.size))) return true
  val address = head.foldIndexed(numbers.last()) { index, sum, value -> sum + (value shl (8 * (3 - index))) }
  return boardAddress(address)
}

private fun boardAddress(address: Long) = address shr 24 == 0L || address shr 24 == 127L || address shr 16 == 0xA9FEL

private fun numberOf(label: String): Long? =
  when {
    label.length > 1 && label[0] == '0' && label[1] in "xX" -> label.drop(2).toLongOrNull(16)
    label.length > 1 && label[0] == '0' -> label.toLongOrNull(8)
    else -> label.toLongOrNull()
  }
