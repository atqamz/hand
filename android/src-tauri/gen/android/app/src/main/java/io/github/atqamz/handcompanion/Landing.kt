package io.github.atqamz.handcompanion

const val PAIRING_PENDING = "Finish or dismiss the current pairing first"
private const val OPEN_REFUSED = "This is not a link Hand can open."

sealed interface Landing {
  data class Pairing(val link: String) : Landing

  data class Decision(val fleet: String, val ref: String) : Landing

  data class Fleet(val fleet: String) : Landing

  data class Web(val fleet: String, val path: String) : Landing

  data class Refused(val message: String) : Landing
}

fun landing(link: String, open: (String) -> String?): Landing {
  if (link.substringBefore('#') == "handboard://c") return Landing.Pairing(link)
  val answer = parse(open(link))
  val path = answer.field("path") ?: return Landing.Refused(answer.field("message") ?: OPEN_REFUSED)
  val fleet = path.removePrefix("/").substringBefore('/')
  val page = path.removePrefix("/$fleet/")
  return when {
    page.isEmpty() -> Landing.Fleet(fleet)
    page.startsWith("decision/") -> Landing.Decision(fleet, page.removePrefix("decision/"))
    else -> Landing.Web(fleet, path)
  }
}
