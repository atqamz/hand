package io.github.atqamz.handcompanion

const val PAIRING_PENDING = "Finish or dismiss the current pairing first"
private const val OPEN_REFUSED = "This is not a link Hand can open."
const val PHONE_LINK = "hand:phone"
private val OPEN_PAGE = Regex("handboard://open/f[0-9a-f]{12}/([a-z][a-z0-9-]*)(?:/.*)?")
private val SHOWN_PAGES = setOf("task", "decision", "attempt")

fun notShown(what: String) = "This app does not show $what."

sealed interface Landing {
  data class Pairing(val link: String) : Landing

  data class Decision(val fleet: String, val ref: String) : Landing

  data class Fleet(val fleet: String) : Landing

  data class Thread(val fleet: String, val refs: List<String>) : Landing

  data class Unshown(val fleet: String, val what: String) : Landing

  data class Refused(val message: String) : Landing

  data object Phone : Landing
}

fun Landing.Thread.stack() = refs.fold("", ::pushTask)

fun incomingLink(action: String?, data: String?): String? =
  if (action == THIS_PHONE_ACTION) PHONE_LINK else data

private fun refusal(link: String, message: String?): String {
  val page = OPEN_PAGE.matchEntire(link)?.groupValues?.get(1)
  return if (page != null && page !in SHOWN_PAGES && (message == null || message == OPEN_REFUSED)) {
    notShown("the page \"$page\"")
  } else {
    message ?: OPEN_REFUSED
  }
}

fun landing(link: String, open: (String) -> String?): Landing {
  if (link == PHONE_LINK) return Landing.Phone
  if (link.substringBefore('#') == "handboard://c") return Landing.Pairing(link)
  val answer = parse(open(link))
  val path = answer.field("path") ?: return Landing.Refused(refusal(link, answer.field("message")))
  if ('\n' in path) return Landing.Refused(OPEN_REFUSED)
  val fleet = path.removePrefix("/").substringBefore('/')
  val page = path.removePrefix("/$fleet/")
  return when {
    page.isEmpty() -> Landing.Fleet(fleet)
    page.startsWith("decision/") -> Landing.Decision(fleet, page.removePrefix("decision/"))
    page.startsWith("task/") -> {
      val task = page.removePrefix("task/").substringBefore('#')
      val report = page.substringAfter('#', "")
      Landing.Thread(fleet, listOf(task) + listOfNotNull(report.takeIf(String::isNotEmpty)))
    }
    page.startsWith("attempt/") -> Landing.Thread(fleet, listOf(page.removePrefix("attempt/")))
    else -> Landing.Unshown(fleet, "the page \"${page.substringBefore('#')}\"")
  }
}
