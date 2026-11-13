package io.github.atqamz.handcompanion

import java.io.File
import javax.xml.parsers.DocumentBuilderFactory
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.w3c.dom.Element

private const val ANDROID = "http://schemas.android.com/apk/res/android"

private fun Element.named(name: String) = getAttributeNS(ANDROID, "name") == name

private fun Element.children(tag: String) =
  (0 until getElementsByTagName(tag).length).map { getElementsByTagName(tag).item(it) as Element }

private fun filterWith(activity: Element, vararg wanted: String) =
  activity.children("intent-filter").any { filter ->
    val names = (filter.children("action") + filter.children("category")).map { it.getAttributeNS(ANDROID, "name") }
    names.containsAll(wanted.toList())
  }

private fun hosts(activity: Element) =
  activity.children("data").filter { it.getAttributeNS(ANDROID, "scheme") == "handboard" }
    .map { it.getAttributeNS(ANDROID, "host") }

class ManifestTest {
  private val root =
    DocumentBuilderFactory.newInstance()
      .apply { isNamespaceAware = true }
      .newDocumentBuilder()
      .parse(File("src/main/AndroidManifest.xml"))
      .documentElement

  private val activities = root.children("activity")

  @Test
  fun handActivityIsTheLauncherAndTakesBothLinkHosts() {
    val hand = activities.single { it.named(".HandActivity") }
    assertTrue(filterWith(hand, "android.intent.action.MAIN", "android.intent.category.LAUNCHER"))
    assertEquals(listOf("c", "open"), hosts(hand).sorted())
    assertEquals("true", hand.getAttributeNS(ANDROID, "exported"))
    assertEquals("singleTask", hand.getAttributeNS(ANDROID, "launchMode"))
  }

  @Test
  fun handActivityIsTheOnlyActivity() {
    assertEquals(listOf(".HandActivity"), activities.map { it.getAttributeNS(ANDROID, "name") })
    assertTrue(root.children("activity-alias").isEmpty())
    assertEquals(1, activities.count { filterWith(it, "android.intent.category.LAUNCHER") })
  }

  @Test
  fun noBuildTypeAddsItsOwnManifest() {
    assertFalse(File("src/debug/AndroidManifest.xml").exists())
  }
}
