package io.github.atqamz.handcompanion

private const val MAX_LIST_DEPTH = 6
private const val MAX_PATHS = 50
private const val FILE_PREFIX = "file:"

data class MarkdownOptions(
  val refs: Boolean = true,
  val paths: Boolean = true,
  val filePaths: Boolean = paths
)

sealed interface MdInline

data class MdText(val text: String) : MdInline

data class MdCode(val text: String, val path: String? = null) : MdInline

data class MdStrong(val children: List<MdInline>) : MdInline

data class MdEm(val children: List<MdInline>) : MdInline

data class MdStrike(val children: List<MdInline>) : MdInline

data class MdLink(val url: String, val children: List<MdInline>) : MdInline

data class MdRef(val ref: String) : MdInline

data class MdPath(val path: String, val children: List<MdInline>) : MdInline

sealed interface MdBlock

data class MdParagraph(val lines: List<List<MdInline>>) : MdBlock

data class MdHeading(val level: Int, val text: List<MdInline>) : MdBlock

data class MdCodeBlock(val text: String) : MdBlock

data object MdRule : MdBlock

data class MdQuote(val blocks: List<MdBlock>) : MdBlock

data class MdTable(
  val head: List<List<MdInline>>,
  val rows: List<List<List<MdInline>>>,
  val align: List<String>
) : MdBlock

data class MdList(val ordered: Boolean, val start: Int, val items: List<MdItem>) : MdBlock

data class MdItem(val lead: List<MdInline>, val blocks: List<MdBlock>)

fun markdown(text: String, options: MarkdownOptions = MarkdownOptions()): List<MdBlock> =
  MarkdownParser(options).document(text)

fun markdownInline(text: String): List<MdInline> =
  MarkdownParser(PLAIN).inline(
    text.split(Regex("\\s+")).filter(String::isNotEmpty).joinToString(" "),
    false
  )

private class MarkdownParser(private val options: MarkdownOptions) {
  private var left = MAX_PATHS

  fun document(text: String): List<MdBlock> {
    val lines =
      text.replace("\r\n", "\n").replace('\r', '\n').split('\n')
    return blocks(lines, true, 0)
  }

  private fun blocks(lines: List<String>, quotes: Boolean, depth: Int): List<MdBlock> {
    val out = mutableListOf<MdBlock>()
    var i = 0
    while (i < lines.size) {
      val line = lines[i]
      when {
        line.isBlank() -> i++
        fence(line) -> {
          var j = i + 1
          while (j < lines.size && !fence(lines[j])) j++
          out += MdCodeBlock(lines.subList(i + 1, j).joinToString("\n"))
          i = j + 1
        }
        rule(line) -> {
          out += MdRule
          i++
        }
        heading(line) > 0 -> {
          val level = heading(line)
          out += MdHeading(level, inline(line.substring(level).trim(), true))
          i++
        }
        quotes && line.startsWith(">") -> {
          val inner = mutableListOf<String>()
          while (i < lines.size && lines[i].startsWith(">")) {
            val rest = lines[i].substring(1)
            inner += if (rest.startsWith(" ")) rest.substring(1) else rest
            i++
          }
          out += MdQuote(blocks(inner, false, depth))
        }
        tableAt(lines, i) -> {
          val (table, next) = table(lines, i)
          out += table
          i = next
        }
        item(line, false) != "" -> {
          val (list, next) = list(lines, i, false, depth)
          out += list
          i = next
        }
        item(line, true) != "" -> {
          val (list, next) = list(lines, i, true, depth)
          out += list
          i = next
        }
        else -> {
          val parts = mutableListOf<List<MdInline>>()
          while (
            i < lines.size &&
              lines[i].isNotBlank() &&
              (parts.isEmpty() || !starts(lines, i, quotes))
          ) {
            parts += inline(lines[i], true)
            i++
          }
          out += MdParagraph(parts)
        }
      }
    }
    return out
  }

  private fun starts(lines: List<String>, i: Int, quotes: Boolean): Boolean {
    val line = lines[i]
    return fence(line) ||
      rule(line) ||
      heading(line) > 0 ||
      quotes && line.startsWith(">") ||
      tableAt(lines, i) ||
      item(line, false) != "" ||
      item(line, true) != ""
  }

  private fun tableAt(lines: List<String>, i: Int): Boolean {
    if (i + 1 >= lines.size || "|" !in lines[i]) return false
    val delim = cells(lines[i + 1])
    if (delim.any { !ALIGN.matches(it) }) return false
    return delim.size == cells(lines[i]).size
  }

  private fun table(lines: List<String>, start: Int): Pair<MdTable, Int> {
    val head = cells(lines[start])
    val align = cells(lines[start + 1]).map { delim ->
      when {
        delim.startsWith(":") && delim.endsWith(":") -> "c"
        delim.endsWith(":") -> "r"
        delim.startsWith(":") -> "l"
        else -> ""
      }
    }.toMutableList()
    val rows = mutableListOf<List<String>>()
    var i = start + 2
    while (i < lines.size && lines[i].isNotBlank() && "|" in lines[i] && !fence(lines[i])) {
      val row = cells(lines[i]).toMutableList()
      while (row.size < head.size) row += ""
      rows += row.take(head.size)
      i++
    }
    for (k in head.indices) {
      if (align[k].isEmpty() && numericColumn(rows, k)) align[k] = "r"
    }
    val block =
      MdTable(
        head = head.map { inline(it, true) },
        rows = rows.map { row -> row.map { inline(it, true) } },
        align = align
      )
    return block to i
  }

  private fun numericColumn(rows: List<List<String>>, k: Int): Boolean {
    var seen = false
    for (row in rows) {
      val text = row[k]
      if (text.isEmpty()) continue
      if (!numeric(text)) return false
      seen = true
    }
    return seen
  }

  private fun list(
    lines: List<String>,
    start: Int,
    ordered: Boolean,
    depth: Int
  ): Pair<MdList, Int> {
    val number =
      if (ordered) {
        lines[start].substringBefore('.').toIntOrNull() ?: 1
      } else 1
    val items = mutableListOf<MdItem>()
    var i = start
    while (i < lines.size && item(lines[i], ordered) != "") {
      val lead = inline(item(lines[i], ordered), true)
      var j = i + 1
      while (
        j < lines.size &&
          (lines[j].isBlank() || lines[j].startsWith(" ") || lines[j].startsWith("\t"))
      ) {
        j++
      }
      val inner = dedent(lines.subList(i + 1, j))
      val nested =
        if (depth + 1 < MAX_LIST_DEPTH) blocks(inner, false, depth + 1)
        else flat(inner)
      items += MdItem(lead, nested)
      i = j
    }
    return MdList(ordered, number, items) to i
  }

  private fun flat(lines: List<String>): List<MdBlock> {
    val parts = lines.filter(String::isNotBlank).map { inline(it, true) }
    return if (parts.isEmpty()) emptyList() else listOf(MdParagraph(parts))
  }

  private fun dedent(lines: List<String>): List<String> {
    var cut = -1
    for (line in lines) {
      if (line.isNotBlank()) {
        val n = line.length - line.trimStart(' ', '\t').length
        if (cut < 0 || n < cut) cut = n
      }
    }
    if (cut < 0) cut = 0
    return lines.map { if (it.isBlank()) "" else it.substring(cut) }
  }

  internal fun inline(text: String, links: Boolean): List<MdInline> {
    val out = mutableListOf<MdInline>()
    val dead = mutableSetOf<String>()
    var i = 0
    while (i < text.length) {
      val (span, length) = marker(text, i, links, dead)
      if (length > 0) {
        if (span != null) out += span
        i += length
      } else {
        out += MdText(text.substring(i, i + 1))
        i++
      }
    }
    return merge(out)
  }

  private fun marker(
    text: String,
    index: Int,
    links: Boolean,
    dead: MutableSet<String>
  ): Pair<MdInline?, Int> {
    val rest = text.substring(index)
    return when {
      rest[0] == '`' && "`" !in dead -> {
        val j = rest.indexOf('`', 1)
        when {
          j > 1 -> {
            val value = rest.substring(1, j)
            MdCode(value, path(value, true, links)) to j + 1
          }
          else -> {
            if (j < 0) dead += "`"
            null to 0
          }
        }
      }
      rest.startsWith("~~") -> {
        if ("~~" !in dead) {
          val j = rest.indexOf("~~", 2)
          if (j > 2) {
            MdStrike(inline(rest.substring(2, j), links)) to j + 2
          } else {
            if (j < 0) dead += "~~"
            MdText("~~") to 2
          }
        } else MdText("~~") to 2
      }
      rest.startsWith("**") -> {
        if ("**" !in dead) {
          val j = rest.indexOf("**", 2)
          if (j > 2) {
            MdStrong(inline(rest.substring(2, j), links)) to j + 2
          } else {
            if (j < 0) dead += "**"
            MdText("**") to 2
          }
        } else MdText("**") to 2
      }
      (rest[0] == '*' || rest[0] == '_') && rest.substring(0, 1) !in dead -> {
        val j = closing(text, index)
        when {
          j > 0 -> MdEm(inline(text.substring(index + 1, j), links)) to j - index + 1
          else -> {
            if (j < 0) dead += rest.substring(0, 1)
            null to 0
          }
        }
      }
      rest[0] == '[' && links && "[" !in dead -> {
        val parsed = link(rest)
        val span = parsed.span
        when {
          span != null -> span
          parsed.dead -> {
            dead += "["
            null to 0
          }
          else -> null to 0
        }
      }
      links &&
        (rest.startsWith("http://") || rest.startsWith("https://")) &&
        (index == 0 || !word(text[index - 1]) && text[index - 1] != '/') -> {
        val url = token(rest)
        val href = if (url.isEmpty()) null else linkUrl(url)
        if (href != null) MdLink(href, listOf(MdText(url))) to url.length else null to 0
      }
      room(links) &&
        (pathLike(rest) || options.filePaths && rest.startsWith(FILE_PREFIX)) &&
        (index == 0 || !word(text[index - 1]) && text[index - 1] !in "/\\.~-_:") -> {
        val value = token(rest)
        val href = path(value, false, links)
        if (href != null) MdPath(href, listOf(MdText(value))) to value.length else null to 0
      }
      options.refs &&
        rest[0] in "tdar" &&
        (index == 0 || !word(text[index - 1]) && text[index - 1] !in "/\\_") -> {
        val length = refLength(rest)
        if (length > 0) MdRef(rest.substring(0, length)) to length else null to 0
      }
      else -> null to 0
    }
  }

  private class ParsedLink(val span: Pair<MdInline, Int>?, val dead: Boolean)

  private fun link(rest: String): ParsedLink {
    val mid = rest.indexOf("](")
    if (mid < 0) return ParsedLink(null, true)
    if (mid < 1) return ParsedLink(null, false)
    val end = rest.indexOf(')', mid + 2)
    if (end < 0) return ParsedLink(null, true)
    val url = linkUrl(rest.substring(mid + 2, end)) ?: return ParsedLink(null, false)
    val children = MarkdownParser(PLAIN).inline(rest.substring(1, mid), false)
    return ParsedLink(MdLink(url, children) to end + 1, false)
  }

  private fun room(links: Boolean) = links && options.paths && left > 0

  private fun path(value: String, relative: Boolean, links: Boolean): String? {
    if (!room(links)) return null
    if (
      !pathLike(value) &&
        !(relative && relLike(value)) &&
        !(options.filePaths && value.startsWith(FILE_PREFIX))
    ) {
      return null
    }
    left--
    return value
  }
}

private val ALIGN = Regex("^:?-+:?$")
private val PLAIN = MarkdownOptions(refs = false, paths = false, filePaths = false)

private fun merge(spans: List<MdInline>): List<MdInline> {
  val out = mutableListOf<MdInline>()
  for (span in spans) {
    val last = out.lastOrNull()
    if (span is MdText && last is MdText) out[out.lastIndex] = MdText(last.text + span.text)
    else out += span
  }
  return out
}

private fun fence(line: String) = line.trimStart().startsWith("```")

private fun rule(line: String): Boolean {
  val text = line.trim().replace(" ", "")
  return text.length >= 3 && text.all { it == text[0] } && text[0] in "-*_"
}

private fun heading(line: String): Int {
  var n = 0
  while (n < line.length && n < 7 && line[n] == '#') n++
  if (n == 0 || n > 6 || n == line.length || line[n] != ' ') return 0
  return n
}

private fun cells(line: String): List<String> {
  var text = line.trim()
  if (text.startsWith("|")) text = text.substring(1)
  if (text.endsWith("|") && !text.endsWith("\\|")) text = text.dropLast(1)
  val out = mutableListOf<String>()
  var start = 0
  for (j in text.indices) {
    if (text[j] == '|' && (j == 0 || text[j - 1] != '\\')) {
      out += text.substring(start, j)
      start = j + 1
    }
  }
  out += text.substring(start)
  return out.map { it.trim().replace("\\|", "|") }
}

internal fun numeric(text: String): Boolean {
  var digit = false
  for (c in text) {
    when {
      c in '0'..'9' -> digit = true
      c in "*_ .,%kKMx×~≈<>+-–—→" -> Unit
      else -> return false
    }
  }
  return digit
}

internal fun numericColumn(rows: List<List<String>>, k: Int): Boolean {
  var seen = false
  for (row in rows) {
    val text = row[k]
    if (text.isEmpty()) continue
    if (!numeric(text)) return false
    seen = true
  }
  return seen
}

private fun item(line: String, ordered: Boolean): String {
  if (!ordered) {
    return when {
      line.startsWith("- ") || line.startsWith("* ") -> line.substring(2)
      else -> ""
    }
  }
  var n = 0
  while (n < line.length && line[n] in '0'..'9') n++
  return if (n > 0 && line.startsWith(". ", n)) line.substring(n + 2) else ""
}

private fun word(c: Char) = c.isLetterOrDigit() || c.code >= 0x80

private fun pathLike(value: String): Boolean {
  var p = value
  when {
    p.startsWith("~/") -> p = p.substring(1)
    p.length > 2 && p[0].lowercaseChar() in 'a'..'z' && p[1] == ':' && (p[2] == '\\' || p[2] == '/') ->
      p = p.substring(2)
    !p.startsWith("/") -> return false
  }
  return p.length > 1 && p[1] !in " \t/\\"
}

internal fun relLike(value: String): Boolean {
  if (value.length > 1 && value[1] == ':' && value[0].lowercaseChar() in 'a'..'z') return false
  return "/" in value &&
    value.none { it == ' ' || it == '\t' } &&
    "://" !in value &&
    !value.startsWith("/") &&
    !value.startsWith("~") &&
    ".." !in value.split("/")
}

private fun closing(text: String, index: Int): Int {
  val c = text[index]
  if (index + 1 >= text.length || text[index + 1] == ' ') return 0
  if (c == '_' && index > 0 && word(text[index - 1])) return 0
  for (j in index + 2 until text.length) {
    if (
      text[j] == c &&
        text[j - 1] != ' ' &&
        (c == '*' || j + 1 == text.length || !word(text[j + 1]))
    ) {
      return j
    }
  }
  return -1
}

private fun token(rest: String): String {
  val n = rest.indexOfFirst { it in " \t\n<>\"'`" }
  var text = if (n < 0) rest else rest.substring(0, n)
  text = text.trimEnd('.', ',', ';', ':', '!', '?')
  if (text.endsWith(")") && "(" !in text) {
    text = text.dropLast(1).trimEnd('.', ',', ';', ':', '!', '?')
  }
  return text
}

private val LINK_SCHEME = Regex("^[A-Za-z][A-Za-z0-9+.-]*$")
private const val HEX = "0123456789ABCDEF"
private const val HOST_SAFE = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~!$&'()*+,;=:[]@%"
private const val TAIL_SAFE = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~:/?#[]@!$&'()*+,;=%"

internal fun linkUrl(raw: String): String? {
  if (raw.isEmpty() || raw.any { it.code < 0x20 || it.code == 0x7f }) return null
  val colon = raw.indexOf(':')
  if (colon <= 0) return null
  val scheme = raw.substring(0, colon)
  if (!LINK_SCHEME.matches(scheme)) return null
  if (!scheme.equals("http", true) && !scheme.equals("https", true)) return null
  if (!raw.startsWith("//", colon + 1)) return null
  val start = colon + 3
  var end = start
  while (end < raw.length && raw[end] !in "/?#") end++
  if (end == start) return null
  val authority = raw.substring(start, end)
  if (authority.any { it == ' ' || it in "\"<>\\^`{|}" }) return null
  val host = encodeUrl(authority, HOST_SAFE) ?: return null
  val tail = encodeUrl(raw.substring(end), TAIL_SAFE) ?: return null
  return scheme.lowercase() + "://" + host + tail
}

private fun encodeUrl(value: String, safe: String): String? {
  val out = StringBuilder(value.length)
  var i = 0
  while (i < value.length) {
    val c = value[i]
    when {
      c == '%' -> {
        if (i + 2 >= value.length || !value[i + 1].isHexDigit() || !value[i + 2].isHexDigit()) return null
        out.append(value, i, i + 3)
        i += 3
      }
      c.code in 0x20..0x7e && c in safe -> {
        out.append(c)
        i++
      }
      else -> {
        val code = value.codePointAt(i)
        for (byte in String(Character.toChars(code)).toByteArray(Charsets.UTF_8)) {
          val b = byte.toInt() and 0xff
          out.append('%').append(HEX[b shr 4]).append(HEX[b and 0x0f])
        }
        i += Character.charCount(code)
      }
    }
  }
  return out.toString()
}

private fun Char.isHexDigit() = this in '0'..'9' || this in 'a'..'f' || this in 'A'..'F'

private fun refLength(rest: String): Int {
  var n = 1
  while (n < rest.length && rest[n] in '0'..'9') n++
  if (
    n == 1 ||
      rest[1] == '0' ||
      n < rest.length && (word(rest[n]) || rest[n] == '_') ||
      n + 1 < rest.length && rest[n] == '.' && word(rest[n + 1])
  ) {
    return 0
  }
  return n
}
