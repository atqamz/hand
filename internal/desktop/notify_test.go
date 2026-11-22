package desktop_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/desktop"
)

type alerts struct {
	got []desktop.Alert
	now time.Time
}

func (a *alerts) advance(clock *fakeClock, d time.Duration) {
	for elapsed := time.Duration(0); elapsed < d; elapsed += time.Second {
		a.now = a.now.Add(time.Second)
		clock.Advance(time.Second)
	}
}

func notifyApp(t *testing.T, f *fixture, setup ...func(*desktop.App)) (*desktop.App, *fakeClock, *alerts) {
	t.Helper()
	n := &alerts{now: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}
	opt := func(a *desktop.App) {
		a.Notify = true
		a.NotifyOK = func() bool { return true }
		a.OnNotify = func(al desktop.Alert) { n.got = append(n.got, al) }
		a.Now = func() time.Time { return n.now }
		for _, fn := range setup {
			fn(a)
		}
	}
	app, clock, _ := osApp(t, f, opt)
	return app, clock, n
}

func blockedBy(ref string) desktop.Needs {
	return desktop.Needs{Waiting: 1, Worst: "blocked", WorstWord: "blocked", Items: []desktop.Need{{Kind: "blocked", Ref: ref}}}
}

func TestNothingNotifiesUntilTheOperatorOptsIn(t *testing.T) {
	f := quietPair(t)
	app, clock, n := notifyApp(t, f, func(a *desktop.App) { a.Notify = false })
	if app.Notify {
		t.Fatal("Notify is on by default")
	}
	n.advance(clock, 10*time.Second)
	app.SetFocused(false)
	setNeeds(f, fleetID, blockedNeed)
	n.advance(clock, 10*time.Second)
	if len(n.got) != 0 {
		t.Fatalf("notified %v with the setting off", n.got)
	}
}

func TestABlockedRiseNotifiesTheSelectedFleetWithItsRef(t *testing.T) {
	f := quietPair(t)
	app, clock, n := notifyApp(t, f)
	n.advance(clock, 10*time.Second)
	app.SetFocused(false)
	setNeeds(f, fleetID, blockedNeed)
	n.advance(clock, 10*time.Second)
	want := desktop.Alert{ID: "attn:" + fleetID, Title: "Hand · golden", Body: "s1 is blocked"}
	if len(n.got) != 1 || n.got[0] != want {
		t.Fatalf("alerts %v, want [%v]", n.got, want)
	}
}

func TestAnotherFleetsBlockedScreenNotifiesWithoutARef(t *testing.T) {
	f := quietPair(t)
	app, clock, n := notifyApp(t, f)
	n.advance(clock, 10*time.Second)
	app.SetFocused(false)
	setAttn(f, secondID, blockedNeed)
	n.advance(clock, 10*time.Second)
	want := desktop.Alert{ID: "attn:" + secondID, Title: "Hand · second", Body: "A screen is blocked"}
	if len(n.got) != 1 || n.got[0] != want {
		t.Fatalf("alerts %v, want [%v]", n.got, want)
	}
}

func TestAWorkerBlockNotifiesToo(t *testing.T) {
	f := quietPair(t)
	app, clock, n := notifyApp(t, f)
	n.advance(clock, 10*time.Second)
	app.SetFocused(false)
	setNeeds(f, fleetID, desktop.Needs{Waiting: 1, Worst: "worker", WorstWord: "blocked", Items: []desktop.Need{{Kind: "worker", Ref: "a1"}}})
	n.advance(clock, 10*time.Second)
	if len(n.got) != 1 || n.got[0].Body != "a1 is blocked" {
		t.Fatalf("alerts %v", n.got)
	}
}

func TestOnlyABlockedScreenNotifies(t *testing.T) {
	for _, tc := range []struct {
		name  string
		needs desktop.Needs
	}{
		{"decision", decisions(2)},
		{"report", reports(1)},
		{"failure", desktop.Needs{Waiting: 1, Worst: "failure", WorstWord: "failed", Items: []desktop.Need{{Kind: "failure", Ref: "a2"}}}},
		{"limited", desktop.Needs{Waiting: 1, Worst: "limited", WorstWord: "waiting"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := quietPair(t)
			app, clock, n := notifyApp(t, f)
			n.advance(clock, 10*time.Second)
			app.SetFocused(false)
			setNeeds(f, fleetID, tc.needs)
			setAttn(f, secondID, tc.needs)
			n.advance(clock, 20*time.Second)
			if len(n.got) != 0 {
				t.Fatalf("notified %v for a %s", n.got, tc.name)
			}
		})
	}
}

func TestNothingNotifiesWhileTheWindowHasFocus(t *testing.T) {
	f := quietPair(t)
	_, clock, n := notifyApp(t, f)
	n.advance(clock, 10*time.Second)
	setNeeds(f, fleetID, blockedNeed)
	setAttn(f, secondID, blockedNeed)
	n.advance(clock, 30*time.Second)
	if len(n.got) != 0 {
		t.Fatalf("notified %v while focused", n.got)
	}
}

func TestAMinimizedWindowNotifies(t *testing.T) {
	f := quietPair(t)
	app, clock, n := notifyApp(t, f)
	n.advance(clock, 5*time.Second)
	app.SetMinimized(true)
	n.advance(clock, 15*time.Second)
	setAttn(f, secondID, blockedNeed)
	n.advance(clock, 15*time.Second)
	if len(n.got) != 1 || n.got[0].ID != "attn:"+secondID {
		t.Fatalf("alerts %v", n.got)
	}
}

func TestAQueueThereAtStartIsABaselineNotAnAlert(t *testing.T) {
	f := markFixture(t)
	setNeeds(f, fleetID, blockedNeed)
	setAttn(f, secondID, blockedNeed)
	_, clock, n := notifyApp(t, f, func(a *desktop.App) { a.SetFocused(false) })
	n.advance(clock, 60*time.Second)
	if len(n.got) != 0 {
		t.Fatalf("notified %v for a queue that was there when the window opened", n.got)
	}
}

func TestADecisionAddedUnderAnOldBlockDoesNotNotifyAgain(t *testing.T) {
	f := quietPair(t)
	setNeeds(f, fleetID, blockedNeed)
	app, clock, n := notifyApp(t, f, func(a *desktop.App) { a.SetFocused(false) })
	n.advance(clock, 10*time.Second)
	setNeeds(f, fleetID, desktop.Needs{Waiting: 2, Worst: "blocked", WorstWord: "blocked", Items: []desktop.Need{{Kind: "blocked", Ref: "s1"}, {Kind: "decision", Ref: "d1"}}})
	n.advance(clock, 90*time.Second)
	if len(n.got) != 0 {
		t.Fatalf("notified %v for a block that was already shown", n.got)
	}
	setNeeds(f, fleetID, blockedBy("s2"))
	n.advance(clock, 10*time.Second)
	if len(n.got) != 1 || n.got[0].Body != "s2 is blocked" {
		t.Fatalf("alerts %v, want one for s2", n.got)
	}
	_ = app
}

func TestAFleetIsNotifiedOncePerMinuteUnderOneID(t *testing.T) {
	f := quietPair(t)
	app, clock, n := notifyApp(t, f)
	n.advance(clock, 10*time.Second)
	app.SetFocused(false)
	flap := func() {
		setNeeds(f, fleetID, desktop.Needs{})
		n.advance(clock, 6*time.Second)
		setNeeds(f, fleetID, blockedNeed)
		n.advance(clock, 6*time.Second)
	}
	flap()
	flap()
	if len(n.got) != 1 {
		t.Fatalf("alerts %v inside a minute", n.got)
	}
	n.advance(clock, 40*time.Second)
	flap()
	if len(n.got) != 2 || n.got[0].ID != n.got[1].ID {
		t.Fatalf("alerts %v after the minute, want a second with the same ID", n.got)
	}
}

func TestTwoFleetsAreNotifiedTenSecondsApart(t *testing.T) {
	f := quietPair(t)
	app, clock, n := notifyApp(t, f)
	n.advance(clock, 10*time.Second)
	app.SetFocused(false)
	setNeeds(f, fleetID, blockedNeed)
	n.advance(clock, 5*time.Second)
	setAttn(f, secondID, blockedNeed)
	n.advance(clock, 5*time.Second)
	if len(n.got) != 1 || n.got[0].ID != "attn:"+fleetID {
		t.Fatalf("alerts %v inside ten seconds", n.got)
	}
	setAttn(f, secondID, desktop.Needs{})
	n.advance(clock, 10*time.Second)
	setAttn(f, secondID, blockedNeed)
	n.advance(clock, 10*time.Second)
	if len(n.got) != 2 || n.got[1].ID != "attn:"+secondID {
		t.Fatalf("alerts %v, want the second fleet after the gap", n.got)
	}
}

func TestANotificationCarriesNoAskOrCommandText(t *testing.T) {
	f := quietPair(t)
	app, clock, n := notifyApp(t, f)
	n.advance(clock, 10*time.Second)
	app.SetFocused(false)
	setNeeds(f, fleetID, desktop.Needs{Waiting: 1, Worst: "blocked", WorstWord: "blocked", Items: []desktop.Need{{
		Kind: "blocked", Ref: "s1", Title: "Bash: rm -rf /srv", Headline: "rm -rf /srv", Excerpt: "token=hunter2 rm -rf", Reason: "rm -rf",
	}}})
	n.advance(clock, 10*time.Second)
	if len(n.got) != 1 {
		t.Fatalf("alerts %v", n.got)
	}
	if all := n.got[0].Title + n.got[0].Body + n.got[0].ID; strings.Contains(all, "rm -rf") || strings.Contains(all, "hunter2") {
		t.Fatalf("alert leaks the ask: %v", n.got[0])
	}
}

func TestARefThatIsNotARefIsLeftOutOfTheBody(t *testing.T) {
	f := quietPair(t)
	app, clock, n := notifyApp(t, f)
	n.advance(clock, 10*time.Second)
	app.SetFocused(false)
	setNeeds(f, fleetID, blockedBy("s1 rm -rf /"))
	n.advance(clock, 10*time.Second)
	if len(n.got) != 1 || n.got[0].Body != "A screen is blocked" {
		t.Fatalf("alerts %v", n.got)
	}
}

func TestNoNotificationWhereTheSystemOffersNone(t *testing.T) {
	f := quietPair(t)
	app, clock, n := notifyApp(t, f, func(a *desktop.App) { a.NotifyOK = func() bool { return false } })
	n.advance(clock, 10*time.Second)
	app.SetFocused(false)
	setNeeds(f, fleetID, blockedNeed)
	n.advance(clock, 10*time.Second)
	if len(n.got) != 0 {
		t.Fatalf("notified %v where notifications are not available", n.got)
	}
}

func TestFramesNeverNotify(t *testing.T) {
	f := quietPair(t)
	app, clock, n := notifyApp(t, f)
	tt := sized(t, app, windowWidth, windowHeight, false)
	n.advance(clock, 10*time.Second)
	app.SetFocused(false)
	setNeeds(f, fleetID, blockedNeed)
	n.advance(clock, 10*time.Second)
	sent, reads := len(n.got), f.count("attention")
	for range 200 {
		tt.Frame()
	}
	if len(n.got) != sent || f.count("attention") != reads {
		t.Fatalf("200 frames notified %d times and read %d times", len(n.got)-sent, f.count("attention")-reads)
	}
}

func TestClickingANotificationOpensItsFleetsBoard(t *testing.T) {
	f := quietPair(t)
	app, _, _ := notifyApp(t, f)
	for _, from := range []string{secondID, fleetID} {
		app.SelectFleet(from)
		app.Show(desktop.TasksScreen)
		if !app.OpenAttention("attn:" + fleetID) {
			t.Fatal("a notification of a known fleet was refused")
		}
		if app.Fleet.ID != fleetID || app.Screen != desktop.BoardScreen {
			t.Fatalf("from %q: fleet %q screen %v after the click", from, app.Fleet.ID, app.Screen)
		}
	}
}

func TestAHostileNotificationIDChangesNothing(t *testing.T) {
	for _, id := range []string{
		"", "attn:", "attn:../x", "attn:" + strings.ToUpper(fleetID), "attn:fffffffffffff",
		"attn:" + fleetID + "/x", fleetID, "x:" + fleetID, "attn:attn:" + fleetID, " attn:" + fleetID,
	} {
		t.Run(id, func(t *testing.T) {
			f := quietPair(t)
			app, _, _ := notifyApp(t, f)
			app.SelectFleet(secondID)
			app.Show(desktop.TasksScreen)
			if app.OpenAttention(id) {
				t.Fatalf("%q was accepted", id)
			}
			if app.Fleet.ID != secondID || app.Screen != desktop.TasksScreen {
				t.Fatalf("fleet %q screen %v after %q", app.Fleet.ID, app.Screen, id)
			}
		})
	}
}

func TestAFleetThatLeftTheRailCannotBeOpenedFromANotification(t *testing.T) {
	f := quietPair(t)
	app, _, _ := notifyApp(t, f)
	if app.OpenAttention("attn:f0000000000ff") {
		t.Fatal("an unknown fleet was opened")
	}
}

func TestTheNotifySettingIsOffInAnOldPrefsFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(desktop.PrefsPath(dir), []byte(`{"zoom":1.25,"chat":400}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if app := booted(t, dir); app.Notify {
		t.Fatal("an old prefs.json without the field loads as on")
	}
}

func TestTheNotifySettingRoundTripsThroughPrefs(t *testing.T) {
	dir := t.TempDir()
	app := booted(t, dir)
	app.SetNotify(true)
	raw, err := os.ReadFile(desktop.PrefsPath(dir))
	if err != nil {
		t.Fatalf("no preferences file: %v", err)
	}
	var stored struct {
		Notify bool `json:"notify"`
	}
	if json.Unmarshal(raw, &stored) != nil || !stored.Notify {
		t.Fatalf("preferences hold %q, want notify true", raw)
	}
	if !booted(t, dir).Notify {
		t.Fatal("the restarted window lost the setting")
	}
	app.SetNotify(false)
	if booted(t, dir).Notify {
		t.Fatal("turning it off was not stored")
	}
}

func TestAHostilePrefsFileCannotTurnNotifyOn(t *testing.T) {
	dir := t.TempDir()
	pad := strings.Repeat(" ", 2048)
	if err := os.WriteFile(desktop.PrefsPath(dir), []byte(`{"notify":true`+pad+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if booted(t, dir).Notify {
		t.Fatal("a prefs file over 1 KiB was read")
	}
}

func TestTheOverflowMenuHoldsTheNotifySetting(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			dir := t.TempDir()
			app := booted(t, dir)
			app.NotifyOK = func() bool { return true }
			app.StartRefresh()
			defer app.Close()
			tt := sized(t, app, windowWidth, windowHeight, dark)
			click(t, tt, "More")
			mustText(t, tt, "Notify when a screen is blocked")
			click(t, tt, "Notify when a screen is blocked")
			if !app.Notify {
				t.Fatal("choosing the item did not turn the setting on")
			}
			click(t, tt, "More")
			click(t, tt, "Notify when a screen is blocked")
			if app.Notify {
				t.Fatal("choosing it again did not turn the setting off")
			}
		})
	}
}

func TestTheSettingIsDisabledWhereNotificationsAreNotAvailable(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			dir := t.TempDir()
			app := booted(t, dir)
			app.Notify = true
			app.StartRefresh()
			defer app.Close()
			tt := sized(t, app, windowWidth, windowHeight, dark)
			click(t, tt, "More")
			mustText(t, tt, "Notifications are not available here")
			mustNotText(t, tt, "Notify when a screen is blocked")
			_ = tt.Click("Notifications are not available here")
			if !app.Notify {
				t.Fatal("a disabled item changed the setting")
			}
		})
	}
}

func TestADeniedNotificationTurnsTheSettingOffAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	app := booted(t, dir)
	app.NotifyOK = func() bool { return true }
	app.StartRefresh()
	defer app.Close()
	app.SetNotify(true)
	app.NotifyDenied()
	if app.Notify || booted(t, dir).Notify {
		t.Fatal("a denial left the setting on")
	}
	tt := sized(t, app, windowWidth, windowHeight, false)
	click(t, tt, "More")
	mustText(t, tt, "Notifications are blocked in system settings")
	click(t, tt, "Notify when a screen is blocked")
	click(t, tt, "More")
	mustNotText(t, tt, "Notifications are blocked in system settings")
}

func TestScenesTheNotifySettingInTheOverflowMenu(t *testing.T) {
	for _, tc := range []struct {
		state     string
		on, avail bool
		want      string
	}{
		{"off", false, true, "Notify when a screen is blocked"},
		{"on", true, true, "Notify when a screen is blocked"},
		{"unavailable", false, false, "Notifications are not available here"},
	} {
		themed(t, "85-notify-menu-"+tc.state, func(t *testing.T, dark bool, name string) {
			app := newApp(t, goldenFixture(t))
			app.Notify = tc.on
			app.NotifyOK = func() bool { return tc.avail }
			app.StartRefresh()
			defer app.Close()
			tt := newTester(app)
			tt.SetDark(dark)
			click(t, tt, "More")
			mustText(t, tt, tc.want, "Refresh now")
			shot(t, tt, name)
		})
	}
}
