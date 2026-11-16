package desktop_test

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/kit"
)

func fullModels() desktop.Models {
	return desktop.Models{
		Harnesses: []desktop.Harness{
			{
				Name:    "claude",
				Efforts: []string{"low", "medium", "high", "xhigh", "max"},
				Groups: []desktop.ModelGroup{{Label: "built-in", Models: []desktop.ModelOption{
					{Name: "opus", Text: "Opus 5.5 · opus", Efforts: []string{"low", "medium", "high", "xhigh", "max"}},
					{Name: "sonnet", Text: "Sonnet 5.5 · sonnet", Efforts: []string{"low", "medium", "high", "xhigh", "max"}, Selected: true},
					{Name: "haiku", Text: "Haiku 5.5", Efforts: []string{"low", "medium", "high", "xhigh", "max"}},
					{Name: "fable", Text: "Fable · fable", Efforts: []string{"low", "medium", "high", "xhigh", "max"}},
				}}},
			},
			{
				Name:    "codex",
				Efforts: []string{"low", "medium", "high"},
				Note:    "listed 2 h ago",
				Groups: []desktop.ModelGroup{{Label: "built-in", Models: []desktop.ModelOption{
					{Name: "gpt-6-luna", Text: "GPT-6 Luna · gpt-6-luna", Efforts: []string{"low", "medium", "high"}},
					{Name: "gpt-6-sol", Text: "GPT-6 Sol · gpt-6-sol", Efforts: []string{"low", "medium", "high"}},
				}}},
			},
			{
				Name: "opencode",
				Groups: []desktop.ModelGroup{{Label: "discovered", Models: []desktop.ModelOption{
					{Name: "anthropic/sonnet", Text: "anthropic/sonnet"},
					{Name: "acme/fast", Text: "Acme Fast · acme/fast", Unverified: true},
				}}},
			},
			{
				Name: "agy",
				Groups: []desktop.ModelGroup{{Label: "built-in", Models: []desktop.ModelOption{
					{Name: "gemini-pro", Text: "Gemini Pro · gemini-pro"},
				}}},
			},
			{Name: "omp", Missing: "omp not found on the board's PATH; restart the board after installing it"},
		},
	}
}

func firstRunModels() desktop.Models {
	m := fullModels()
	m.Current, m.Model, m.Effort, m.Switchable = "", "", "", false
	for i := range m.Harnesses {
		m.Harnesses[i].Model, m.Harnesses[i].Effort = "", ""
	}
	return m
}

func menuFixture(t *testing.T, change func(*fixture)) *fixture {
	t.Helper()
	f := goldenFixture(t)
	ms := fullModels()
	st := f.status[fleetID]
	if st.Supervisor != nil {
		ms.Current, ms.Model, ms.Effort = st.Supervisor.Harness, st.Supervisor.Model, st.Supervisor.Effort
	}
	ms.Switchable = st.Supervisor != nil && st.Supervisor.Status == "running"
	f.models[fleetID] = ms
	if change != nil {
		change(f)
	}
	return f
}

func idleFixture(f *fixture) {
	st := f.status[fleetID]
	st.AgentState = "ready"
	f.status[fleetID] = st
}

func openMenu(t *testing.T, tt *ui.Tester, f *fixture) {
	t.Helper()
	click(t, tt, "sonnet · low")
	tt.Frame()
}

func TestScenesTheModelMenu(t *testing.T) {
	scenes := []struct {
		name   string
		change func(*fixture)
		play   func(t *testing.T, tt *ui.Tester, f *fixture)
		want   []string
	}{
		{"full", nil, openMenu,
			[]string{"Running now", "Harness", "claude", "Model", "sonnet", "Effort", "Change model or effort",
				"Other model", "Switch after this turn", "Switch the supervisor to another harness",
				"codex", "Switch to codex", "opencode", "Switch to opencode", "agy", "Switch to agy", "omp",
				"omp not found on the board's PATH; restart the board after installing it"}},
		{"cold", func(f *fixture) {
			ms := f.models[fleetID]
			ms.Harnesses[0].Note = "refresh failed: no model list yet; run `hand models --refresh claude`"
			ms.Harnesses[1].Note = "listed 2 h ago"
			f.models[fleetID] = ms
		}, openMenu, []string{
			"refresh failed: no model list yet; run `hand models --refresh claude`",
			"listed 2 h ago",
			"Switch to codex",
		}},
		{"now", func(f *fixture) {
			st := f.status[fleetID]
			st.AgentState = "ready"
			f.status[fleetID] = st
		}, openMenu, []string{"Switch now", "keep sonnet", "keep low"}},
		{"after", nil, openMenu, []string{"Switch after this turn", "keep sonnet", "keep low"}},
		{"pending", func(f *fixture) {
			ms := f.models[fleetID]
			ms.Switching = "opus high"
			f.models[fleetID] = ms
		}, nil, []string{"next turn: opus high", "Cancel switch", "Interrupt", "Stop"}},
		{"cancel", func(f *fixture) {
			ms := f.models[fleetID]
			ms.Switching = "opus high"
			f.models[fleetID] = ms
		}, func(t *testing.T, tt *ui.Tester, f *fixture) {
			click(t, tt, "Cancel switch")
			tt.Frame()
		}, []string{"Switch canceled"}},
		{"drop", func(f *fixture) {
			st := f.status[fleetID]
			sup := *st.Supervisor
			sup.Status, sup.Live, sup.Ref = "stopped", false, "s2"
			st.Supervisor = &sup
			f.status[fleetID] = st
			ms := f.models[fleetID]
			ms.Current, ms.Switchable, ms.Drop, ms.DropRef = "", false, "opus high", "s1"
			f.models[fleetID] = ms
		}, func(t *testing.T, tt *ui.Tester, f *fixture) {
			click(t, tt, "Drop switch")
			tt.Frame()
		}, []string{"Drop switch", "Switch canceled"}},
		{"error", func(f *fixture) {
			f.modelsErr = &desktop.APIError{Status: 500, Code: "internal",
				Message: "the models could not be read: the store went away"}
		}, openMenu, []string{"The models could not be read",
			"the models could not be read: the store went away", "Retry"}},
		{"first-run", func(f *fixture) {
			st := f.status[fleetID]
			st.Supervisor = nil
			f.status[fleetID] = st
			f.models[fleetID] = firstRunModels()
			f.timeline[fleetID] = desktop.Timeline{}
		}, nil, []string{"Other harness, model or effort", "Start claude", "Start codex",
			"Start opencode", "Start agy", "Start omp", "omp not found on the board's PATH; restart the board after installing it"}},
		{"stop-armed", nil, func(t *testing.T, tt *ui.Tester, f *fixture) {
			click(t, tt, "Stop")
			tt.Frame()
		}, []string{"Press again to stop"}},
		{"stop", nil, nil, []string{"Stop"}},
	}
	for _, dark := range []bool{false, true} {
		mode := map[bool]string{false: "light", true: "dark"}[dark]
		for _, sc := range scenes {
			t.Run(sc.name+"-"+mode, func(t *testing.T) {
				f := menuFixture(t, sc.change)
				tt := darkTester(newApp(t, f), dark)
				if sc.play != nil {
					sc.play(t, tt, f)
				}
				tt.Frame()
				shot(t, tt, "42-menu-"+sc.name+"-"+mode)
				for _, s := range sc.want {
					if !tt.HasText(s) {
						t.Errorf("missing %q in %q", s, tt.Texts())
					}
				}
			})
		}
	}
}

func TestTheModelMenuIsTheBoardsPanelOverTheChat(t *testing.T) {
	f := menuFixture(t, nil)
	tt := newTester(newApp(t, f))
	openMenu(t, tt, f)
	r, ok := tt.Find("Running now")
	if !ok {
		t.Fatalf("no menu; the window shows %q", tt.Texts())
	}
	img, tone := tt.Image(), kit.ColorsFor(false).BG
	edged := false
	for y := int(r.Y) - 4; y < int(r.Y) && !edged; y++ {
		run := 0
		for x := int(r.X) - 8; x < 1100 && run < 50; x++ {
			if sameInk(img.RGBAAt(x, y), tone) {
				run++
			} else {
				run = 0
			}
		}
		edged = run >= 50
	}
	if !edged {
		t.Errorf("the menu has no 50 px panel of %v above %q", tone, "Running now")
	}
}

func TestTheModelPillShowsTheRunningHarnessAndOpensItsMenu(t *testing.T) {
	f := menuFixture(t, nil)
	tt := newTester(newApp(t, f))
	mustText(t, tt, "sonnet · low")
	mustNotText(t, tt, "Running now")
	openMenu(t, tt, f)
	mustText(t, tt, "Running now", "claude", "sonnet", "low", "Switch after this turn")
}

func TestTheMenuSwitchesTheModelWithTheRunningRef(t *testing.T) {
	f := menuFixture(t, idleFixture)
	tt := newTester(newApp(t, f))
	openMenu(t, tt, f)
	click(t, tt, "keep sonnet")
	click(t, tt, "Opus 5.5 · opus")
	click(t, tt, "keep low")
	click(t, tt, "high")
	click(t, tt, "Switch now")
	tt.Frame()
	f.mu.Lock()
	sw := f.switchReq
	f.mu.Unlock()
	if sw.Ref != "s1" || sw.Model != "opus" || sw.Effort != "high" || sw.Cancel || sw.Harness != "" {
		t.Fatalf("switch sent %+v", sw)
	}
	mustText(t, tt, "Switch applied")
	mustNotText(t, tt, "Running now")
}

func TestTheMenuSwitchesTheHarnessWithItsModelAndEffort(t *testing.T) {
	f := menuFixture(t, func(f *fixture) {
		ms := f.models[fleetID]
		ms.Harnesses = ms.Harnesses[:2]
		f.models[fleetID] = ms
	})
	tt := newTester(newApp(t, f))
	openMenu(t, tt, f)
	tt.Scroll(860, 400, 0, 300)
	tt.Frame()
	click(t, tt, "Switch to codex")
	tt.Frame()
	f.mu.Lock()
	sw := f.switchReq
	f.mu.Unlock()
	if sw.Ref != "s1" || sw.Harness != "codex" || sw.Model != "" || sw.Effort != "" {
		t.Fatalf("switch sent %+v", sw)
	}
	mustText(t, tt, "Switching to codex…")
}

func TestThePendingSwitchCancelPostsTheSwitchOwnersRef(t *testing.T) {
	f := menuFixture(t, func(f *fixture) {
		ms := f.models[fleetID]
		ms.Switching, ms.DropRef = "opus high", "s7"
		f.models[fleetID] = ms
	})
	tt := newTester(newApp(t, f))
	click(t, tt, "Cancel switch")
	tt.Frame()
	f.mu.Lock()
	sw := f.switchReq
	f.mu.Unlock()
	if !sw.Cancel || sw.Ref != "s7" {
		t.Fatalf("cancel sent %+v, want the switch owner's s7", sw)
	}
	mustText(t, tt, "Switch canceled")
}

func TestTheDropSwitchPostsTheRefThePendingSwitchBelongsTo(t *testing.T) {
	f := menuFixture(t, func(f *fixture) {
		st := f.status[fleetID]
		sup := *st.Supervisor
		sup.Status, sup.Live, sup.Resumable, sup.Ref = "failed", false, false, "s2"
		st.Supervisor = &sup
		f.status[fleetID] = st
		ms := f.models[fleetID]
		ms.Current, ms.Switchable, ms.Drop, ms.DropRef = "", false, "opus high", "s1"
		f.models[fleetID] = ms
	})
	tt := newTester(newApp(t, f))
	click(t, tt, "Drop switch")
	tt.Frame()
	f.mu.Lock()
	sw := f.switchReq
	f.mu.Unlock()
	if !sw.Cancel || sw.Ref != "s1" {
		t.Fatalf("drop sent %+v, want s1", sw)
	}
	mustText(t, tt, "Switch canceled")
}

func TestTheFirstRunStripStartsTheChosenHarness(t *testing.T) {
	f := menuFixture(t, func(f *fixture) {
		st := f.status[fleetID]
		st.Supervisor = nil
		f.status[fleetID] = st
		f.models[fleetID] = firstRunModels()
	})
	tt := newTester(newApp(t, f))
	mustText(t, tt, "Other harness, model or effort", "Start claude", "Start codex", "Start opencode", "Start agy", "Start omp")
	mustNotText(t, tt, "Open in browser")
	click(t, tt, "default")
	click(t, tt, "Opus 5.5 · opus")
	click(t, tt, "default")
	click(t, tt, "high")
	click(t, tt, "Start claude")
	tt.Frame()
	f.mu.Lock()
	harness, model, effort := f.startHarness, f.startModel, f.startEffort
	f.mu.Unlock()
	if harness != "claude" || model != "opus" || effort != "high" {
		t.Fatalf("start sent %q %q %q", harness, model, effort)
	}
	mustText(t, tt, "Starting the supervisor…")
}

func TestTheFirstRunStripStartsAHarnessOnItsDefaultModel(t *testing.T) {
	f := menuFixture(t, func(f *fixture) {
		st := f.status[fleetID]
		st.Supervisor = nil
		f.status[fleetID] = st
		f.models[fleetID] = firstRunModels()
	})
	tt := newTester(newApp(t, f))
	click(t, tt, "Start omp")
	if f.count("start:omp") != 0 {
		t.Fatal("a missing harness started")
	}
	click(t, tt, "Start opencode")
	f.mu.Lock()
	harness, model, effort := f.startHarness, f.startModel, f.startEffort
	f.mu.Unlock()
	if harness != "opencode" || model != "" || effort != "" {
		t.Fatalf("start sent %q %q %q", harness, model, effort)
	}
}

func TestTheStartStripSendsAVariantTheListDoesNotHold(t *testing.T) {
	f := menuFixture(t, func(f *fixture) {
		st := f.status[fleetID]
		st.Supervisor = nil
		f.status[fleetID] = st
		f.models[fleetID] = firstRunModels()
	})
	tt := newTester(newApp(t, f))
	click(t, tt, "opencode variant")
	tt.Type("thinking")
	click(t, tt, "Start opencode")
	tt.Frame()
	f.mu.Lock()
	harness, model, effort := f.startHarness, f.startModel, f.startEffort
	f.mu.Unlock()
	if harness != "opencode" || model != "" || effort != "thinking" {
		t.Fatalf("start sent %q %q %q", harness, model, effort)
	}
}

func TestTheStartStripSendsAModelTypedForAHarnessWithoutAList(t *testing.T) {
	f := menuFixture(t, func(f *fixture) {
		st := f.status[fleetID]
		st.Supervisor = nil
		f.status[fleetID] = st
		ms := firstRunModels()
		ms.Harnesses[1].Groups, ms.Harnesses[1].Efforts = nil, nil
		f.models[fleetID] = ms
	})
	tt := newTester(newApp(t, f))
	mustText(t, tt, "Start codex")
	click(t, tt, "codex model")
	tt.Type("gpt-6-luna")
	click(t, tt, "codex effort")
	tt.Type("high")
	click(t, tt, "Start codex")
	tt.Frame()
	f.mu.Lock()
	harness, model, effort := f.startHarness, f.startModel, f.startEffort
	f.mu.Unlock()
	if harness != "codex" || model != "gpt-6-luna" || effort != "high" {
		t.Fatalf("start sent %q %q %q", harness, model, effort)
	}
}

func TestSwitchingFleetsWithAModelReadInFlightLoadsTheNewFleets(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	f.mu.Lock()
	f.modelsBlock, f.modelsBlockFor, f.modelsEntered = make(chan struct{}), fleetID, make(chan struct{}, 4)
	f.mu.Unlock()
	tt := sized(t, app, 1100, 760, false)
	click(t, tt, "sonnet · low")
	select {
	case <-f.modelsEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the window never read the first fleet's models")
	}
	app.SelectFleet(secondID)
	waitPost(t, posted)()
	waitPost(t, posted)()
	waitPost(t, posted)()
	if got := f.count("models:" + secondID); got == 0 {
		t.Fatalf("the second fleet's models were never read: %q", tt.Texts())
	}
	close(f.modelsBlock)
	waitPost(t, posted)()
	tt.Frame()
	if app.Fleet.ID != secondID {
		t.Fatalf("fleet %s", app.Fleet.ID)
	}
}

func TestAControlDuringAModelReadReadsTheModelsAgain(t *testing.T) {
	f := menuFixture(t, nil)
	app := newApp(t, f)
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	f.mu.Lock()
	f.modelsBlock, f.modelsBlockFor, f.modelsEntered = make(chan struct{}), fleetID, make(chan struct{}, 4)
	f.mu.Unlock()
	tt := sized(t, app, 1100, 760, false)
	click(t, tt, "sonnet · low")
	select {
	case <-f.modelsEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the window never read the models")
	}
	reads := f.count("models:" + fleetID)
	click(t, tt, "Switch after this turn")
	waitPost(t, posted)()
	waitPost(t, posted)()
	close(f.modelsBlock)
	waitPost(t, posted)()
	waitPost(t, posted)()
	if got := f.count("models:" + fleetID); got < reads+1 {
		t.Fatalf("the models were read %d times, want a follow-up after the read in flight", got-reads)
	}
}

func TestRefreshModelsReportsTheCooldownRatherThanHanging(t *testing.T) {
	f := menuFixture(t, nil)
	tt := newTester(newApp(t, f))
	const cooldown = "claude models were refreshed 12 s ago; try again in a moment"
	f.refreshErr = &desktop.APIError{Status: 429, Code: "rate_limited", Message: cooldown}
	menu(t, tt, "Refresh models")
	tt.Frame()
	mustText(t, tt, cooldown)
	if app := f.count("refresh:"); app != 1 {
		t.Fatalf("refresh called %d times", app)
	}
}

func TestRefreshModelsSaysTheBoardsReceipt(t *testing.T) {
	f := menuFixture(t, nil)
	tt := newTester(newApp(t, f))
	menu(t, tt, "Refresh models")
	tt.Frame()
	mustText(t, tt, "Refreshing models…")
}

func TestStopDisarmsWhenAnotherControlRuns(t *testing.T) {
	f := menuFixture(t, nil)
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	tt := newTester(app)
	click(t, tt, "Stop")
	mustText(t, tt, "Press again to stop")
	click(t, tt, "Interrupt")
	tt.Frame()
	mustNotText(t, tt, "Press again to stop")
	if app.StopArmed {
		t.Fatal("Stop stayed armed through Interrupt")
	}
	click(t, tt, "Stop")
	if f.count("control:stop") != 0 {
		t.Fatal("the disarmed Stop stopped the supervisor")
	}
}

func TestStopDisarmsWhenTheSupervisorChanges(t *testing.T) {
	f := menuFixture(t, nil)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Stop")
	mustText(t, tt, "Press again to stop")
	st := f.status[fleetID]
	sup := *st.Supervisor
	sup.Ref = "s2"
	st.Supervisor = &sup
	f.status[fleetID] = st
	menu(t, tt, "Refresh now")
	tt.Frame()
	mustNotText(t, tt, "Press again to stop")
	if app.StopArmed {
		t.Fatal("Stop stayed armed for the supervisor that left")
	}
}

func TestStopStillDisarmsOnItsOwnWindow(t *testing.T) {
	f := menuFixture(t, nil)
	app := newApp(t, f)
	clock := newFakeClock()
	app.Clock = clock
	tt := newTester(app)
	click(t, tt, "Stop")
	clock.Advance(desktop.StopWindow - time.Millisecond)
	tt.Frame()
	mustText(t, tt, "Press again to stop")
	clock.Advance(time.Millisecond)
	tt.Frame()
	mustNotText(t, tt, "Press again to stop")
}
