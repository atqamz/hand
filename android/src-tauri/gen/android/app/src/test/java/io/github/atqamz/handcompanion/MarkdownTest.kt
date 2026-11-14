package io.github.atqamz.handcompanion

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

private fun subset(text: String) =
  html(text, MarkdownOptions(refs = false, paths = false, filePaths = false))

private fun refs(text: String) =
  html(text, MarkdownOptions(refs = true, paths = false, filePaths = false))

private fun paths(text: String) =
  html(text, MarkdownOptions(refs = false, paths = true, filePaths = false))

private fun both(text: String) =
  html(text, MarkdownOptions(refs = true, paths = true, filePaths = false))

private fun html(text: String, options: MarkdownOptions): String = html(markdown(text, options))

private fun html(blocks: List<MdBlock>): String = blocks.joinToString("") { block(it) }

private fun block(block: MdBlock): String =
  when (block) {
    is MdParagraph -> "<p>" + block.lines.joinToString("<br>") { inlines(it) } + "</p>"
    is MdHeading -> "<" + head(block.level) + ">" + inlines(block.text) + "</" + head(block.level) + ">"
    is MdCodeBlock -> "<pre><code>" + escape(block.text) + "</code></pre>"
    MdRule -> "<hr>"
    is MdQuote -> "<blockquote>" + html(block.blocks) + "</blockquote>"
    is MdTable -> table(block)
    is MdList -> list(block)
  }

private fun head(level: Int) = if (level <= 2) "h3" else "h4"

private fun list(list: MdList): String {
  val tag = if (list.ordered) "ol" else "ul"
  val open = if (list.ordered && list.start != 1) "<ol start=\"${list.start}\">" else "<$tag>"
  return open + list.items.joinToString("") { item ->
    "<li>" + inlines(item.lead) + html(item.blocks) + "</li>"
  } + "</$tag>"
}

private const val WRAP = "<div class=\"table\" role=\"region\" aria-label=\"Table\" tabindex=\"0\"><table>"

private fun table(table: MdTable): String {
  val head =
    "<thead><tr>" +
      table.head.mapIndexed { index, cell -> cell("th", table.align[index], cell) }.joinToString("") +
      "</tr></thead>"
  val body =
    "<tbody>" +
      table.rows.joinToString("") { row ->
        "<tr>" + row.mapIndexed { index, cell -> cell("td", table.align[index], cell) }.joinToString("") + "</tr>"
      } +
      "</tbody>"
  return WRAP + head + body + "</table></div>"
}

private fun cell(tag: String, align: String, cell: List<MdInline>): String {
  val open = if (align.isEmpty()) "<$tag>" else "<$tag class=\"$align\">"
  return open + inlines(cell) + "</$tag>"
}

private fun inlines(spans: List<MdInline>): String = spans.joinToString("") { span ->
  when (span) {
    is MdText -> escape(span.text)
    is MdCode ->
      if (span.path == null) "<code>${escape(span.text)}</code>"
      else pathLink(span.path, "<code>${escape(span.text)}</code>")
    is MdStrong -> "<strong>" + inlines(span.children) + "</strong>"
    is MdEm -> "<em>" + inlines(span.children) + "</em>"
    is MdStrike -> "<del>" + inlines(span.children) + "</del>"
    is MdLink ->
      "<a href=\"${escape(span.url)}\" target=\"_blank\" rel=\"noopener noreferrer\">" +
        inlines(span.children) + "</a>"
    is MdRef -> "<a class=\"ref\" href=\"/f1/ref/${span.ref}\">${span.ref}</a>"
    is MdPath -> pathLink(span.path, inlines(span.children))
  }
}

private fun pathLink(path: String, text: String) =
  "<a class=\"path\" href=\"/f1/doc/${pathEscape(path)}\" target=\"_blank\" rel=\"noopener\">$text</a>"

private fun escape(text: String) =
  buildString {
    for (c in text) {
      when (c) {
        '&' -> append("&amp;")
        '<' -> append("&lt;")
        '>' -> append("&gt;")
        '"' -> append("&#34;")
        '\'' -> append("&#39;")
        else -> append(c)
      }
    }
  }

private fun pathEscape(text: String) =
  buildString {
    for (byte in text.toByteArray()) {
      val code = byte.toInt() and 0xFF
      val c = code.toChar()
      if (code in 'a'.code..'z'.code || code in 'A'.code..'Z'.code || code in '0'.code..'9'.code ||
        c in "-._~$&+=:@") {
        append(c)
      } else {
        append('%').append(code.toString(16).uppercase().padStart(2, '0'))
      }
    }
  }

private fun pathValues(blocks: List<MdBlock>): List<String> {
  val out = mutableListOf<String>()
  fun inline(spans: List<MdInline>) {
    for (span in spans) {
      when (span) {
        is MdCode -> span.path?.let(out::add)
        is MdStrong -> inline(span.children)
        is MdEm -> inline(span.children)
        is MdStrike -> inline(span.children)
        is MdLink -> inline(span.children)
        is MdPath -> {
          out += span.path
          inline(span.children)
        }
        else -> Unit
      }
    }
  }
  fun block(block: MdBlock) {
    when (block) {
      is MdParagraph -> block.lines.forEach(::inline)
      is MdHeading -> inline(block.text)
      is MdQuote -> block.blocks.forEach(::block)
      is MdTable -> {
        block.head.forEach(::inline)
        block.rows.forEach { row -> row.forEach(::inline) }
      }
      is MdList -> block.items.forEach {
        inline(it.lead)
        it.blocks.forEach(::block)
      }
      else -> Unit
    }
  }
  blocks.forEach(::block)
  return out
}

class MarkdownTest {
  @Test
  fun theSubsetRendersAsTheBoardRendersIt() {
    for ((input, want) in SUBSET) {
      assertEquals("Render($input)", want, subset(input))
    }
  }

  @Test
  fun blocksRenderAsTheBoardRendersThem() {
    for ((input, want) in BLOCKS) {
      assertEquals("Render($input)", want, subset(input))
    }
  }

  @Test
  fun tablesRenderAsTheBoardRendersThem() {
    for ((input, want) in TABLES) {
      assertEquals("Render($input)", want, subset(input))
    }
  }

  @Test
  fun refsRenderAsTheBoardRendersThem() {
    for ((input, want) in REFS) {
      assertEquals("RenderRefs($input)", want, refs(input))
    }
    assertEquals("<p>see t12</p>", subset("see t12"))
  }

  @Test
  fun numberedListsKeepTheirNumbers() {
    for ((input, want) in NUMBERED) {
      assertEquals("Render($input)", want, subset(input))
    }
  }

  @Test
  fun fourNumberedItemsWithBulletsStayOneList() {
    val input = StringBuilder()
    val want = StringBuilder("<ol>")
    for (n in 1..4) {
      if (n > 1) input.append("\n\n")
      input.append("$n. **Step $n**\n   - first note $n\n   - second note $n\n   - third note $n")
      want.append(
        "<li><strong>Step $n</strong><ul><li>first note $n</li>" +
          "<li>second note $n</li><li>third note $n</li></ul></li>"
      )
    }
    want.append("</ol>")
    assertEquals(want.toString(), subset(input.toString()))
  }

  @Test
  fun listNestingIsCappedAtSixAndKeepsTheText() {
    val input = StringBuilder()
    for (n in 0 until 600) input.append(" ".repeat(n)).append("1. item$n\n")
    val got = subset(input.toString())
    assertEquals(6, Regex("<ol>").findAll(got).count())
    for (n in 0 until 600) assertTrue("item$n missing", got.contains("item$n<"))
  }

  @Test
  fun pathsRenderAsTheBoardRendersThem() {
    for ((input, want) in PATHS) {
      assertEquals("Render($input)", want, paths(input))
    }
  }

  @Test
  fun relativePathsLinkOnlyInsideBackticks() {
    for ((input, want) in RELATIVE) {
      assertEquals("Render($input)", want, paths(input))
    }
  }

  @Test
  fun pathsWithSpacesAndUnicodeLink() {
    for ((input, want) in SPACED) {
      assertEquals("Render($input)", want, paths(input))
    }
  }

  @Test
  fun refsAndPathsTogether() {
    assertEquals(
      "<p><a class=\"ref\" href=\"/f1/ref/t1\">t1</a> wrote " +
        pathLink("/fleet/reports/t1/a.md", "/fleet/reports/t1/a.md") +
        " and " + pathLink("/fleet/reports/t2/b.md", "/fleet/reports/t2/b.md") + "</p>",
      both("t1 wrote /fleet/reports/t1/a.md and /fleet/reports/t2/b.md")
    )
  }

  @Test
  fun onlyPlausibleRelativePathsReachTheHook() {
    val input =
      "`reports/a.md` `./a/b` `a/../b` `../a/b` `reports/..` `https://a.io/x` " +
        "`file://x/y` `a b/c` `a\tb/c` `readme.md` `a.md` `/x/y` `~/x/y` `~u/x/y` " +
        "`C:/x/y` `c:\\x\\y` `//host/x` `x:y/z`"
    assertEquals(
      listOf("reports/a.md", "./a/b", "/x/y", "~/x/y", "C:/x/y", "c:\\x\\y"),
      pathValues(markdown(input, MarkdownOptions(refs = false, paths = true, filePaths = false)))
    )
  }

  @Test
  fun onlyAbsolutePathsLinkInPlainText() {
    val input =
      "a/fleet/x ./x ../x //host/x / and/or 1/2 https://a.io/fleet/x C:x c:\\ \\\\host\\share " +
        "~user/x ~ \t x t1/x_/y\n```\n/fleet/x\n```"
    assertEquals(
      emptyList<String>(),
      pathValues(markdown(input, MarkdownOptions(refs = false, paths = true, filePaths = false)))
    )
    assertEquals(
      listOf("/fleet/a.md:12", "/fleet/a b.md", "C:\\fleet\\a.md", "c:/fleet/a.md", "~/fleet/a.md"),
      pathValues(
        markdown(
          "see /fleet/a.md:12, `/fleet/a b.md`, (C:\\fleet\\a.md) c:/fleet/a.md and ~/fleet/a.md.",
          MarkdownOptions(refs = false, paths = true, filePaths = false)
        )
      )
    )
  }

  @Test
  fun atMostFiftyPathsAreCheckedInOneMessage() {
    val input = StringBuilder()
    for (n in 0 until 60) {
      val path = "/fleet/p$n.md"
      input.append(if (n % 2 == 0) "`$path`" else path).append("\n")
    }
    val got = paths(input.toString())
    assertEquals(50, Regex("<a class=\"path\"").findAll(got).count())
    assertTrue(got.contains("<br>/fleet/p59.md</p>"))
  }

  @Test
  fun aFileUrlLinksTheFallbacksPath() {
    val got = html("`file:///fleet/a.md`", MarkdownOptions(refs = false, paths = true, filePaths = true))
    assertEquals("<p>" + pathLink("file:///fleet/a.md", "<code>file:///fleet/a.md</code>") + "</p>", got)
  }

  @Test
  fun unclosedMarkersStayText() {
    for ((input, want) in UNCLOSED) {
      assertEquals("Render($input)", want, subset(input))
    }
  }

  @Test
  fun theTitleRendersInlineAsTheBoardDoes() {
    assertEquals("<strong>Cookie</strong> rename: done", inlines(markdownInline("**Cookie** rename: done")))
    assertEquals("a b", inlines(markdownInline("a\n  b")))
    assertEquals("a b", inlines(markdownInline("a\u00A0b")))
    assertEquals("x****y", inlines(markdownInline("x****y")))
  }

  @Test
  fun unsafeLinksNeverBecomeLinks() {
    for (url in listOf(
      "javascript:alert(1)",
      "JaVaScRiPt:alert(1)",
      "data:text/html,x",
      "vbscript:x",
      "//evil.io/x",
      "/relative",
      "mailto:a@b.c",
      "https:///nohost",
      "https://@/x",
      "https://user@/x",
      "https://a.io:bad/x",
      "https://[]"
    )) {
      val blocks = markdown("[x]($url)", MarkdownOptions(refs = false, paths = false, filePaths = false))
      assertEquals("unsafe link $url linked", "<p>[x](${escape(url)})</p>", html(blocks))
      assertFalse(blocks.any { it is MdParagraph && it.lines.flatten().any { span -> span is MdLink } })
    }
  }

  @Test
  fun htmlAndImagesDegradeToText() {
    assertEquals("<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>", subset("<script>alert(1)</script>"))
    assertEquals("<p>&lt;img src=x onerror=y&gt;</p>", subset("<img src=x onerror=y>"))
    assertEquals("<p>![x](x.png)</p>", subset("![x](x.png)"))
    assertEquals("<p>!<a href=\"https://example.com/x.png\" target=\"_blank\" rel=\"noopener noreferrer\">x</a></p>", subset("![x](https://example.com/x.png)"))
  }

  @Test
  fun taskListItemsStayText() {
    assertEquals("<ul><li>[ ] todo</li></ul>", subset("- [ ] todo"))
    assertEquals("<ul><li>[x] done</li></ul>", subset("- [x] done"))
  }

  @Test
  fun headingsOutsideTheBoardLevelsDegrade() {
    assertEquals("<p>#tag</p>", subset("#tag"))
    assertEquals("<p>####### seven</p>", subset("####### seven"))
  }

  @Test
  fun linkTextKeepsNoRefsOrPaths() {
    assertEquals(
      "<p><a href=\"https://a.io\" target=\"_blank\" rel=\"noopener noreferrer\">/fleet/a.md</a></p>",
      paths("[/fleet/a.md](https://a.io)")
    )
    assertEquals(
      "<p><a href=\"https://a.io\" target=\"_blank\" rel=\"noopener noreferrer\">t12</a></p>",
      refs("[t12](https://a.io)")
    )
  }

  private companion object {
    val SUBSET = listOf(
      "**a** *b* _c_ `d`" to "<p><strong>a</strong> <em>b</em> <em>c</em> <code>d</code></p>",
      "x\ny" to "<p>x<br>y</p>",
      "x\n\ny" to "<p>x</p><p>y</p>",
      "x\r\n\r\ny" to "<p>x</p><p>y</p>",
      "- a\n- b" to "<ul><li>a</li><li>b</li></ul>",
      "* a\n* **b**" to "<ul><li>a</li><li><strong>b</strong></li></ul>",
      "1. a\n2. b" to "<ol><li>a</li><li>b</li></ol>",
      "intro\n- a\n\nafter" to "<p>intro</p><ul><li>a</li></ul><p>after</p>",
      "```go\n<b>\n```" to "<pre><code>&lt;b&gt;</code></pre>",
      "```\na\n\nb\n```\nnext" to "<pre><code>a\n\nb</code></pre><p>next</p>",
      "[PR](https://github.com/a/b/pull/1)" to
        "<p><a href=\"https://github.com/a/b/pull/1\" target=\"_blank\" rel=\"noopener noreferrer\">PR</a></p>",
      "see https://x.io/a?b=1&c=2." to
        "<p>see <a href=\"https://x.io/a?b=1&amp;c=2\" target=\"_blank\" rel=\"noopener noreferrer\">https://x.io/a?b=1&amp;c=2</a>.</p>",
      "snake_case_name and a_b" to "<p>snake_case_name and a_b</p>",
      "[x](https://a.io/a b)" to
        "<p><a href=\"https://a.io/a%20b\" target=\"_blank\" rel=\"noopener noreferrer\">x</a></p>",
      "[x](https://例え.jp/x)" to
        "<p><a href=\"https://%E4%BE%8B%E3%81%88.jp/x\" target=\"_blank\" rel=\"noopener noreferrer\">x</a></p>",
      "[x](https://[::1]/x)" to
        "<p><a href=\"https://[::1]/x\" target=\"_blank\" rel=\"noopener noreferrer\">x</a></p>",
      "x****y" to "<p>x****y</p>",
      "~~~~" to "<p>~~~~</p>",
      "``" to "<p>``</p>",
      "`**not bold**`" to "<p><code>**not bold**</code></p>",
      "#not a heading" to "<p>#not a heading</p>",
      "" to ""
    )

    val BLOCKS = listOf(
      "## Sources\ntext" to "<h3>Sources</h3><p>text</p>",
      "# A" to "<h3>A</h3>",
      "### B" to "<h4>B</h4>",
      "###### C" to "<h4>C</h4>",
      "#tag" to "<p>#tag</p>",
      "## **Bold** head" to "<h3><strong>Bold</strong> head</h3>",
      "a\n---\nb" to "<p>a</p><hr><p>b</p>",
      "***" to "<hr>",
      "_ _ _" to "<hr>",
      "--" to "<p>--</p>",
      "> quoted\n> more\n\nafter" to "<blockquote><p>quoted<br>more</p></blockquote><p>after</p>",
      "> - a\n> - b" to "<blockquote><ul><li>a</li><li>b</li></ul></blockquote>",
      "> > deep" to "<blockquote><p>&gt; deep</p></blockquote>",
      "~~gone~~ kept" to "<p><del>gone</del> kept</p>",
      "~~open" to "<p>~~open</p>"
    )

    val TABLES = listOf(
      "| a | b |\n|---|---:|\n| x | 1 |" to
        WRAP + "<thead><tr><th>a</th><th class=\"r\">b</th></tr></thead><tbody><tr><td>x</td><td class=\"r\">1</td></tr></tbody></table></div>",
      "| day | online |\n|---|---|\n| Mon | 314 |\n| Tue | 1.2k–3k |\n| Wed | 12% → 46% |" to
        WRAP + "<thead><tr><th>day</th><th class=\"r\">online</th></tr></thead><tbody>" +
        "<tr><td>Mon</td><td class=\"r\">314</td></tr>" +
        "<tr><td>Tue</td><td class=\"r\">1.2k–3k</td></tr>" +
        "<tr><td>Wed</td><td class=\"r\">12% → 46%</td></tr></tbody></table></div>",
      "| n |\n|---|\n| 12 |\n| n/a |" to
        WRAP + "<thead><tr><th>n</th></tr></thead><tbody><tr><td>12</td></tr><tr><td>n/a</td></tr></tbody></table></div>",
      "| a | b |\n|:---|:-:|\n| 1 | 2 |" to
        WRAP + "<thead><tr><th class=\"l\">a</th><th class=\"c\">b</th></tr></thead><tbody>" +
        "<tr><td class=\"l\">1</td><td class=\"c\">2</td></tr></tbody></table></div>",
      "a | b\n--- | ---\nx | y" to
        WRAP + "<thead><tr><th>a</th><th>b</th></tr></thead><tbody><tr><td>x</td><td>y</td></tr></tbody></table></div>",
      "| a \\| b |\n|---|\n| c |" to
        WRAP + "<thead><tr><th>a | b</th></tr></thead><tbody><tr><td>c</td></tr></tbody></table></div>",
      "| a | b |\n|---|---|\n| x |\n| p | q | r |" to
        WRAP + "<thead><tr><th>a</th><th>b</th></tr></thead><tbody>" +
        "<tr><td>x</td><td></td></tr><tr><td>p</td><td>q</td></tr></tbody></table></div>",
      "| | **28 Sep** |\n|---|---|\n| online | **291** |" to
        WRAP + "<thead><tr><th></th><th class=\"r\"><strong>28 Sep</strong></th></tr></thead><tbody>" +
        "<tr><td>online</td><td class=\"r\"><strong>291</strong></td></tr></tbody></table></div>",
      "intro\n| a |\n|---|\n| b |" to
        "<p>intro</p>" + WRAP + "<thead><tr><th>a</th></tr></thead><tbody><tr><td>b</td></tr></tbody></table></div>",
      "| a |\n|---|\n| b |\nafter" to
        WRAP + "<thead><tr><th>a</th></tr></thead><tbody><tr><td>b</td></tr></tbody></table></div><p>after</p>",
      "a | b\nc | d" to "<p>a | b<br>c | d</p>",
      "| a | b |\n|---|---|---|\n| x | y |" to "<p>| a | b |<br>|---|---|---|<br>| x | y |</p>",
      "```\n| a |\n|---|\n```" to "<pre><code>| a |\n|---|</code></pre>"
    )

    val REFS = listOf(
      "see t12 and d3" to
        "<p>see <a class=\"ref\" href=\"/f1/ref/t12\">t12</a> and <a class=\"ref\" href=\"/f1/ref/d3\">d3</a></p>",
      "`t12`" to "<p><code>t12</code></p>",
      "[x](https://a.io/t12)" to
        "<p><a href=\"https://a.io/t12\" target=\"_blank\" rel=\"noopener noreferrer\">x</a></p>",
      "at12 t12x" to "<p>at12 t12x</p>",
      "r7." to "<p><a class=\"ref\" href=\"/f1/ref/r7\">r7</a>.</p>",
      "**a5** done" to "<p><strong><a class=\"ref\" href=\"/f1/ref/a5\">a5</a></strong> done</p>",
      "- t1\n- t0" to
        "<ul><li><a class=\"ref\" href=\"/f1/ref/t1\">t1</a></li><li>t0</li></ul>",
      "see test_d3.go and t1_done and t3.micro" to "<p>see test_d3.go and t1_done and t3.micro</p>",
      "t3. next" to "<p><a class=\"ref\" href=\"/f1/ref/t3\">t3</a>. next</p>",
      "C:\\x\\t12\\a.md and /x/t3/a.md" to "<p>C:\\x\\t12\\a.md and /x/t3/a.md</p>"
    )

    val NUMBERED = listOf(
      "1. a\n- x\n- y\n\n2. b\n- z" to
        "<ol><li>a</li></ol><ul><li>x</li><li>y</li></ul><ol start=\"2\"><li>b</li></ol><ul><li>z</li></ul>",
      "1. **a**\n   - x\n   - y\n\n2. **b**\n   - z\n\n3. c" to
        "<ol><li><strong>a</strong><ul><li>x</li><li>y</li></ul></li>" +
        "<li><strong>b</strong><ul><li>z</li></ul></li><li>c</li></ol>",
      "1. a\n  - x\n2. b" to "<ol><li>a<ul><li>x</li></ul></li><li>b</li></ol>",
      "1. a\n    note\n2. b" to "<ol><li>a<p>note</p></li><li>b</li></ol>",
      "1. a\n\n2. b" to "<ol><li>a</li><li>b</li></ol>",
      "- a\n\n- b" to "<ul><li>a</li><li>b</li></ul>",
      "3. a\n4. b" to "<ol start=\"3\"><li>a</li><li>b</li></ol>",
      "1. a\n\ntext\n\n2. b" to "<ol><li>a</li></ol><p>text</p><ol start=\"2\"><li>b</li></ol>",
      "1. a\n   1. x\n   2. y\n2. b" to "<ol><li>a<ol><li>x</li><li>y</li></ol></li><li>b</li></ol>",
      "0. a\n1. b" to "<ol start=\"0\"><li>a</li><li>b</li></ol>",
      "2147483647. a" to "<ol start=\"2147483647\"><li>a</li></ol>",
      "2147483648. a" to "<ol><li>a</li></ol>",
      "99999999999999999999. a\n2. b" to "<ol><li>a</li><li>b</li></ol>",
      "1. a\n   ```\n   <x>\n\n   y\n   ```\n2. b" to
        "<ol><li>a<pre><code>&lt;x&gt;\n\ny</code></pre></li><li>b</li></ol>"
    )

    val PATHS = listOf(
      "see /fleet/a.md." to "<p>see " + pathLink("/fleet/a.md", "/fleet/a.md") + ".</p>",
      "(/fleet/a.md), /fleet/a.md: and /fleet/a.md; ok /fleet/a.md!" to
        "<p>(" + pathLink("/fleet/a.md", "/fleet/a.md") + "), " +
        pathLink("/fleet/a.md", "/fleet/a.md") + ": and " +
        pathLink("/fleet/a.md", "/fleet/a.md") + "; ok " +
        pathLink("/fleet/a.md", "/fleet/a.md") + "!</p>",
      "the shots are in /fleet/shots/." to
        "<p>the shots are in " + pathLink("/fleet/shots/", "/fleet/shots/") + ".</p>",
      "/fleet/a.md /fleet/shots/\n/fleet/a.md" to
        "<p>" + pathLink("/fleet/a.md", "/fleet/a.md") + " " +
        pathLink("/fleet/shots/", "/fleet/shots/") + "<br>" +
        pathLink("/fleet/a.md", "/fleet/a.md") + "</p>",
      "`/fleet/a.md`" to "<p>" + pathLink("/fleet/a.md", "<code>/fleet/a.md</code>") + "</p>",
      "**/fleet/a.md** and *see /fleet/a.md*" to
        "<p><strong>" + pathLink("/fleet/a.md", "/fleet/a.md") + "</strong> and <em>see " +
        pathLink("/fleet/a.md", "/fleet/a.md") + "</em></p>",
      "## /fleet/a.md\n- /fleet/a.md\n\n> /fleet/a.md" to
        "<h3>" + pathLink("/fleet/a.md", "/fleet/a.md") + "</h3><ul><li>" +
        pathLink("/fleet/a.md", "/fleet/a.md") + "</li></ul><blockquote><p>" +
        pathLink("/fleet/a.md", "/fleet/a.md") + "</p></blockquote>",
      "| doc |\n|---|\n| /fleet/a.md |" to
        WRAP + "<thead><tr><th>doc</th></tr></thead><tbody><tr><td>" +
        pathLink("/fleet/a.md", "/fleet/a.md") + "</td></tr></tbody></table></div>",
      "```\n/fleet/a.md\n```" to "<pre><code>/fleet/a.md</code></pre>",
      "[/fleet/a.md](https://a.io) [`/fleet/a.md`](https://a.io)" to
        "<p><a href=\"https://a.io\" target=\"_blank\" rel=\"noopener noreferrer\">/fleet/a.md</a> " +
        "<a href=\"https://a.io\" target=\"_blank\" rel=\"noopener noreferrer\"><code>/fleet/a.md</code></a></p>"
    )

    val RELATIVE = listOf(
      "`reports/a.md`" to "<p>" + pathLink("reports/a.md", "<code>reports/a.md</code>") + "</p>",
      "`reports/t1/assets/`" to
        "<p>" + pathLink("reports/t1/assets/", "<code>reports/t1/assets/</code>") + "</p>",
      "`./reports/a.md`" to "<p>" + pathLink("./reports/a.md", "<code>./reports/a.md</code>") + "</p>",
      "see reports/a.md and reports/t1/assets/." to "<p>see reports/a.md and reports/t1/assets/.</p>",
      "```\nreports/a.md\n```" to "<pre><code>reports/a.md</code></pre>"
    )

    val SPACED = listOf(
      "`/fleet/résumé notes.md`" to
        "<p>" + pathLink("/fleet/résumé notes.md", "<code>/fleet/résumé notes.md</code>") + "</p>",
      "see /fleet/naïve-日本.md." to
        "<p>see " + pathLink("/fleet/naïve-日本.md", "/fleet/naïve-日本.md") + ".</p>",
      "`C:\\fleet\\a b.md`" to
        "<p>" + pathLink("C:\\fleet\\a b.md", "<code>C:\\fleet\\a b.md</code>") + "</p>"
    )

    val UNCLOSED = listOf(
      "**a" to "<p>**a</p>",
      "*a" to "<p>*a</p>",
      "`a" to "<p>`a</p>",
      "[a](" to "<p>[a](</p>",
      "[a](https://x.io" to
        "<p>[a](<a href=\"https://x.io\" target=\"_blank\" rel=\"noopener noreferrer\">https://x.io</a></p>",
      "```\nx\ny" to "<pre><code>x\ny</code></pre>"
    )
  }
}
