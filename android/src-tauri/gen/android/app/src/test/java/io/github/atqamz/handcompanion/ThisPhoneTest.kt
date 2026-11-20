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
private const val ENDPOINT = "4d92000000000000000000000000000000000000000000000000000000006ed6"
private const val NAME = "atqa-laptop"
private const val DAY = 24 * 60 * 60L

private fun seconds(iso: String) = Instant.parse(iso).epochSecond

private fun stored(id: String, name: String, paired: Long) =
  """{"id":"$id","name":"$name","paired":$paired}"""

private fun host(vararg fleets: String, name: String = NAME) =
  """{"endpoint":"$ENDPOINT","id":"$HOST","name":"$name","fleets":[${fleets.joinToString(",")}]}"""

private fun answer(vararg hosts: String, id: String = "9ab2…11fe", core: String = "0.1.0") =
  """{"id":"$id","core":"$core","hosts":[${hosts.joinToString(",")}]}"""

private fun held(vararg fleets: PairedFleet, id: String = "a…b", core: String = "0.1.0") =
  PhoneInfo(id, core, listOf(PairedHost(ENDPOINT, HOST, NAME, fleets.toList())))

private val status =
  checkNotNull(ThisPhoneTest::class.java.classLoader?.getResource("api/status.json")).readText()

private val board = Transport { _, _, _, _, _ -> Raw(200, status) }

private fun state(
  rows: List<FleetRow> = emptyList(),
  phone: PhoneInfo? = null,
  band: Band? = null,
  alertsOff: Boolean = false
) =
  FleetsState(
    listOf(HostGroup(ENDPOINT, HOST, NAME, rows, band.takeIf { it != UNSAVED })),
    band.takeIf { it == UNSAVED },
    alertsOff,
    loaded = true,
    phone = phone
  )

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
    val info = phoneInfo(answer(host(stored(ONE, "hand", 1_700_000_000), stored(TWO, "", 0))))
    assertEquals("9ab2…11fe", info?.id)
    assertEquals("0.1.0", info?.core)
    assertEquals(
      listOf(PairedFleet(ONE, "hand", 1_700_000_000), PairedFleet(TWO, "", 0)),
      info?.fleets
    )
  }

  @Test
  fun anAnswerThatIsNotTheCoresIsNoInfo() {
    for (text in listOf(null, "", "{}", "garbage", """{"hosts":[]}""")) {
      assertNull(text, phoneInfo(text))
    }
  }

  @Test
  fun aFleetWithoutAnIdIsLeftOutAndAMissingTimeReadsAsNone() {
    val info = phoneInfo("""{"id":"a…b","hosts":[${host("""{"name":"x"}""", "5", """{"id":"$ONE"}""")}],"extra":1}""")
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
        { phoneInfo(answer(host(stored(ONE, "stored name", seconds("2026-11-03T12:00:00Z"))))) }
      )
    runBlocking { model.refresh() }
    assertEquals(
      listOf(FleetLine(ONE, "golden", "paired Nov 3 · signs out Feb 1")),
      thisPhone(model.state.value, "0.2.0").fleets
    )
  }

  @Test
  fun aFleetTheBoardHasNotAnsweredShowsItsStoredNameThenItsId() {
    val info = held(PairedFleet(ONE, "hand", 0), PairedFleet(TWO, "", 0))
    val shown = thisPhone(state(listOf(FleetRow(ONE), FleetRow(TWO)), info), "0.2.0").fleets
    assertEquals(listOf("hand", TWO), shown.map { it.name })
  }

  @Test
  fun theAlertsLineSaysOnOffAndNothingToWatch() {
    val info = held(PairedFleet(ONE, "a", 0), PairedFleet(TWO, "b", 0))
    val one = held(PairedFleet(ONE, "a", 0))
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
    thisPhone(state(phone = held()), "0.2.0").let {
      assertEquals("Alerts are off", it.alerts)
      assertEquals("No fleet is paired.", it.alertsDetail)
    }
  }

  @Test
  fun theHostIsConnectedUnlessTheListShowsABand() {
    val info = held(PairedFleet(ONE, "hand", 1))
    fun link(band: Band?) = thisPhone(state(phone = info, band = band), "0.2.0").hosts.single().link
    assertEquals("connected", link(null))
    assertEquals("connected", link(UNSAVED))
    assertEquals("not connected", link(band(BoardError.Unreachable, HOST, "12:41")))
  }

  @Test
  fun thePhoneIdAndBothVersionsComeFromTheCoreAndTheApp() {
    val shown = thisPhone(state(phone = held(id = "9ab2…11fe")), "0.2.0")
    assertEquals("9ab2…11fe", shown.phoneId)
    assertEquals("0.2.0", shown.app)
    assertEquals("0.1.0", shown.core)
    assertNull(thisPhone(state(phone = held(core = "")), "0.2.0").core)
    assertNull(thisPhone(state(), "0.2.0").phoneId)
    assertFalse(thisPhone(FleetsState(), "0.2.0").loaded)
  }

  @Test
  fun theQuestionFollowsTheListsAsk() {
    val ask = Forgetting(ENDPOINT, NAME, 1)
    assertNull(thisPhone(state(), "0.2.0").asking)
    assertEquals(ask, thisPhone(state().copy(asking = ask), "0.2.0").asking)
  }

  @Test
  fun aStateWithNothingPairedEmptiesTheScreen() {
    val shown =
      thisPhone(
        FleetsState(loaded = true, phone = held()),
        "0.2.0"
      )
    assertTrue(shown.fleets.isEmpty())
    assertEquals("No fleet is paired.", shown.alertsDetail)
  }

  @Test
  fun aStateWithFleetsKeepsThemAndTheHost() {
    val info = held(PairedFleet(ONE, "hand", 1))
    val shown = thisPhone(state(listOf(FleetRow(ONE)), info), "0.2.0")
    assertEquals(listOf(ONE), shown.fleets.map { it.id })
    assertEquals(listOf(HostLine(ENDPOINT, NAME, HOST, "connected", 1)), shown.hosts)
  }

  private val twoHosts =
    PhoneInfo(
      "a…b",
      "0.1.0",
      listOf(
        PairedHost(ENDPOINT, HOST, "atqa-laptop", listOf(PairedFleet(ONE, "hand", 1))),
        PairedHost("e2", "7c1e…a2f9", "vps-berlin", listOf(PairedFleet(TWO, "ops", 1)))
      )
    )

  @Test
  fun theCoreAnswerListsEachHostWithItsFleetsAndAnUnnamedHostShowsItsShortId() {
    val text =
      answer(
        host(stored(ONE, "hand", 1), name = "atqa-laptop"),
        host(stored(TWO, "ops", 1), name = ""),
        """{"endpoint":"e3","id":"aaaa…bbbb","fleets":[]}""",
        """{"id":"no-endpoint"}""",
        "5"
      )
    val info = phoneInfo(text)!!
    assertEquals(listOf("atqa-laptop", HOST, "aaaa…bbbb"), info.hosts.map { it.name })
    assertEquals(listOf(ONE, TWO), info.fleets.map { it.id })
    assertEquals(emptyList<PairedFleet>(), info.hosts.last().fleets)
  }

  @Test
  fun eachHostLineShowsNameShortIdFleetCountAndItsOwnLink() {
    val state =
      FleetsState(
        listOf(
          HostGroup(ENDPOINT, HOST, "atqa-laptop", listOf(FleetRow(ONE))),
          HostGroup("e2", "7c1e…a2f9", "vps-berlin", listOf(FleetRow(TWO)), band(BoardError.Unreachable, "vps-berlin", "09:12"))
        ),
        loaded = true,
        phone = twoHosts
      )
    val shown = thisPhone(state, "0.2.0")
    assertEquals(
      listOf(
        HostLine(ENDPOINT, "atqa-laptop", HOST, "connected", 1),
        HostLine("e2", "vps-berlin", "7c1e…a2f9", "not connected", 1)
      ),
      shown.hosts
    )
    assertEquals("4d92…6ed6 · 1 fleet · connected", shown.hosts[0].detail)
    assertEquals("Hand is partly connected · watching 2 fleets", shown.alertsDetail)
  }

  @Test
  fun aHostWithEveryFleetUnpairedSaysSoAndAHostNamedByItsIdDoesNotRepeatIt() {
    val state =
      FleetsState(
        listOf(HostGroup(ENDPOINT, HOST, HOST, listOf(FleetRow(ONE, paired = false), FleetRow(TWO, paired = false)))),
        loaded = true,
        phone = PhoneInfo("a…b", "0.1.0", listOf(PairedHost(ENDPOINT, HOST, HOST, listOf(PairedFleet(ONE, "", 1), PairedFleet(TWO, "", 1)))))
      )
    val line = thisPhone(state, "0.2.0").hosts.single()
    assertEquals("not paired anymore", line.link)
    assertEquals("2 fleets · not paired anymore", line.detail)
  }

  @Test
  fun aNoticeNamesItsHostOnlyWhenTwoOrMoreHostsArePaired() {
    assertEquals("atqa-laptop", noticeHost(twoHosts, ONE))
    assertEquals("vps-berlin", noticeHost(twoHosts, TWO))
    assertNull(noticeHost(twoHosts, "f0000000000ff"))
    assertNull(noticeHost(PhoneInfo("a…b", "0.1.0", twoHosts.hosts.take(1)), ONE))
    assertNull(noticeHost(null, ONE))
  }

  @Test
  fun signOutOpensTheBoardsPhonePageOfThatFleet() {
    assertEquals("/$ONE/connect", signOutPath(ONE))
    assertEquals("/$TWO/connect", signOutPath(TWO))
  }
}
