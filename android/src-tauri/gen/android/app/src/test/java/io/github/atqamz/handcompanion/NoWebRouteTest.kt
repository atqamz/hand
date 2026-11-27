package io.github.atqamz.handcompanion

import android.view.View
import androidx.compose.ui.platform.LocalView
import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import java.io.File
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

private const val FLEET = "f0123456789ab"
private val VECTORS = Json.parseToJsonElement(File("../../../../../docs/companion-vectors.json").readText()).jsonObject
private val SOURCES = File("src/main/java/io/github/atqamz/handcompanion")
private val FALLBACK_FILES = setOf("Fallback.kt", "FallbackScreen.kt")

private fun JsonObject.text(key: String) = getValue(key).jsonPrimitive.content

private fun JsonObject.list(key: String): JsonArray = getValue(key).jsonArray

private fun native(landing: Landing) =
  landing is Landing.Fleet || landing is Landing.Decision || landing is Landing.Thread

private fun alertLink(kind: String) =
  "handboard://open/$FLEET/" +
    when {
      kind == "decision.asked" -> "decision/d3"
      kind.startsWith("attempt.") -> "attempt/a7"
      else -> ""
    }

private fun answering(path: String) = { _: String -> """{"path":"$path"}""" }

class OpenVectorRouteTest {
  @Test
  fun everyValidOpenVectorLandsOnItsNativeScreen() {
    val valid = VECTORS.getValue("open").jsonObject.list("valid")
    assertTrue(valid.size >= 6)
    for (item in valid) {
      val vector = item.jsonObject
      val parsed = vector.getValue("parsed").jsonObject
      val page = parsed.text("page")
      val report = parsed["report"]?.jsonPrimitive?.content?.takeIf { it != "null" }
      val landed = landing(vector.text("link"), answering(parsed.text("path")))
      val want =
        when {
          page.isEmpty() -> Landing.Fleet(FLEET)
          page.startsWith("decision/") -> Landing.Decision(FLEET, page.removePrefix("decision/"))
          else -> Landing.Thread(FLEET, listOf(page.substringAfter('/')) + listOfNotNull(report))
        }
      assertEquals(vector.text("name"), want, landed)
      assertTrue(vector.text("name"), native(landed))
    }
  }

  @Test
  fun everyInvalidOpenVectorIsRefusedAndNeverOpensAPage() {
    val invalid = VECTORS.getValue("open").jsonObject.list("invalid")
    assertTrue(invalid.size >= 60)
    for (item in invalid) {
      val link = item.jsonObject.text("link")
      if (link.substringBefore('#') == "handboard://c") continue
      assertTrue(link, landing(link) { """{"message":"no"}""" } is Landing.Refused)
    }
  }

  @Test
  fun aPageWithNoNativeScreenSaysThisAppDoesNotShowItAndNamesIt() {
    val pages = listOf("connect", "screen", "tasks", "history", "alerts", "doc/reports/t3/notes.md", "future/x9")
    for (page in pages) {
      val landed = landing("handboard://open/$FLEET/$page", answering("/$FLEET/$page"))
      assertEquals(page, Landing.Unshown(FLEET, "the page \"$page\""), landed)
      assertFalse(page, native(landed))
      val sentence = notShown((landed as Landing.Unshown).what)
      assertTrue(sentence, sentence.startsWith("This app does not show "))
      assertTrue(sentence, page in sentence)
    }
  }

  @Test
  fun aPageWithAReportAnchorStillNamesOnlyThePage() {
    val landed = landing("x", answering("/$FLEET/connect#r5"))
    assertEquals(Landing.Unshown(FLEET, "the page \"connect\""), landed)
  }
}

class AlertRouteTest {
  private val kinds = VECTORS.getValue("alerts").jsonObject.list("kinds").map { it.jsonPrimitive.content }

  private fun tap(kind: String, link: String?) =
    tapOf(alertSpec(FLEET, "hand", kind, "x1", "text", link)).let { incomingLink(it.action, it.uri) }

  @Test
  fun everyAlertKindTheFeedCanCarryLandsOnANativeScreen() {
    assertEquals(12, kinds.size)
    for (kind in kinds) {
      val link = alertLink(kind)
      val taken = tap(kind, link)
      assertEquals(kind, link, taken)
      val landed = landing(taken.orEmpty(), answering("/" + link.removePrefix("handboard://open/")))
      assertTrue("$kind $landed", native(landed))
    }
  }

  @Test
  fun everyAlertTheVectorsCarryLandsOnANativeScreen() {
    val exchanges = VECTORS.getValue("alerts").jsonObject.list("exchanges")
    var seen = 0
    for (exchange in exchanges) {
      val given = exchange.jsonObject["given"] as? JsonArray ?: continue
      for (alert in given) {
        val link = alert.jsonObject["link"]?.jsonPrimitive?.content ?: continue
        if (link.isEmpty()) continue
        seen++
        val landed = landing(link, answering("/" + link.removePrefix("handboard://open/")))
        assertTrue(link, native(landed))
      }
    }
    assertTrue(seen > 0)
  }

  @Test
  fun anAlertWithNoLinkOpensNoScreenAndNeverAWebPage() {
    for (kind in kinds) {
      assertNull(kind, tap(kind, null))
      assertNull(kind, tap(kind, ""))
      assertNull(kind, tap(kind, "https://example.test/$FLEET/task/t3"))
    }
    assertNull(incomingLink(tapOf(unpairedSpec(FLEET, "hand")).action, tapOf(unpairedSpec(FLEET, "hand")).uri))
    assertEquals(Landing.Phone, landing(incomingLink(tapOf(null).action, tapOf(null).uri).orEmpty()) { null })
  }
}

class DeliveredRouteTest {
  @Test
  fun everyDeliveredKindIsADocAnExternalLinkOrNothing() {
    val cases =
      listOf(
        TaskDeliverable("a.md", "file", "/doc/a.md") to ThreadTarget.Doc("a.md"),
        TaskDeliverable("d", "file", "/doc/reports/t1/") to ThreadTarget.Doc("reports/t1"),
        TaskDeliverable("a.md", "file") to ThreadTarget.None,
        TaskDeliverable("https://github.com/o/r/pull/1", "PR") to ThreadTarget.Browser("https://github.com/o/r/pull/1"),
        TaskDeliverable("https://example.test/x", "link") to ThreadTarget.Browser("https://example.test/x"),
        TaskDeliverable("mailto:a@example.test", "link") to ThreadTarget.Browser("mailto:a@example.test"),
        TaskDeliverable("", "PR") to ThreadTarget.None,
        TaskDeliverable("r5", "report") to ThreadTarget.None
      )
    for ((deliverable, want) in cases) assertEquals(deliverable.toString(), want, deliverableTarget(deliverable))
  }

  @Test
  fun aBoardAddressNeverGoesToTheBrowser() {
    val urls =
      listOf(
        "http://localhost:50123/$FLEET/task/t3",
        "http://LOCALHOST/$FLEET/",
        "http://127.0.0.1:8080/$FLEET/doc/a.md",
        "http://127.1.2.3/",
        "http://[::1]:8080/$FLEET/",
        "file:///etc/passwd",
        "javascript:alert(1)",
        "handboard://open/$FLEET/task/t3",
        "/$FLEET/task/t3",
        "not a url"
      )
    for (url in urls) {
      assertEquals(url, ThreadTarget.None, externalTarget(url))
      assertEquals(url, ThreadTarget.None, deliverableTarget(TaskDeliverable(url, "link")))
    }
    assertEquals(ThreadTarget.Browser("https://example.test/a"), externalTarget("https://example.test/a"))
  }
}

class QueueRouteTest {
  private fun need(kind: String, ref: String? = "x1", keys: Boolean = false) =
    Need(kind, ref, "title", kind.uppercase(), "neutral", keys = if (keys) NeedKeys(emptyList(), 1, "d", 1) else null)

  private fun opensOf(need: Need) = bands(listOf(need)).single().entries.single().opens

  @Test
  fun everyQueueKindOpensANativeScreenOrTheNotice() {
    val native = setOf(Opens.Card, Opens.Choice, Opens.Row, Opens.Report)
    val kinds =
      listOf("blocked", "worker", "decision", "failure", "report", "limited", "resume", "stalled", "nosup", "quiet", "long", "kept", "artifacts")
    for (kind in kinds) {
      val opens = opensOf(need(kind))
      assertTrue("$kind $opens", opens in native || opens == Opens.Unshown)
    }
    assertEquals(Opens.Unshown, opensOf(need("a-future-kind")))
    assertEquals(Opens.Unshown, opensOf(need("worker")))
  }

  @Test
  fun theNoticeNamesTheItemThatWasTapped() {
    assertEquals(
      "This app does not show the limited item s2.",
      notShown(bands(listOf(need("limited", "s2"))).single().entries.single().unshown())
    )
    assertEquals(
      "This app does not show the a-future-kind item.",
      notShown(bands(listOf(need("a-future-kind", null))).single().entries.single().unshown())
    )
  }
}

class MenuRouteTest {
  private val frames = Frames()

  @get:Rule
  val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_5.copy(screenHeight = 2200), snapshotHandler = frames)

  private fun labels(content: @androidx.compose.runtime.Composable () -> Unit): List<String> {
    var found = emptyList<String>()
    lateinit var view: View
    frames.onFrame = { found = Taps(view).labels() }
    paparazzi.snapshot {
      HandTheme(false) {
        view = LocalView.current
        content()
      }
    }
    return found
  }

  @Test
  fun theFleetMenuHoldsAlertsAndSkillsAndNoWebBoard() {
    assertEquals(listOf("Alerts", "Skills"), fleetMenu({}, {}).map { it.first })
  }

  @Test
  fun taskReportAndAttemptScreensHaveNoMenuLeftToOpenTheWeb() {
    val shown =
      listOf(
        labels { TaskScreen(FLEET, "t3", TaskState(), {}, {}, {}, {}) },
        labels { ReportScreen(FLEET, "r5", ReportState(), {}, {}, {}, {}) },
        labels { AttemptScreen(FLEET, "a7", AttemptState(), {}, {}, {}, {}, {}) }
      )
    for (names in shown) {
      assertTrue("Back" in names)
      assertFalse(names.toString(), "Menu" in names)
      assertTrue(names.toString(), names.none { "web" in it.lowercase() })
    }
  }
}

class NoWebSourceTest {
  private val files = SOURCES.listFiles { file -> file.extension == "kt" }.orEmpty().sortedBy { it.name }

  private fun outsideTheFallback() = files.filter { it.name !in FALLBACK_FILES }

  @Test
  fun noScreenOrRouteReachesTheWebFallback() {
    assertTrue(files.size > 50)
    val banned =
      listOf("WebBoard(", "FallbackScreen(", "openBoard(", "Landing.Web", "Opens.Web", "ThreadTarget.Web", "onWeb", "web = ", "Core.fallback(")
    for (file in outsideTheFallback()) {
      val text = file.readText()
      for (word in banned) {
        if (word == "Core.fallback(" && file.name == "DocVideo.kt") continue
        assertFalse("${file.name} holds $word", word in text)
      }
    }
  }

  @Test
  fun noMenuItemOrButtonOpensTheWebBoard() {
    for (file in outsideTheFallback()) {
      val text = file.readText()
      assertFalse("${file.name} Open web Board", "Open web Board" in text)
      assertFalse("${file.name} web Board has it", "The web Board has it" in text)
    }
  }

  @Test
  fun aBrowserIntentGoesOnlyThroughTheExternalTargetInTheActivity() {
    val holders = outsideTheFallback().filter { "ACTION_VIEW" in it.readText() }.map { it.name }
    assertEquals(listOf("AlertNotice.kt", "HandActivity.kt"), holders)
    assertTrue(File(SOURCES, "AlertNotice.kt").readText().contains("OPEN_PREFIX = \"handboard://open/\""))
    assertTrue(File(SOURCES, "HandActivity.kt").readText().contains("is ThreadTarget.Browser ->"))
  }

  @Test
  fun theFallbackScreenIsKeptAndNothingReachesIt() {
    assertTrue(File(SOURCES, "FallbackScreen.kt").readText().contains("fun WebBoard("))
    for (file in outsideTheFallback()) assertFalse(file.name, "WebBoard" in file.readText())
  }
}
