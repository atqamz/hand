package io.github.atqamz.handcompanion

import java.time.Instant
import java.util.Locale
import java.util.TimeZone
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

private const val ONE = "f00000000e0aa"
private const val TWO = "f000000000005"
private const val HOST = "4d92…6ed6"
private const val DAY = 24 * 60 * 60L

private fun seconds(iso: String) = Instant.parse(iso).epochSecond

private fun stored(id: String, name: String, paired: Long) =
  """{"id":"$id","name":"$name","paired":$paired}"""

private fun answer(vararg fleets: String, id: String = "9ab2…11fe", core: String = "0.1.0") =
  """{"id":"$id","core":"$core","fleets":[${fleets.joinToString(",")}]}"""

private val status =
  checkNotNull(ThisPhoneTest::class.java.classLoader?.getResource("api/status.json")).readText()

private val board = Transport { _, _, _, _, _ -> Raw(200, status) }

private fun state(
  rows: List<FleetRow> = emptyList(),
  phone: PhoneInfo? = null,
  band: Band? = null,
  alertsOff: Boolean = false
) = FleetsState(HOST, rows, band, alertsOff, loaded = true, phone = phone)

class ThisPhoneTest {
  private val zone = TimeZone.getDefault()
  private val locale = Locale.getDefault()

  @Before
  fun fixZoneAndLocale() {
    TimeZone.setDefault(TimeZone.getTimeZone("UTC"))
    Locale.setDefault(Locale.US)
  }

  @After
  fun restoreZoneAndLocale() {
    TimeZone.setDefault(zone)
    Locale.setDefault(locale)
  }

  @Test
  fun theCoreAnswerGivesThePhoneIdTheVersionAndEachPairing() {
    val info = phoneInfo(answer(stored(ONE, "hand", 1_700_000_000), stored(TWO, "", 0)))
    assertEquals("9ab2…11fe", info?.id)
    assertEquals("0.1.0", info?.core)
    assertEquals(
      listOf(PairedFleet(ONE, "hand", 1_700_000_000), PairedFleet(TWO, "", 0)),
      info?.fleets
    )
  }

  @Test
  fun anAnswerThatIsNotTheCoresIsNoInfo() {
    for (text in listOf(null, "", "{}", "garbage", """{"fleets":[]}""")) {
      assertNull(text, phoneInfo(text))
    }
  }

  @Test
  fun aFleetWithoutAnIdIsLeftOutAndAMissingTimeReadsAsNone() {
    val info = phoneInfo("""{"id":"a…b","fleets":[{"name":"x"},5,{"id":"$ONE"}],"extra":1}""")
    assertEquals(listOf(PairedFleet(ONE, "", 0)), info?.fleets)
    assertEquals("", info?.core)
  }

  @Test
  fun aFleetSignsOutNinetyDaysAfterItsPairingDay() {
    assertEquals("paired Nov 3 · signs out Feb 1", pairingDates(seconds("2026-11-03T12:00:00Z")))
    assertEquals("paired Nov 9 · signs out Feb 7", pairingDates(seconds("2026-11-09T00:00:01Z")))
    assertEquals("paired Oct 20 · signs out Jan 18", pairingDates(seconds("2026-10-20T23:59:59Z")))
  }

  @Test
  fun theDaysAreTheDaysOfThePhonesOwnZone() {
    TimeZone.setDefault(TimeZone.getTimeZone("Pacific/Kiritimati"))
    assertEquals("paired Nov 4 · signs out Feb 2", pairingDates(seconds("2026-11-03T23:30:00Z")))
  }

  @Test
  fun theSignOutIsNinetyDaysOfSecondsNotThreeCalendarMonths() {
    val paired = seconds("2026-11-03T12:00:00Z")
    assertEquals(90 * DAY, signOutSeconds(paired) - paired)
    assertEquals(90L, SIGN_OUT_DAYS)
  }

  @Test
  fun aPairingTheStoreHasNoTimeForShowsNoDates() {
    assertEquals("pairing date not recorded", pairingDates(0))
    assertFalse(pairingDates(0).contains("1970"))
  }

  @Test
  fun eachFleetShowsTheDatesTheStoreRecordsAndTheNameTheBoardGives() {
    val model =
      FleetsModel(
        board,
        { listOf(ONE) },
        { HOST },
        phone = { answer(stored(ONE, "stored name", seconds("2026-11-03T12:00:00Z"))) }
      )
    runBlocking { model.refresh() }
    assertEquals(
      listOf(FleetLine(ONE, "golden", "paired Nov 3 · signs out Feb 1")),
      thisPhone(model.state.value, "0.2.0").fleets
    )
  }

  @Test
  fun aFleetTheBoardHasNotAnsweredShowsItsStoredNameThenItsId() {
    val info =
      PhoneInfo("9ab2…11fe", "0.1.0", listOf(PairedFleet(ONE, "hand", 0), PairedFleet(TWO, "", 0)))
    val shown = thisPhone(state(listOf(FleetRow(ONE), FleetRow(TWO)), info), "0.2.0").fleets
    assertEquals(listOf("hand", TWO), shown.map { it.name })
  }

  @Test
  fun theAlertsLineSaysOnOffAndNothingToWatch() {
    val info = PhoneInfo("a…b", "0.1.0", listOf(PairedFleet(ONE, "a", 0), PairedFleet(TWO, "b", 0)))
    val one = PhoneInfo("a…b", "0.1.0", listOf(PairedFleet(ONE, "a", 0)))
    thisPhone(state(phone = info), "0.2.0").let {
      assertEquals("Alerts are on", it.alerts)
      assertEquals("Hand is connected · watching 2 fleets", it.alertsDetail)
    }
    assertEquals(
      "Hand is connected · watching 1 fleet",
      thisPhone(state(phone = one), "0.2.0").alertsDetail
    )
    assertEquals(
      "Hand is not connected · watching 1 fleet",
      thisPhone(state(phone = one, band = band(BoardError.Unreachable, HOST, "12:41")), "0.2.0")
        .alertsDetail
    )
    thisPhone(state(phone = info, alertsOff = true), "0.2.0").let {
      assertEquals("Alerts are off", it.alerts)
      assertEquals("Allow notifications in Settings.", it.alertsDetail)
    }
    thisPhone(state(phone = PhoneInfo("a…b", "0.1.0", emptyList())), "0.2.0").let {
      assertEquals("Alerts are off", it.alerts)
      assertEquals("No fleet is paired.", it.alertsDetail)
    }
  }

  @Test
  fun theHostIsConnectedUnlessTheListShowsABand() {
    assertEquals("connected", thisPhone(state(), "0.2.0").link)
    assertEquals("connected", thisPhone(state(band = UNSAVED), "0.2.0").link)
    assertEquals(
      "not connected",
      thisPhone(state(band = band(BoardError.Unreachable, HOST, "12:41")), "0.2.0").link
    )
  }

  @Test
  fun thePhoneIdAndBothVersionsComeFromTheCoreAndTheApp() {
    val shown = thisPhone(state(phone = PhoneInfo("9ab2…11fe", "0.1.0", emptyList())), "0.2.0")
    assertEquals("9ab2…11fe", shown.phoneId)
    assertEquals("0.2.0", shown.app)
    assertEquals("0.1.0", shown.core)
    assertNull(thisPhone(state(phone = PhoneInfo("a…b", "", emptyList())), "0.2.0").core)
    assertNull(thisPhone(state(), "0.2.0").phoneId)
    assertFalse(thisPhone(FleetsState(), "0.2.0").loaded)
  }

  @Test
  fun theQuestionFollowsTheListsAsk() {
    assertFalse(thisPhone(state(), "0.2.0").asking)
    assertTrue(thisPhone(state().copy(asking = true), "0.2.0").asking)
  }

  @Test
  fun aStateWithNothingPairedEmptiesTheScreen() {
    val shown =
      thisPhone(
        FleetsState(null, emptyList(), loaded = true, phone = PhoneInfo("a…b", "0.1.0", emptyList())),
        "0.2.0"
      )
    assertTrue(shown.fleets.isEmpty())
    assertNull(shown.host)
    assertEquals("No fleet is paired.", shown.alertsDetail)
  }

  @Test
  fun aStateWithFleetsKeepsThemAndTheHost() {
    val info = PhoneInfo("a…b", "0.1.0", listOf(PairedFleet(ONE, "hand", 1)))
    val shown = thisPhone(state(listOf(FleetRow(ONE)), info), "0.2.0")
    assertEquals(listOf(ONE), shown.fleets.map { it.id })
    assertEquals(HOST, shown.host)
  }

  @Test
  fun signOutOpensTheBoardsPhonePageOfThatFleet() {
    assertEquals("/$ONE/connect", signOutPath(ONE))
    assertEquals("/$TWO/connect", signOutPath(TWO))
  }
}
