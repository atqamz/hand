package io.github.atqamz.handcompanion

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

private val desktopQueries =
  """
    0 1 tasks/search
    0 3 tasks/search?page=3
    1 1 tasks/search?q=fix%3A+a%26b%3Dc
    1 3 tasks/search?page=3&q=fix%3A+a%26b%3Dc
    2 1 tasks/search?status=inbox
    2 3 tasks/search?page=3&status=inbox
    3 1 tasks/search?q=fix%3A+a%26b%3Dc&status=inbox
    3 3 tasks/search?page=3&q=fix%3A+a%26b%3Dc&status=inbox
    4 1 tasks/search?project=my+proj%2F%C3%A9
    4 3 tasks/search?page=3&project=my+proj%2F%C3%A9
    5 1 tasks/search?project=my+proj%2F%C3%A9&q=fix%3A+a%26b%3Dc
    5 3 tasks/search?page=3&project=my+proj%2F%C3%A9&q=fix%3A+a%26b%3Dc
    6 1 tasks/search?project=my+proj%2F%C3%A9&status=inbox
    6 3 tasks/search?page=3&project=my+proj%2F%C3%A9&status=inbox
    7 1 tasks/search?project=my+proj%2F%C3%A9&q=fix%3A+a%26b%3Dc&status=inbox
    7 3 tasks/search?page=3&project=my+proj%2F%C3%A9&q=fix%3A+a%26b%3Dc&status=inbox
    8 1 tasks/search?since=2026-11-01
    8 3 tasks/search?page=3&since=2026-11-01
    9 1 tasks/search?q=fix%3A+a%26b%3Dc&since=2026-11-01
    9 3 tasks/search?page=3&q=fix%3A+a%26b%3Dc&since=2026-11-01
    10 1 tasks/search?since=2026-11-01&status=inbox
    10 3 tasks/search?page=3&since=2026-11-01&status=inbox
    11 1 tasks/search?q=fix%3A+a%26b%3Dc&since=2026-11-01&status=inbox
    11 3 tasks/search?page=3&q=fix%3A+a%26b%3Dc&since=2026-11-01&status=inbox
    12 1 tasks/search?project=my+proj%2F%C3%A9&since=2026-11-01
    12 3 tasks/search?page=3&project=my+proj%2F%C3%A9&since=2026-11-01
    13 1 tasks/search?project=my+proj%2F%C3%A9&q=fix%3A+a%26b%3Dc&since=2026-11-01
    13 3 tasks/search?page=3&project=my+proj%2F%C3%A9&q=fix%3A+a%26b%3Dc&since=2026-11-01
    14 1 tasks/search?project=my+proj%2F%C3%A9&since=2026-11-01&status=inbox
    14 3 tasks/search?page=3&project=my+proj%2F%C3%A9&since=2026-11-01&status=inbox
    15 1 tasks/search?project=my+proj%2F%C3%A9&q=fix%3A+a%26b%3Dc&since=2026-11-01&status=inbox
    15 3 tasks/search?page=3&project=my+proj%2F%C3%A9&q=fix%3A+a%26b%3Dc&since=2026-11-01&status=inbox
    16 1 tasks/search?until=2026-11-24
    16 3 tasks/search?page=3&until=2026-11-24
    17 1 tasks/search?q=fix%3A+a%26b%3Dc&until=2026-11-24
    17 3 tasks/search?page=3&q=fix%3A+a%26b%3Dc&until=2026-11-24
    18 1 tasks/search?status=inbox&until=2026-11-24
    18 3 tasks/search?page=3&status=inbox&until=2026-11-24
    19 1 tasks/search?q=fix%3A+a%26b%3Dc&status=inbox&until=2026-11-24
    19 3 tasks/search?page=3&q=fix%3A+a%26b%3Dc&status=inbox&until=2026-11-24
    20 1 tasks/search?project=my+proj%2F%C3%A9&until=2026-11-24
    20 3 tasks/search?page=3&project=my+proj%2F%C3%A9&until=2026-11-24
    21 1 tasks/search?project=my+proj%2F%C3%A9&q=fix%3A+a%26b%3Dc&until=2026-11-24
    21 3 tasks/search?page=3&project=my+proj%2F%C3%A9&q=fix%3A+a%26b%3Dc&until=2026-11-24
    22 1 tasks/search?project=my+proj%2F%C3%A9&status=inbox&until=2026-11-24
    22 3 tasks/search?page=3&project=my+proj%2F%C3%A9&status=inbox&until=2026-11-24
    23 1 tasks/search?project=my+proj%2F%C3%A9&q=fix%3A+a%26b%3Dc&status=inbox&until=2026-11-24
    23 3 tasks/search?page=3&project=my+proj%2F%C3%A9&q=fix%3A+a%26b%3Dc&status=inbox&until=2026-11-24
    24 1 tasks/search?since=2026-11-01&until=2026-11-24
    24 3 tasks/search?page=3&since=2026-11-01&until=2026-11-24
    25 1 tasks/search?q=fix%3A+a%26b%3Dc&since=2026-11-01&until=2026-11-24
    25 3 tasks/search?page=3&q=fix%3A+a%26b%3Dc&since=2026-11-01&until=2026-11-24
    26 1 tasks/search?since=2026-11-01&status=inbox&until=2026-11-24
    26 3 tasks/search?page=3&since=2026-11-01&status=inbox&until=2026-11-24
    27 1 tasks/search?q=fix%3A+a%26b%3Dc&since=2026-11-01&status=inbox&until=2026-11-24
    27 3 tasks/search?page=3&q=fix%3A+a%26b%3Dc&since=2026-11-01&status=inbox&until=2026-11-24
    28 1 tasks/search?project=my+proj%2F%C3%A9&since=2026-11-01&until=2026-11-24
    28 3 tasks/search?page=3&project=my+proj%2F%C3%A9&since=2026-11-01&until=2026-11-24
    29 1 tasks/search?project=my+proj%2F%C3%A9&q=fix%3A+a%26b%3Dc&since=2026-11-01&until=2026-11-24
    29 3 tasks/search?page=3&project=my+proj%2F%C3%A9&q=fix%3A+a%26b%3Dc&since=2026-11-01&until=2026-11-24
    30 1 tasks/search?project=my+proj%2F%C3%A9&since=2026-11-01&status=inbox&until=2026-11-24
    30 3 tasks/search?page=3&project=my+proj%2F%C3%A9&since=2026-11-01&status=inbox&until=2026-11-24
    31 1 tasks/search?project=my+proj%2F%C3%A9&q=fix%3A+a%26b%3Dc&since=2026-11-01&status=inbox&until=2026-11-24
    31 3 tasks/search?page=3&project=my+proj%2F%C3%A9&q=fix%3A+a%26b%3Dc&since=2026-11-01&status=inbox&until=2026-11-24
  """
    .trimIndent()
    .lines()

private val values = listOf("fix: a&b=c", "inbox", "my proj/é", "2026-11-01", "2026-11-24")

private fun formFor(mask: Int): TaskSearchForm {
  fun at(i: Int) = if (mask shr i and 1 == 1) values[i] else ""
  return TaskSearchForm(q = at(0), status = at(1), project = at(2), since = at(3), until = at(4))
}

private val escapes =
  listOf(
    "a b" to "tasks/search?q=a+b",
    "100%" to "tasks/search?q=100%25",
    "x+y" to "tasks/search?q=x%2By",
    "~tilde*star" to "tasks/search?q=~tilde%2Astar",
    "(parens)!'" to "tasks/search?q=%28parens%29%21%27",
    "日本語" to "tasks/search?q=%E6%97%A5%E6%9C%AC%E8%AA%9E",
    "😀" to "tasks/search?q=%F0%9F%98%80",
    "a/b?c#d" to "tasks/search?q=a%2Fb%3Fc%23d",
    "semi;colon" to "tasks/search?q=semi%3Bcolon",
    "tab\there" to "tasks/search?q=tab%09here",
    "quote\"s" to "tasks/search?q=quote%22s",
    "back\\slash" to "tasks/search?q=back%5Cslash",
    "a=b&c=d" to "tasks/search?q=a%3Db%26c%3Dd",
  )

class TaskSearchQueryTest {
  @Test
  fun everyFilterCombinationMatchesDesktopsQueryString() {
    assertEquals(64, desktopQueries.size)
    for (line in desktopQueries) {
      val (mask, page, expected) = line.split(" ", limit = 3)
      assertEquals(line, expected, taskSearchQuery(formFor(mask.toInt()), page.toInt()).path())
    }
  }

  @Test
  fun valuesThatNeedEscapingMatchDesktops() {
    for ((value, expected) in escapes) {
      assertEquals(value, expected, taskSearchQuery(TaskSearchForm(q = value)).path())
    }
  }

  @Test
  fun textAndDatesAreTrimmedAndStatusAndProjectAreNot() {
    val query = taskSearchQuery(TaskSearchForm(" a b ", " inbox", " p ", " 2026-11-01\n", "\u00a02026-11-24 "))
    assertEquals(TaskSearchQuery("a b", " inbox", " p ", "2026-11-01", "2026-11-24"), query)
    assertEquals("tasks/search?project=+p+&q=a+b&since=2026-11-01&status=+inbox&until=2026-11-24", query.path())
  }

  @Test
  fun aPageBelowTwoIsLeftOutAndNeverBelowOne() {
    assertEquals(1, taskSearchQuery(TaskSearchForm(), 0).page)
    assertEquals(1, taskSearchQuery(TaskSearchForm(), -4).page)
    assertEquals("tasks/search", taskSearchQuery(TaskSearchForm(), 1).path())
    assertEquals("tasks/search?page=2", taskSearchQuery(TaskSearchForm(), 2).path())
  }

  @Test
  fun aBlankFormIsBlankAndAPageAloneIsNot() {
    assertTrue(taskSearchQuery(TaskSearchForm(" ", "", "", "  ", "")).blank)
    assertFalse(taskSearchQuery(TaskSearchForm(status = "finished")).blank)
  }
}
