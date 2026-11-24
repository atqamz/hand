package io.github.atqamz.handcompanion

const val PAIRING_PENDING = "Finish or dismiss the current pairing first"
private const val OPEN_REFUSED = "This is not a link Hand can open."
const val PHONE_LINK = "hand:phone"

sealed interface Landing {
  data class Pairing(val link: String) : Landing

  data class Decision(val fleet: String, val ref: String) : Landing

  data class Fleet(val fleet: String) : Landing

  data class Thread(val fleet: String, val refs: List<String>) : Landing

  data class Web(val fleet: String, val path: String) : Landing

  data class Refused(val message: String) : Landing

  data object Phone : Landing
}

fun Landing.Thread.stack() = refs.fold("", ::pushTask)

fun incomingLink(action: String?, data: String?): String? =
  if (action == THIS_PHONE_ACTION) PHONE_LINK else data

fun landing(link: String, open: (String) -> String?): Landing {
  if (link == PHONE_LINK) return Landing.Phone
  if (link.substringBefore('#') == "handboard://c") return Landing.Pairing(link)
  val answer = parse(open(link))
  val path = answer.field("path") ?: return Landing.Refused(answer.field("message") ?: OPEN_REFUSED)
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
    else -> Landing.Web(fleet, path)
  }
}
