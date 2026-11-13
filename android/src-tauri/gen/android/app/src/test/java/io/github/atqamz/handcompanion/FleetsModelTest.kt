package io.github.atqamz.handcompanion

import java.io.File
import java.util.TimeZone
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

private const val ONE = "f000000000003"
private const val TWO = "f000000000005"

private fun needs(waiting: Int, worst: String?, count: Int, tone: String, cleared: String?): String {
  val items = if (waiting == 0) "" else """{"kind":"x","word":"W","tone":"$tone"}"""
  val head = if (worst == null) "" else """"worst_word":"$worst","""
  val clear = if (cleared == null) "" else """"cleared":"$cleared","""
  return """{"waiting":$waiting,"more":0,$head$clear"worst_count":$count,"items":[$items]}"""
}

private fun answer(
  fleet: String,
  name: String,
  pill: String,
  line: String,
  needs: String,
  cursor: String = "c1"
) =
  Raw(
    200,
    """{"version":1,"fleet":{"id":"$fleet","name":"$name"},"cursor":"$cursor","data":""" +
      """{"agent_state":"x","composer_hint":"","pill":"$pill","line":"$line",""" +
      """"gauge":{"text":"","level":"","pct":0},"compactions":0,"needs":$needs,""" +
      """"tasks":{"active":0,"inbox":0,"done":0,"abandoned":0},"projects":1}}"""
  )

private val blocked = answer(ONE, "atlas", "failing", "blocked", needs(5, "blocked", 1, "fail", null))
private val clear =
  answer(TWO, "orchard", "ready", "ready", needs(0, null, 0, "neutral", "2026-11-01T12:41:00Z"))

private class Laptop(var script: (String, String) -> Raw) : Transport {
  val paths = mutableListOf<String>()

  override fun request(
    fleet: String,
    post: Boolean,
    path: String,
    csrf: String?,
    body: String?
  ): Raw {
    paths += "$fleet$path"
    return script(fleet, path)
  }
}

private fun model(laptop: Laptop, clock: () -> String = { "12:41" }) =
  FleetsModel(laptop, { listOf(ONE, TWO) }, { "4d92…6ed6" }, clock = clock)

private fun FleetsModel.row(id: String) = state.value.rows.single { it.id == id }

class FleetsModelTest {
  @get:Rule val folder = TemporaryFolder()

  private val zone = TimeZone.getDefault()

  @After fun restoreZone() = TimeZone.setDefault(zone)

  private fun both(fleet: String) = if (fleet == ONE) blocked else clear

  @Test
  fun aChangedPollFillsTheRowOfEachFleet() {
    val model = model(Laptop { fleet, _ -> both(fleet) })
    runBlocking { model.refresh() }
    val state = model.state.value
    assertEquals("4d92…6ed6", state.laptop)
    assertNull(state.band)
    val first = model.row(ONE)
    assertEquals("atlas", first.name)
    assertEquals(Badge("1 blocked", Tone.Fail), first.badge)
    assertEquals(5, first.open)
    assertFalse(first.clear)
    assertEquals(Badge("blocked", Tone.Fail), first.pill)
    assertEquals("12:41", first.seen)
    val second = model.row(TWO)
    assertTrue(second.clear)
    assertNull(second.badge)
    assertEquals("12:41", second.since)
    assertEquals(Badge("ready", Tone.Pass), second.pill)
  }

  @Test
  fun theLauncherShowsLoadingUntilItsFirstAnswer() {
    val model = model(Laptop { fleet, _ -> both(fleet) })
    assertEquals(FleetsBody.Loading, fleetsBody(model.state.value))
    runBlocking { model.refresh() }
    assertEquals(FleetsBody.Rows, fleetsBody(model.state.value))
  }

  @Test
  fun aFirstAnswerWithNoFleetStillEndsTheLoadingLine() {
    val model = FleetsModel(Laptop { _, _ -> blocked }, { emptyList() }, { "4d92…6ed6" })
    runBlocking { model.refresh() }
    assertTrue(model.state.value.rows.isEmpty())
    assertEquals(FleetsBody.Rows, fleetsBody(model.state.value))
  }

  @Test
  fun anUnchangedPollKeepsTheRowAndMovesTheTimeSeen() {
    val laptop = Laptop { fleet, path ->
      if (path.contains("since=")) Raw(304, "") else both(fleet)
    }
    var now = "12:41"
    val model = model(laptop) { now }
    runBlocking { model.refresh() }
    val before = model.row(ONE)
    now = "12:46"
    runBlocking { model.refresh() }
    assertEquals(before.copy(seen = "12:46"), model.row(ONE))
    assertTrue(laptop.paths.any { it.startsWith(ONE) && it.contains("since=c1") })
  }

  @Test
  fun aChangedPollReplacesTheRow() {
    var next = blocked
    val model = model(Laptop { fleet, _ -> if (fleet == ONE) next else clear })
    runBlocking { model.refresh() }
    next = answer(ONE, "atlas", "working", "working", needs(2, "waiting", 2, "wait", null), "c2")
    runBlocking { model.refresh() }
    val row = model.row(ONE)
    assertEquals(Badge("2 waiting", Tone.Wait), row.badge)
    assertEquals(Badge("working", Tone.Accent), row.pill)
  }

  @Test
  fun aBusyLaptopShowsTheSentenceAndNoRetry() {
    val model = model(Laptop { _, _ -> throw BoardError.Busy })
    runBlocking { model.refresh() }
    assertEquals(Band("Your laptop is busy. Trying again.", retry = false), model.state.value.band)
  }

  @Test
  fun anUnreachableLaptopKeepsTheRowsAndNamesWhenItWentAway() {
    var up = true
    var now = "12:41"
    val model = model(Laptop { fleet, _ -> if (up) both(fleet) else throw BoardError.Unreachable }) { now }
    runBlocking { model.refresh() }
    up = false
    now = "12:50"
    runBlocking { model.refresh() }
    now = "12:55"
    runBlocking { model.refresh() }
    val band = model.state.value.band!!
    assertEquals("Can't reach 4d92…6ed6 since 12:50", band.title)
    assertEquals(
      "Check that the laptop is on, that Hand's Board is running, and that this phone has internet.",
      band.body
    )
    assertTrue(band.retry)
    assertEquals("12:41", model.row(ONE).seen)
    assertEquals(Badge("1 blocked", Tone.Fail), model.row(ONE).badge)
    up = true
    runBlocking { model.refresh() }
    assertNull(model.state.value.band)
    assertEquals("12:55", model.row(ONE).seen)
  }

  @Test
  fun anUnreachableLaptopIsAskedOnceAndNotOncePerFleet() {
    val laptop = Laptop { _, _ -> throw BoardError.Unreachable }
    runBlocking { model(laptop).refresh() }
    assertEquals(1, laptop.paths.size)
  }

  @Test
  fun aBoardThatIsNotRunningSaysSo() {
    val model = model(Laptop { _, _ -> throw BoardError.BoardNotRunning })
    runBlocking { model.refresh() }
    assertEquals(
      Band("Your laptop answers, but the Board is not running.", "Start it on the laptop."),
      model.state.value.band
    )
  }

  @Test
  fun aFleetThePhoneIsNoLongerPairedInTurnsToPairAgainAndTheOthersStay() {
    val model =
      model(Laptop { fleet, _ -> if (fleet == ONE) throw BoardError.Unpaired else clear })
    runBlocking { model.refresh() }
    assertNull(model.state.value.band)
    assertFalse(model.row(ONE).paired)
    assertEquals(Badge("ready", Tone.Pass), model.row(TWO).pill)
  }

  @Test
  fun anAnswerThatCannotBeReadMarksThatFleetAndTheRoundGoesOn() {
    val model =
      model(Laptop { fleet, _ -> if (fleet == ONE) throw BoardError.Malformed else clear })
    runBlocking { model.refresh() }
    assertNull(model.state.value.band)
    assertTrue(model.row(ONE).unreadable)
    assertFalse(model.row(TWO).unreadable)
    assertEquals(Badge("ready", Tone.Pass), model.row(TWO).pill)
  }

  @Test
  fun eachErrorThatIsNotAboutTheLaptopStaysOnItsFleet() {
    val errors =
      listOf(
        BoardError.UnsupportedVersion(2),
        BoardError.NotFound(null),
        BoardError.Internal(null),
        BoardError.Conflict(null),
        BoardError.Other(418, null, null),
        BoardError.TooLarge,
        BoardError.AppliedUnread(RuntimeException())
      )
    for (error in errors) {
      val model = model(Laptop { fleet, _ -> if (fleet == ONE) throw error else clear })
      runBlocking { model.refresh() }
      assertNull(error.toString(), model.state.value.band)
      assertTrue(error.toString(), model.row(ONE).unreadable)
      assertEquals(Badge("ready", Tone.Pass), model.row(TWO).pill)
    }
  }

  @Test
  fun aReadableAnswerClearsTheMark() {
    var step = 0
    val model =
      model(Laptop { fleet, path ->
        when {
          fleet == TWO -> clear
          step == 1 -> throw BoardError.Malformed
          path.contains("since=") -> Raw(304, "")
          else -> blocked
        }
      })
    runBlocking { model.refresh() }
    step = 1
    runBlocking { model.refresh() }
    assertTrue(model.row(ONE).unreadable)
    step = 2
    runBlocking { model.refresh() }
    assertFalse(model.row(ONE).unreadable)
    assertEquals(Badge("blocked", Tone.Fail), model.row(ONE).pill)
  }

  @Test
  fun aFleetThatCameBackAfterUnpairedStartsWithoutTheOldCursor() {
    var step = 0
    val laptop = Laptop { fleet, _ ->
      if (fleet == TWO) clear
      else
        when (step) {
          1 -> throw BoardError.Unpaired
          else -> blocked
        }
    }
    val model = model(laptop)
    runBlocking { model.refresh() }
    step = 1
    runBlocking { model.refresh() }
    step = 2
    runBlocking { model.refresh() }
    assertTrue(model.row(ONE).paired)
    val ones = laptop.paths.filter { it.startsWith(ONE) }
    assertTrue(ones[1].contains("since=c1"))
    assertFalse(ones[2].contains("since="))
  }

  @Test
  fun lostTimedOutAndNoLaptopEachShowTheUnreachableBand() {
    for (error in listOf(BoardError.Lost, BoardError.TimedOut, BoardError.NoLaptop)) {
      val model = model(Laptop { _, _ -> throw error })
      runBlocking { model.refresh() }
      val band = model.state.value.band!!
      assertEquals(error.toString(), "Can't reach 4d92…6ed6 since 12:41", band.title)
      assertTrue(band.retry)
    }
  }

  @Test
  fun anOutageAfterARecoveryCountsFromItsOwnStart() {
    var up = false
    var now = "12:41"
    val model =
      model(Laptop { fleet, _ -> if (up) both(fleet) else throw BoardError.Unreachable }) { now }
    runBlocking { model.refresh() }
    up = true
    now = "12:50"
    runBlocking { model.refresh() }
    up = false
    now = "13:05"
    runBlocking { model.refresh() }
    assertEquals("Can't reach 4d92…6ed6 since 13:05", model.state.value.band?.title)
  }

  @Test
  fun aBusyLaptopDoesNotStartTheOutageClock() {
    var error: BoardError = BoardError.Busy
    var now = "12:41"
    val model = model(Laptop { _, _ -> throw error }) { now }
    runBlocking { model.refresh() }
    error = BoardError.Unreachable
    now = "12:50"
    runBlocking { model.refresh() }
    assertEquals("Can't reach 4d92…6ed6 since 12:50", model.state.value.band?.title)
  }

  @Test
  fun theTimeIsTheLocalOneWhateverTheZoneOfTheAnswer() {
    TimeZone.setDefault(TimeZone.getTimeZone("Asia/Jakarta"))
    for ((iso, local) in
      listOf(
        "2026-11-01T12:41:00Z" to "19:41",
        "2026-11-01T12:41:00.250Z" to "19:41",
        "2026-11-01T12:41:00+02:00" to "17:41"
      )) {
      val model =
        model(
          Laptop { _, _ ->
            answer(ONE, "atlas", "ready", "ready", needs(0, null, 0, "neutral", iso))
          }
        )
      runBlocking { model.refresh() }
      assertEquals(iso, local, model.row(ONE).since)
    }
  }

  @Test
  fun aFleetThatLeavesTheCookieJarLeavesTheList() {
    var fleets = listOf(ONE, TWO)
    val model = FleetsModel(Laptop { f, _ -> both(f) }, { fleets }, { null }) { "12:41" }
    runBlocking { model.refresh() }
    fleets = listOf(TWO)
    runBlocking { model.refresh() }
    assertEquals(listOf(TWO), model.state.value.rows.map { it.id })
    assertNull(model.state.value.laptop)
  }

  @Test
  fun pairingsTheCoreCouldNotSaveAreSaidSoWhileTheRowsStayLive() {
    var unsaved = true
    val model = FleetsModel(Laptop { f, _ -> both(f) }, { listOf(ONE) }, { null }, { unsaved }) { "12:41" }
    runBlocking { model.refresh() }
    assertEquals(UNSAVED, model.state.value.band)
    assertEquals("blocked", model.row(ONE).pill?.text)
    assertFalse(model.state.value.band?.retry == true)
    unsaved = false
    runBlocking { model.refresh() }
    assertNull(model.state.value.band)
  }

  @Test
  fun anUnreachableLaptopIsSaidBeforeAnUnsavedStore() {
    val model =
      FleetsModel(Laptop { _, _ -> throw BoardError.Unreachable }, { listOf(ONE) }, { null }, { true }) {
        "12:41"
      }
    runBlocking { model.refresh() }
    assertTrue(model.state.value.band?.title?.startsWith("Can't reach") == true)
  }

  @Test
  fun fleetsComeFromTheCoresSpaceSeparatedIds() {
    assertEquals(
      listOf(ONE, TWO),
      pairedFleets("$ONE $TWO $ONE bad  hand_phone_$TWO")
    )
    assertEquals(emptyList<String>(), pairedFleets(null))
  }

  @Test
  fun theLaptopShowsTheEndsOfItsId() {
    val file = folder.newFile("laptop")
    file.writeText("4d92" + "0".repeat(56) + "6ed6\n")
    assertEquals("4d92…6ed6", laptopId(file))
    assertNull(laptopId(File(folder.root, "missing")))
  }

  @Test
  fun aFleetHasTheTintTheBoardGivesIt() {
    assertEquals(10, tint("f00000000e0aa"))
    assertEquals(5, tint("f0123456789ab"))
    assertEquals(0, tint("f000000000003"))
  }

  @Test
  fun forgettingAsksFirstAndTheMenuOnlyOpensTheQuestion() {
    var forgotten = 0
    val model = FleetsModel(Laptop { fleet, _ -> both(fleet) }, { listOf(ONE) }, { "4d92…6ed6" }, forgetter = { forgotten++; true })
    model.ask(true)
    assertTrue(model.state.value.asking)
    assertEquals(0, forgotten)
    model.ask(false)
    assertFalse(model.state.value.asking)
    assertEquals(0, forgotten)
  }

  @Test
  fun aForgetThatWorksEmptiesTheListAndClearsTheAlertsLine() {
    var paired = listOf(ONE, TWO)
    val model =
      FleetsModel(
        Laptop { fleet, _ -> both(fleet) },
        { paired },
        { "4d92…6ed6" },
        forgetter = {
          paired = emptyList()
          true
        }
      )
    runBlocking { model.refresh() }
    model.alerts(true)
    model.ask(true)
    runBlocking { model.forgetLaptop() }
    val state = model.state.value
    assertTrue(state.rows.isEmpty())
    assertFalse(state.asking)
    assertFalse(state.alertsOff)
    assertNull(state.notice)
  }

  @Test
  fun aForgetThatFailsIsShownAndKeepsWhatTheCoreStillHolds() {
    val model =
      FleetsModel(Laptop { fleet, _ -> both(fleet) }, { listOf(ONE) }, { "4d92…6ed6" }, forgetter = { false })
    runBlocking { model.refresh() }
    model.ask(true)
    runBlocking { model.forgetLaptop() }
    val state = model.state.value
    assertEquals("Can't clear the saved login. Try again.", state.notice)
    assertFalse(state.asking)
    assertEquals(listOf(ONE), state.rows.map { it.id })
  }

  @Test
  fun aNoticeGoesWhenTheUserAsksOrSaysSomethingElse() {
    val model = model(Laptop { fleet, _ -> both(fleet) })
    model.say("This is not a link Hand can open.")
    assertEquals("This is not a link Hand can open.", model.state.value.notice)
    model.ask(true)
    assertNull(model.state.value.notice)
    model.say("again")
    model.say(null)
    assertNull(model.state.value.notice)
  }

  @Test
  fun aPollKeepsTheAlertsLineAndTheNotice() {
    val model = model(Laptop { fleet, _ -> both(fleet) })
    model.alerts(true)
    model.say("kept")
    runBlocking { model.refresh() }
    assertTrue(model.state.value.alertsOff)
    assertEquals("kept", model.state.value.notice)
  }
}
