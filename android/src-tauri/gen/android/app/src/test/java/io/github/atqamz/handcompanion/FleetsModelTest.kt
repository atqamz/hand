package io.github.atqamz.handcompanion

import java.util.Collections
import java.util.TimeZone
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlin.concurrent.thread
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private const val ONE = "f000000000003"
private const val TWO = "f000000000005"
private const val THREE = "f000000000007"
private const val LAPTOP = "4d92000000000000000000000000000000000000000000000000000000006ed6"
private const val VPS = "7c1e00000000000000000000000000000000000000000000000000000000a2f9"

private fun host(endpoint: String, name: String, vararg ids: String) =
  PairedHost(endpoint, "${endpoint.take(4)}…${endpoint.takeLast(4)}", name, ids.map { PairedFleet(it, "", 1) })

private fun laptop(vararg ids: String) = host(LAPTOP, "4d92…6ed6", *ids)

private fun phone(vararg hosts: PairedHost) = PhoneInfo("9ab2…11fe", "0.1.0", hosts.toList())

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

private class Host(var script: (String, String) -> Raw) : Transport {
  val paths: MutableList<String> = Collections.synchronizedList(mutableListOf())

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

private fun model(host: Host, clock: () -> String = { "12:41" }) =
  FleetsModel(host, { phone(laptop(ONE, TWO)) }, clock = clock)

private fun FleetsModel.row(id: String) = state.value.rows.single { it.id == id }

class FleetsModelTest {
  private val zone = TimeZone.getDefault()

  @After fun restoreZone() = TimeZone.setDefault(zone)

  private fun both(fleet: String) = if (fleet == ONE) blocked else clear

  @Test
  fun aChangedPollFillsTheRowOfEachFleet() {
    val model = model(Host { fleet, _ -> both(fleet) })
    runBlocking { model.refresh() }
    val state = model.state.value
    assertEquals("4d92…6ed6", state.hosts.single().name)
    assertNull(state.band)
    assertNull(state.hosts.single().band)
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
    val model = model(Host { fleet, _ -> both(fleet) })
    assertEquals(FleetsBody.Loading, fleetsBody(model.state.value))
    runBlocking { model.refresh() }
    assertEquals(FleetsBody.Rows, fleetsBody(model.state.value))
  }

  @Test
  fun aFirstAnswerWithNoFleetStillEndsTheLoadingLine() {
    val model = FleetsModel(Host { _, _ -> blocked }, { phone() })
    runBlocking { model.refresh() }
    assertTrue(model.state.value.rows.isEmpty())
    assertEquals(FleetsBody.Rows, fleetsBody(model.state.value))
  }

  @Test
  fun anUnchangedPollKeepsTheRowAndMovesTheTimeSeen() {
    val host = Host { fleet, path ->
      if (path.contains("since=")) Raw(304, "") else both(fleet)
    }
    var now = "12:41"
    val model = model(host) { now }
    runBlocking { model.refresh() }
    val before = model.row(ONE)
    now = "12:46"
    runBlocking { model.refresh() }
    assertEquals(before.copy(seen = "12:46"), model.row(ONE))
    assertTrue(host.paths.any { it.startsWith(ONE) && it.contains("since=c1") })
  }

  @Test
  fun aChangedPollReplacesTheRow() {
    var next = blocked
    val model = model(Host { fleet, _ -> if (fleet == ONE) next else clear })
    runBlocking { model.refresh() }
    next = answer(ONE, "atlas", "working", "working", needs(2, "waiting", 2, "wait", null), "c2")
    runBlocking { model.refresh() }
    val row = model.row(ONE)
    assertEquals(Badge("2 waiting", Tone.Wait), row.badge)
    assertEquals(Badge("working", Tone.Accent), row.pill)
  }

  @Test
  fun aBusyHostShowsTheSentenceAndNoRetry() {
    val model = model(Host { _, _ -> throw BoardError.Busy })
    runBlocking { model.refresh() }
    assertEquals(Band("Your host is busy. Trying again.", retry = false), model.state.value.hosts.single().band)
  }

  @Test
  fun anUnreachableHostKeepsTheRowsAndNamesWhenItWentAway() {
    var up = true
    var now = "12:41"
    val model = model(Host { fleet, _ -> if (up) both(fleet) else throw BoardError.Unreachable }) { now }
    runBlocking { model.refresh() }
    up = false
    now = "12:50"
    runBlocking { model.refresh() }
    now = "12:55"
    runBlocking { model.refresh() }
    val band = model.state.value.hosts.single().band!!
    assertEquals("Can't reach 4d92…6ed6 since 12:50", band.title)
    assertEquals(
      "Check that the host is on, that Hand's Board is running, and that this phone has internet.",
      band.body
    )
    assertTrue(band.retry)
    assertEquals("12:41", model.row(ONE).seen)
    assertEquals(Badge("1 blocked", Tone.Fail), model.row(ONE).badge)
    up = true
    runBlocking { model.refresh() }
    assertNull(model.state.value.hosts.single().band)
    assertEquals("12:55", model.row(ONE).seen)
  }

  @Test
  fun anUnreachableHostIsAskedOnceAndNotOncePerFleet() {
    val host = Host { _, _ -> throw BoardError.Unreachable }
    runBlocking { model(host).refresh() }
    assertEquals(1, host.paths.size)
  }

  @Test
  fun aBoardThatIsNotRunningSaysSo() {
    val model = model(Host { _, _ -> throw BoardError.BoardNotRunning })
    runBlocking { model.refresh() }
    assertEquals(
      Band("Your host answers, but the Board is not running.", "Start it on the host."),
      model.state.value.hosts.single().band
    )
  }

  @Test
  fun aFleetThePhoneIsNoLongerPairedInTurnsToPairAgainAndTheOthersStay() {
    val model =
      model(Host { fleet, _ -> if (fleet == ONE) throw BoardError.Unpaired else clear })
    runBlocking { model.refresh() }
    assertNull(model.state.value.hosts.single().band)
    assertFalse(model.row(ONE).paired)
    assertEquals(Badge("ready", Tone.Pass), model.row(TWO).pill)
  }

  @Test
  fun anAnswerThatCannotBeReadMarksThatFleetAndTheRoundGoesOn() {
    val model =
      model(Host { fleet, _ -> if (fleet == ONE) throw BoardError.Malformed else clear })
    runBlocking { model.refresh() }
    assertNull(model.state.value.hosts.single().band)
    assertTrue(model.row(ONE).unreadable)
    assertFalse(model.row(TWO).unreadable)
    assertEquals(Badge("ready", Tone.Pass), model.row(TWO).pill)
  }

  @Test
  fun eachErrorThatIsNotAboutTheHostStaysOnItsFleet() {
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
      val model = model(Host { fleet, _ -> if (fleet == ONE) throw error else clear })
      runBlocking { model.refresh() }
      assertNull(error.toString(), model.state.value.hosts.single().band)
      assertTrue(error.toString(), model.row(ONE).unreadable)
      assertEquals(Badge("ready", Tone.Pass), model.row(TWO).pill)
    }
  }

  @Test
  fun aReadableAnswerClearsTheMark() {
    var step = 0
    val model =
      model(Host { fleet, path ->
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
    val host = Host { fleet, _ ->
      if (fleet == TWO) clear
      else
        when (step) {
          1 -> throw BoardError.Unpaired
          else -> blocked
        }
    }
    val model = model(host)
    runBlocking { model.refresh() }
    step = 1
    runBlocking { model.refresh() }
    step = 2
    runBlocking { model.refresh() }
    assertTrue(model.row(ONE).paired)
    val ones = host.paths.filter { it.startsWith(ONE) }
    assertTrue(ones[1].contains("since=c1"))
    assertFalse(ones[2].contains("since="))
  }

  @Test
  fun lostTimedOutAndNoHostEachShowTheUnreachableBand() {
    for (error in listOf(BoardError.Lost, BoardError.TimedOut, BoardError.NoHost)) {
      val model = model(Host { _, _ -> throw error })
      runBlocking { model.refresh() }
      val band = model.state.value.hosts.single().band!!
      assertEquals(error.toString(), "Can't reach 4d92…6ed6 since 12:41", band.title)
      assertTrue(band.retry)
    }
  }

  @Test
  fun anOutageAfterARecoveryCountsFromItsOwnStart() {
    var up = false
    var now = "12:41"
    val model =
      model(Host { fleet, _ -> if (up) both(fleet) else throw BoardError.Unreachable }) { now }
    runBlocking { model.refresh() }
    up = true
    now = "12:50"
    runBlocking { model.refresh() }
    up = false
    now = "13:05"
    runBlocking { model.refresh() }
    assertEquals("Can't reach 4d92…6ed6 since 13:05", model.state.value.hosts.single().band?.title)
  }

  @Test
  fun aBusyHostDoesNotStartTheOutageClock() {
    var error: BoardError = BoardError.Busy
    var now = "12:41"
    val model = model(Host { _, _ -> throw error }) { now }
    runBlocking { model.refresh() }
    error = BoardError.Unreachable
    now = "12:50"
    runBlocking { model.refresh() }
    assertEquals("Can't reach 4d92…6ed6 since 12:50", model.state.value.hosts.single().band?.title)
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
          Host { _, _ ->
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
    val model =
      FleetsModel(Host { f, _ -> both(f) }, { phone(laptop(*fleets.toTypedArray())) }) { "12:41" }
    runBlocking { model.refresh() }
    fleets = listOf(TWO)
    runBlocking { model.refresh() }
    assertEquals(listOf(TWO), model.state.value.rows.map { it.id })
  }

  @Test
  fun pairingsTheCoreCouldNotSaveAreSaidSoWhileTheRowsStayLive() {
    var unsaved = true
    val model = FleetsModel(Host { f, _ -> both(f) }, { phone(laptop(ONE)) }, { unsaved }) { "12:41" }
    runBlocking { model.refresh() }
    assertEquals(UNSAVED, model.state.value.band)
    assertEquals("blocked", model.row(ONE).pill?.text)
    assertFalse(model.state.value.band?.retry == true)
    unsaved = false
    runBlocking { model.refresh() }
    assertNull(model.state.value.band)
  }

  @Test
  fun anUnreachableHostIsSaidBeforeAnUnsavedStore() {
    val model =
      FleetsModel(Host { _, _ -> throw BoardError.Unreachable }, { phone(laptop(ONE)) }, { true }) {
        "12:41"
      }
    runBlocking { model.refresh() }
    assertTrue(model.state.value.hosts.single().band?.title?.startsWith("Can't reach") == true)
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
    val model =
      FleetsModel(Host { fleet, _ -> both(fleet) }, { phone(laptop(ONE)) }, forgetter = { forgotten++; true })
    model.ask(Forgetting(null, null, 1))
    assertNotNull(model.state.value.asking)
    assertEquals(0, forgotten)
    model.ask(null)
    assertNull(model.state.value.asking)
    assertEquals(0, forgotten)
  }

  @Test
  fun aForgetThatWorksEmptiesTheListAndClearsTheAlertsLine() {
    var paired = arrayOf(ONE, TWO)
    val model =
      FleetsModel(
        Host { fleet, _ -> both(fleet) },
        { if (paired.isEmpty()) phone() else phone(laptop(*paired)) },
        forgetter = {
          paired = emptyArray()
          true
        }
      )
    runBlocking { model.refresh() }
    model.alerts(true)
    runBlocking { model.forget(Forgetting(null, null, 2)) }
    val state = model.state.value
    assertTrue(state.rows.isEmpty())
    assertNull(state.asking)
    assertFalse(state.alertsOff)
    assertNull(state.notice)
  }

  @Test
  fun aForgetThatFailsIsShownAndKeepsWhatTheCoreStillHolds() {
    val model =
      FleetsModel(Host { fleet, _ -> both(fleet) }, { phone(laptop(ONE)) }, forgetter = { false })
    runBlocking { model.refresh() }
    model.ask(Forgetting(null, null, 1))
    runBlocking { model.forget(Forgetting(null, null, 1)) }
    val state = model.state.value
    assertEquals("Can't clear the saved login. Try again.", state.notice)
    assertNull(state.asking)
    assertEquals(listOf(ONE), state.rows.map { it.id })
  }

  @Test
  fun aNoticeGoesWhenTheUserAsksOrSaysSomethingElse() {
    val model = model(Host { fleet, _ -> both(fleet) })
    model.say("This is not a link Hand can open.")
    assertEquals("This is not a link Hand can open.", model.state.value.notice)
    model.ask(Forgetting(null, null, 1))
    assertNull(model.state.value.notice)
    model.say("again")
    model.say(null)
    assertNull(model.state.value.notice)
  }

  @Test
  fun aPollKeepsTheAlertsLineAndTheNotice() {
    val model = model(Host { fleet, _ -> both(fleet) })
    model.alerts(true)
    model.say("kept")
    runBlocking { model.refresh() }
    assertTrue(model.state.value.alertsOff)
    assertEquals("kept", model.state.value.notice)
  }

  private val orchard = answer(THREE, "ops", "ready", "ready", needs(0, null, 0, "neutral", null))

  private val twoHosts = phone(host(LAPTOP, "atqa-laptop", ONE, TWO), host(VPS, "vps-berlin", THREE))

  private fun answers(fleet: String) = if (fleet == THREE) orchard else both(fleet)

  private fun FleetsModel.group(endpoint: String) = state.value.hosts.single { it.endpoint == endpoint }

  @Test
  fun eachHostIsAGroupOfItsOwnFleetsInPairingOrder() {
    val model = FleetsModel(Host { fleet, _ -> answers(fleet) }, { twoHosts })
    runBlocking { model.refresh() }
    val groups = model.state.value.hosts
    assertEquals(listOf("atqa-laptop", "vps-berlin"), groups.map { it.name })
    assertEquals(listOf(listOf(ONE, TWO), listOf(THREE)), groups.map { it.rows.map(FleetRow::id) })
    assertEquals(listOf(ONE, TWO, THREE), model.state.value.rows.map { it.id })
    assertEquals("vps-berlin", model.state.value.hostOf(THREE)?.name)
    assertNull(model.state.value.hostOf("f0000000000ff"))
  }

  @Test
  fun aDeadHostNeverBlanksTheFleetsOfAnotherHost() {
    var up = true
    val model =
      FleetsModel(
        Host { fleet, _ -> if (fleet == THREE && !up) throw BoardError.Unreachable else answers(fleet) },
        { twoHosts }
      ) { "09:12" }
    runBlocking { model.refresh() }
    up = false
    runBlocking { model.refresh() }
    val dead = model.group(VPS)
    assertEquals("Can't reach vps-berlin since 09:12", dead.band?.title)
    assertTrue(dead.stale)
    assertEquals("ops", dead.rows.single().name)
    val live = model.group(LAPTOP)
    assertNull(live.band)
    assertFalse(live.stale)
    assertEquals(Badge("1 blocked", Tone.Fail), live.rows.first().badge)
    assertEquals(Badge("ready", Tone.Pass), live.rows.last().pill)
  }

  @Test
  fun aDeadHostAtTheFrontDoesNotHoldBackTheHostsBehindIt() {
    val answered = CountDownLatch(1)
    var waited = true
    val model =
      FleetsModel(
        Host { fleet, _ ->
          if (fleet == ONE) {
            waited = answered.await(5, TimeUnit.SECONDS)
            throw BoardError.TimedOut
          }
          answered.countDown()
          answers(fleet)
        },
        { phone(host(LAPTOP, "atqa-laptop", ONE), host(VPS, "vps-berlin", THREE)) }
      )
    runBlocking { model.refresh() }
    assertTrue(waited)
    assertNull(model.group(VPS).band)
    assertNotNull(model.group(LAPTOP).band)
  }

  private fun waitFor(what: String, seconds: Long = 3, until: () -> Boolean) {
    val end = System.nanoTime() + TimeUnit.SECONDS.toNanos(seconds)
    while (!until() && System.nanoTime() < end) Thread.sleep(10)
    assertTrue(what, until())
  }

  @Test
  fun aLiveHostsRowsShowWhileADeadHostIsStillPending() {
    val release = CountDownLatch(1)
    val model =
      FleetsModel(
        Host { fleet, _ ->
          if (fleet == ONE) {
            release.await(10, TimeUnit.SECONDS)
            throw BoardError.TimedOut
          }
          answers(fleet)
        },
        { phone(host(LAPTOP, "atqa-laptop", ONE), host(VPS, "vps-berlin", THREE)) }
      )
    val round = thread { runBlocking { model.refresh() } }
    try {
      waitFor("live host rows were held back by the dead host") { model.state.value.loaded }
      assertEquals(FleetsBody.Rows, fleetsBody(model.state.value))
      assertEquals(listOf(VPS), model.state.value.hosts.map { it.endpoint })
      assertNull(model.group(VPS).band)
    } finally {
      release.countDown()
      round.join()
    }
    assertEquals(listOf(LAPTOP, VPS), model.state.value.hosts.map { it.endpoint })
    assertNotNull(model.group(LAPTOP).band)
  }

  @OptIn(ExperimentalCoroutinesApi::class)
  @Test
  fun aRetryDuringARoundWithADeadHostRunsAnotherRoundOnceAfterIt() {
    Dispatchers.setMain(Dispatchers.Unconfined)
    try {
      val release = CountDownLatch(1)
      val asked = AtomicInteger()
      val model =
        FleetsModel(
          Host { fleet, _ ->
            if (fleet == ONE) {
              asked.incrementAndGet()
              release.await(10, TimeUnit.SECONDS)
              throw BoardError.TimedOut
            }
            answers(fleet)
          },
          { phone(host(LAPTOP, "atqa-laptop", ONE), host(VPS, "vps-berlin", THREE)) }
        )
      val round = thread { runBlocking { model.refresh() } }
      waitFor("the dead host was never asked") { asked.get() == 1 }
      model.retry()
      model.retry()
      release.countDown()
      round.join()
      waitFor("the retry did not ask the dead host again") { asked.get() == 2 }
      Thread.sleep(200)
      assertEquals(2, asked.get())
    } finally {
      Dispatchers.resetMain()
    }
  }

  @Test
  fun eachHostCountsItsOutageFromItsOwnStart() {
    var down = setOf(ONE)
    var now = "12:41"
    val model =
      FleetsModel(
        Host { fleet, _ -> if (fleet in down) throw BoardError.Unreachable else answers(fleet) },
        { twoHosts }
      ) { now }
    runBlocking { model.refresh() }
    down = setOf(ONE, THREE)
    now = "12:50"
    runBlocking { model.refresh() }
    assertEquals("Can't reach atqa-laptop since 12:41", model.group(LAPTOP).band?.title)
    assertEquals("Can't reach vps-berlin since 12:50", model.group(VPS).band?.title)
    down = setOf(THREE)
    now = "12:55"
    runBlocking { model.refresh() }
    assertNull(model.group(LAPTOP).band)
    assertEquals("Can't reach vps-berlin since 12:50", model.group(VPS).band?.title)
    down = setOf(ONE)
    now = "13:05"
    runBlocking { model.refresh() }
    down = setOf(ONE, THREE)
    now = "13:10"
    runBlocking { model.refresh() }
    assertEquals("Can't reach vps-berlin since 13:10", model.group(VPS).band?.title)
  }

  @Test
  fun aHostIsAskedOncePerRoundWhenItsFirstFleetIsUnreachable() {
    val host = Host { fleet, _ -> if (fleet == THREE) answers(fleet) else throw BoardError.Unreachable }
    runBlocking { FleetsModel(host, { twoHosts }).refresh() }
    assertEquals(1, host.paths.count { it.startsWith(ONE) || it.startsWith(TWO) })
    assertEquals(1, host.paths.count { it.startsWith(THREE) })
  }

  @Test
  fun aFleetThatWasNeverAnsweredShowsItsStoredNameUntilTheBoardAnswers() {
    val stored = phone(PairedHost(LAPTOP, "4d92…6ed6", "atqa-laptop", listOf(PairedFleet(ONE, "atlas", 1), PairedFleet(TWO, "", 1))))
    val model = FleetsModel(Host { _, _ -> throw BoardError.Unreachable }, { stored })
    runBlocking { model.refresh() }
    assertEquals(listOf("atlas", TWO), model.state.value.rows.map { it.name })
  }

  @Test
  fun aHostWhoseEveryFleetIsUnpairedIsUnpairedAndOneWithSomeLeftIsNot() {
    var gone = setOf(ONE, TWO)
    val model =
      FleetsModel(
        Host { fleet, _ -> if (fleet in gone) throw BoardError.Unpaired else answers(fleet) },
        { twoHosts }
      )
    runBlocking { model.refresh() }
    assertTrue(model.group(LAPTOP).unpaired)
    assertFalse(model.group(VPS).unpaired)
    assertNull(model.group(LAPTOP).band)
    gone = setOf(ONE)
    runBlocking { model.refresh() }
    assertFalse(model.group(LAPTOP).unpaired)
    assertFalse(model.row(ONE).paired)
    assertTrue(model.row(TWO).paired)
  }

  @Test
  fun aBusyHostIsNotStaleAndKeepsNoRetry() {
    val model =
      FleetsModel(
        Host { fleet, _ -> if (fleet == THREE) throw BoardError.Busy else answers(fleet) },
        { twoHosts }
      )
    runBlocking { model.refresh() }
    assertFalse(model.group(VPS).stale)
    assertFalse(model.group(VPS).band?.retry == true)
    assertNull(model.group(LAPTOP).band)
  }

  @Test
  fun forgettingOneHostForgetsThatHostAndNoOtherAndKeepsTheAlertsLine() {
    val forgotten = mutableListOf<String>()
    var held = twoHosts
    var everything = 0
    val model =
      FleetsModel(
        Host { fleet, _ -> answers(fleet) },
        { held },
        forgetter = { everything++; true },
        hostForgetter = {
          forgotten += it
          held = phone(*held.hosts.filter { host -> host.endpoint != it }.toTypedArray())
          true
        }
      )
    runBlocking { model.refresh() }
    model.alerts(true)
    runBlocking { model.forget(Forgetting(VPS, "vps-berlin", 1)) }
    assertEquals(listOf(VPS), forgotten)
    assertEquals(0, everything)
    assertEquals(listOf("atqa-laptop"), model.state.value.hosts.map { it.name })
    assertTrue(model.state.value.alertsOff)
    runBlocking { model.forget(Forgetting(LAPTOP, "atqa-laptop", 2)) }
    assertTrue(model.state.value.hosts.isEmpty())
    assertFalse(model.state.value.alertsOff)
  }

  @Test
  fun forgettingEverythingUsesTheWholeInstallForget() {
    val forgotten = mutableListOf<String>()
    var everything = 0
    val model =
      FleetsModel(
        Host { fleet, _ -> answers(fleet) },
        { twoHosts },
        forgetter = { everything++; true },
        hostForgetter = { forgotten += it; true }
      )
    runBlocking { model.refresh() }
    runBlocking { model.forget(Forgetting(null, null, 3)) }
    assertEquals(1, everything)
    assertTrue(forgotten.isEmpty())
  }

  @Test
  fun aHostForgetThatFailsIsShownAndTheHostStays() {
    val model =
      FleetsModel(Host { fleet, _ -> answers(fleet) }, { twoHosts }, hostForgetter = { false })
    runBlocking { model.refresh() }
    runBlocking { model.forget(Forgetting(VPS, "vps-berlin", 1)) }
    assertEquals(FORGET_FAILED, model.state.value.notice)
    assertEquals(2, model.state.value.hosts.size)
  }

  @Test
  fun confirmingForgetsWhatTheQuestionNamedAndAnEmptyQuestionForgetsNothing() {
    val forgotten = mutableListOf<String>()
    val model =
      FleetsModel(Host { fleet, _ -> answers(fleet) }, { twoHosts }, hostForgetter = { forgotten += it; true })
    model.forget()
    assertTrue(forgotten.isEmpty())
    model.ask(Forgetting(VPS, "vps-berlin", 1))
    assertEquals(VPS, model.state.value.asking?.endpoint)
  }

  @Test
  fun aHostThatLeavesTheStoreLeavesTheList() {
    var held = twoHosts
    val model = FleetsModel(Host { fleet, _ -> answers(fleet) }, { held })
    runBlocking { model.refresh() }
    held = phone(held.hosts.first())
    runBlocking { model.refresh() }
    assertEquals(listOf(LAPTOP), model.state.value.hosts.map { it.endpoint })
    assertEquals(listOf(ONE, TWO), model.state.value.rows.map { it.id })
  }

  @Test
  fun aHostThatLeavesWhileDownStartsItsNextOutageFromScratch() {
    var held = twoHosts
    var now = "12:41"
    val model = FleetsModel(Host { _, _ -> throw BoardError.Unreachable }, { held }) { now }
    runBlocking { model.refresh() }
    held = phone(twoHosts.hosts.first())
    runBlocking { model.refresh() }
    held = twoHosts
    now = "13:05"
    runBlocking { model.refresh() }
    assertEquals("Can't reach vps-berlin since 13:05", model.group(VPS).band?.title)
  }

  @Test
  fun aFleetThatLeavesTheStoreAndComesBackStartsWithoutTheOldCursor() {
    var held = twoHosts
    val host = Host { fleet, _ -> answers(fleet) }
    val model = FleetsModel(host, { held })
    runBlocking { model.refresh() }
    held = phone(twoHosts.hosts.last())
    runBlocking { model.refresh() }
    held = twoHosts
    runBlocking { model.refresh() }
    val ones = host.paths.filter { it.startsWith(ONE) }
    assertEquals(2, ones.size)
    assertFalse(ones.last().contains("since="))
  }
}
