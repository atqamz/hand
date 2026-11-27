package desktop_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/kit"
	"github.com/atqamz/hand/internal/harness"
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
			[]string{"Running now", "Harness", "claude", "Model", "sonnet", "Effort", "Change harness, model or effort",
				"claude (running)", "Other model", "Switch after this turn", "Unavailable",
				"omp: omp not found on the board's PATH; restart the board after installing it"}},
		{"cold", func(f *fixture) {
			ms := f.models[fleetID]
			ms.Harnesses[0].Note = "refresh failed: no model list yet; run `hand models --refresh claude`"
			f.models[fleetID] = ms
		}, openMenu, []string{
			"refresh failed: no model list yet; run `hand models --refresh claude`",
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
		}, nil, []string{"next turn: opus · high", "Cancel switch", "Interrupt", "Stop"}},
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
		}, nil, []string{"Other harness, model or effort", "Start claude", "codex",
			"opencode", "agy", "omp", "omp not found on the board's PATH; restart the board after installing it"}},
		{"first-run-codex", func(f *fixture) {
			st := f.status[fleetID]
			st.Supervisor = nil
			f.status[fleetID] = st
			f.models[fleetID] = firstRunModels()
			f.timeline[fleetID] = desktop.Timeline{}
		}, func(t *testing.T, tt *ui.Tester, f *fixture) {
			click(t, tt, "codex")
			tt.Frame()
		}, []string{"Start codex", "claude", "opencode"}},
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

func pickHarness(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if tt.HasText("claude (running)") {
		click(t, tt, "claude (running)")
	} else {
		click(t, tt, "codex")
	}
	click(t, tt, name)
	tt.Frame()
}

func lastSwitch(f *fixture) desktop.Switch {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.switchReq
}

func TestTheMenuSwitchesTheHarnessWithItsOwnModelAndEffort(t *testing.T) {
	f := menuFixture(t, idleFixture)
	tt := newTester(newApp(t, f))
	openMenu(t, tt, f)
	click(t, tt, "keep sonnet")
	click(t, tt, "Opus 5.5 · opus")
	pickHarness(t, tt, "codex")
	mustText(t, tt, "GPT-6 Luna · gpt-6-luna", "Switch to codex", "Stops s1 and starts a new session on codex; s1's conversation stays in Chat.")
	mustNotText(t, tt, "Opus 5.5 · opus")
	mustNotText(t, tt, "Switch now")
	click(t, tt, "default")
	mustText(t, tt, "low", "medium", "high")
	mustNotText(t, tt, "xhigh")
	mustNotText(t, tt, "max")
	click(t, tt, "high")
	click(t, tt, "Switch to codex")
	tt.Frame()
	if sw := lastSwitch(f); sw.Ref != "s1" || sw.Harness != "codex" || sw.Model != "gpt-6-luna" || sw.Effort != "high" {
		t.Fatalf("switch sent %+v", sw)
	}
	mustText(t, tt, "Switching to codex…")
}

func TestTheHarnessChoiceNeverKeepsTheModelOfTheHarnessBefore(t *testing.T) {
	f := menuFixture(t, idleFixture)
	tt := newTester(newApp(t, f))
	openMenu(t, tt, f)
	click(t, tt, "keep sonnet")
	click(t, tt, "Opus 5.5 · opus")
	click(t, tt, "keep low")
	click(t, tt, "max")
	pickHarness(t, tt, "codex")
	click(t, tt, "GPT-6 Luna · gpt-6-luna")
	click(t, tt, "GPT-6 Sol · gpt-6-sol")
	pickHarness(t, tt, "opencode")
	mustText(t, tt, "default")
	mustNotText(t, tt, "GPT-6 Sol · gpt-6-sol")
	mustNotText(t, tt, "max")
	click(t, tt, "Switch to opencode")
	tt.Frame()
	if sw := lastSwitch(f); sw.Harness != "opencode" || sw.Model != "" || sw.Effort != "" {
		t.Fatalf("switch sent %+v, want opencode on its own configuration", sw)
	}
}

func TestTheHarnessChoiceOffersOnlyHarnessesASupervisorCanRun(t *testing.T) {
	f := menuFixture(t, func(f *fixture) {
		ms := f.models[fleetID]
		ms.Harnesses = append(ms.Harnesses, desktop.Harness{Name: "copilot", Groups: []desktop.ModelGroup{{Label: "built-in",
			Models: []desktop.ModelOption{{Name: "auto", Text: "auto"}}}}})
		f.models[fleetID] = ms
	})
	tt := newTester(newApp(t, f))
	openMenu(t, tt, f)
	click(t, tt, "claude (running)")
	mustText(t, tt, "codex", "opencode", "agy")
	mustNotText(t, tt, "copilot")
	mustNotText(t, tt, "Switch to omp")
	mustNotText(t, tt, "Switch to copilot")
	mustText(t, tt, "omp: omp not found on the board's PATH; restart the board after installing it")
}

func TestTheHarnessSwitchWaitsForTheTurnAndSaysWhy(t *testing.T) {
	f := menuFixture(t, nil)
	tt := newTester(newApp(t, f))
	openMenu(t, tt, f)
	pickHarness(t, tt, "codex")
	mustText(t, tt, "s1 is working; switch harness when its turn ends, or interrupt it first")
	click(t, tt, "Switch to codex")
	tt.Frame()
	if f.count("switch") != 0 {
		t.Fatal("a harness switch was sent while the supervisor works")
	}
}

func TestTheHarnessSwitchNeedsAModelUnlessTheHarnessHasItsOwn(t *testing.T) {
	f := menuFixture(t, func(f *fixture) {
		idleFixture(f)
		ms := f.models[fleetID]
		ms.Harnesses[3].Groups = nil
		f.models[fleetID] = ms
	})
	tt := newTester(newApp(t, f))
	openMenu(t, tt, f)
	pickHarness(t, tt, "agy")
	click(t, tt, "Switch to agy")
	tt.Frame()
	if f.count("switch") != 0 {
		t.Fatal("a harness switch without a model was sent")
	}
	click(t, tt, "agy model")
	tt.Type("gemini-pro")
	click(t, tt, "Switch to agy")
	tt.Frame()
	if sw := lastSwitch(f); sw.Harness != "agy" || sw.Model != "gemini-pro" {
		t.Fatalf("switch sent %+v", sw)
	}
}

func TestScenesTheHarnessChoice(t *testing.T) {
	scenes := []struct {
		name   string
		change func(*fixture)
		harn   string
		want   []string
	}{
		{"codex", idleFixture, "codex", []string{"Harness", "codex", "GPT-6 Luna · gpt-6-luna", "Switch to codex",
			"Stops s1 and starts a new session on codex; s1's conversation stays in Chat.", "listed 2 h ago"}},
		{"working", nil, "codex", []string{"Switch to codex", "s1 is working; switch harness when its turn ends, or interrupt it first"}},
		{"opencode", idleFixture, "opencode", []string{"opencode", "default", "Switch to opencode"}},
		{"blocked", blockedMenu, "codex", []string{"Switch to codex", "s1 is blocked; answer its screen or wait until it is idle, then switch harness"}},
	}
	for _, dark := range []bool{false, true} {
		mode := map[bool]string{false: "light", true: "dark"}[dark]
		for _, sc := range scenes {
			t.Run(sc.name+"-"+mode, func(t *testing.T) {
				f := menuFixture(t, sc.change)
				tt := darkTester(newApp(t, f), dark)
				openMenu(t, tt, f)
				pickHarness(t, tt, sc.harn)
				shot(t, tt, "87-harness-"+sc.name+"-"+mode)
				for _, s := range sc.want {
					if !tt.HasText(s) {
						t.Errorf("missing %q in %q", s, tt.Texts())
					}
				}
			})
		}
	}
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

func openStart(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if !tt.HasText("Start " + name) {
		click(t, tt, name)
		tt.Frame()
	}
}

func TestTheFirstRunStripStartsTheChosenHarness(t *testing.T) {
	f := menuFixture(t, func(f *fixture) {
		st := f.status[fleetID]
		st.Supervisor = nil
		f.status[fleetID] = st
		f.models[fleetID] = firstRunModels()
	})
	tt := newTester(newApp(t, f))
	mustText(t, tt, "Other harness, model or effort", "Start claude", "codex", "opencode", "agy", "omp")
	mustNotText(t, tt, "Open in browser")
	click(t, tt, "Opus 5.5 · opus")
	click(t, tt, "Sonnet 5.5 · sonnet")
	click(t, tt, "default")
	click(t, tt, "high")
	click(t, tt, "Start claude")
	tt.Frame()
	f.mu.Lock()
	harness, model, effort := f.startHarness, f.startModel, f.startEffort
	f.mu.Unlock()
	if harness != "claude" || model != "sonnet" || effort != "high" {
		t.Fatalf("start sent %q %q %q", harness, model, effort)
	}
	mustText(t, tt, "Starting the supervisor…")
}

func startableModels(lastHarness string) desktop.Models {
	m := firstRunModels()
	for i := range m.Harnesses {
		h := &m.Harnesses[i]
		h.Missing = ""
		if h.Name == "omp" {
			h.Groups = []desktop.ModelGroup{{Label: "built-in", Models: []desktop.ModelOption{{Name: "omp-fast", Text: "omp-fast"}}}}
		}
		if h.Name == lastHarness {
			h.Model, h.Effort = firstModelOf(*h), ""
		}
	}
	return m
}

func firstModelOf(h desktop.Harness) string {
	return h.Groups[0].Models[len(h.Groups[0].Models)-1].Name
}

func acceptedByStart(t *testing.T, harnessName, model, effort string) {
	t.Helper()
	spec := harness.Spec{Harness: harnessName, Model: model, Effort: effort}
	if err := harness.CheckSupervisor(spec); err != nil {
		t.Errorf("start %q %q %q: %v", harnessName, model, effort, err)
		return
	}
	why, err := harness.Check(spec, harness.Env{Getenv: func(string) string { return "" }})
	if err != nil {
		t.Errorf("start %q %q %q: %v", harnessName, model, effort, err)
		return
	}
	if why.Text != "" && !why.Unread {
		t.Errorf("start %q %q %q: %s", harnessName, model, effort, why.Text)
	}
}

func TestTheStartStripSendsARequestStartAcceptsForEveryHarness(t *testing.T) {
	for _, last := range []string{"first-run", "codex"} {
		for _, name := range []string{"claude", "codex", "agy", "omp", "opencode"} {
			t.Run(last+"-"+name, func(t *testing.T) {
				f := menuFixture(t, func(f *fixture) {
					st := f.status[fleetID]
					st.Supervisor = nil
					f.status[fleetID] = st
					f.models[fleetID] = startableModels(strings.TrimPrefix(last, "first-run"))
				})
				tt := newTester(newApp(t, f))
				openStart(t, tt, name)
				click(t, tt, "Start "+name)
				tt.Frame()
				if f.count("start:"+name) != 1 {
					t.Fatalf("Start %s sent no request", name)
				}
				f.mu.Lock()
				harnessName, model, effort := f.startHarness, f.startModel, f.startEffort
				f.mu.Unlock()
				if harnessName != name {
					t.Fatalf("start sent %q, want %q", harnessName, name)
				}
				if name == last {
					want := firstModelOf(startableModels(last).Harnesses[1])
					if model != want {
						t.Errorf("%s sent %q, want the board's prefilled %q", name, model, want)
					}
				}
				if name == "opencode" && model != "" {
					t.Errorf("opencode sent model %q, want its own default", model)
				}
				acceptedByStart(t, harnessName, model, effort)
			})
		}
	}
}

func TestTheStartStripDoesNotStartAHarnessWithoutAModel(t *testing.T) {
	f := menuFixture(t, func(f *fixture) {
		st := f.status[fleetID]
		st.Supervisor = nil
		f.status[fleetID] = st
		ms := startableModels("")
		ms.Harnesses[1].Groups, ms.Harnesses[1].Efforts = nil, nil
		f.models[fleetID] = ms
	})
	tt := newTester(newApp(t, f))
	openStart(t, tt, "codex")
	click(t, tt, "Start codex")
	tt.Frame()
	if f.count("start:codex") != 0 {
		t.Fatal("Start codex was sent without a model")
	}
}

func TestTheFirstRunStripStartsAHarnessOnItsDefaultModel(t *testing.T) {
	f := menuFixture(t, func(f *fixture) {
		st := f.status[fleetID]
		st.Supervisor = nil
		f.status[fleetID] = st
		f.models[fleetID] = firstRunModels()
	})
	tt := newTester(newApp(t, f))
	openStart(t, tt, "omp")
	click(t, tt, "Start omp")
	if f.count("start:omp") != 0 {
		t.Fatal("a missing harness started")
	}
	openStart(t, tt, "opencode")
	click(t, tt, "Start opencode")
	f.mu.Lock()
	harness, model, effort := f.startHarness, f.startModel, f.startEffort
	f.mu.Unlock()
	if harness != "opencode" || model != "" || effort != "" {
		t.Fatalf("start sent %q %q %q", harness, model, effort)
	}
}

func TestTheStartStripSendsAVariantOnlyWithAModel(t *testing.T) {
	f := menuFixture(t, func(f *fixture) {
		st := f.status[fleetID]
		st.Supervisor = nil
		f.status[fleetID] = st
		ms := firstRunModels()
		ms.Harnesses = []desktop.Harness{ms.Harnesses[2]}
		f.models[fleetID] = ms
	})
	tt := newTester(newApp(t, f))
	mustNotText(t, tt, "opencode variant")
	click(t, tt, "default")
	click(t, tt, "anthropic/sonnet")
	tt.Frame()
	click(t, tt, "opencode variant")
	tt.Type("thinking")
	click(t, tt, "Start opencode")
	tt.Frame()
	f.mu.Lock()
	harnessName, model, effort := f.startHarness, f.startModel, f.startEffort
	f.mu.Unlock()
	if harnessName != "opencode" || model != "anthropic/sonnet" || effort != "thinking" {
		t.Fatalf("start sent %q %q %q", harnessName, model, effort)
	}
	acceptedByStart(t, harnessName, model, effort)
	click(t, tt, "anthropic/sonnet")
	click(t, tt, "default")
	tt.Frame()
	mustNotText(t, tt, "opencode variant")
	click(t, tt, "Start opencode")
	tt.Frame()
	f.mu.Lock()
	harnessName, model, effort = f.startHarness, f.startModel, f.startEffort
	f.mu.Unlock()
	if harnessName != "opencode" || model != "" || effort != "" {
		t.Fatalf("start sent %q %q %q, want no variant without a model", harnessName, model, effort)
	}
	acceptedByStart(t, harnessName, model, effort)
}

func TestTheHarnessSwitchSendsNoVariantWithoutAModel(t *testing.T) {
	f := menuFixture(t, idleFixture)
	tt := newTester(newApp(t, f))
	openMenu(t, tt, f)
	pickHarness(t, tt, "opencode")
	mustNotText(t, tt, "opencode variant")
	click(t, tt, "default")
	click(t, tt, "anthropic/sonnet")
	tt.Frame()
	click(t, tt, "opencode variant")
	tt.Type("thinking")
	click(t, tt, "anthropic/sonnet")
	click(t, tt, "default")
	tt.Frame()
	mustNotText(t, tt, "opencode variant")
	click(t, tt, "Switch to opencode")
	tt.Frame()
	sw := lastSwitch(f)
	if sw.Harness != "opencode" || sw.Model != "" || sw.Effort != "" {
		t.Fatalf("switch sent %+v, want no variant without a model", sw)
	}
	acceptedByStart(t, sw.Harness, sw.Model, sw.Effort)
}

func blockedMenu(f *fixture) {
	st := f.status[fleetID]
	sup := *st.Supervisor
	sup.Blocked = true
	st.Supervisor = &sup
	st.AgentState, st.Pill, st.Line = "blocked", "failing", "blocked"
	f.status[fleetID] = st
}

func TestTheHarnessSwitchWaitsWhileTheSupervisorIsBlockedAndSaysWhy(t *testing.T) {
	f := menuFixture(t, blockedMenu)
	tt := newTester(newApp(t, f))
	openMenu(t, tt, f)
	pickHarness(t, tt, "codex")
	mustText(t, tt, "s1 is blocked; answer its screen or wait until it is idle, then switch harness")
	mustNotText(t, tt, "s1 is working; switch harness when its turn ends, or interrupt it first")
	click(t, tt, "Switch to codex")
	tt.Frame()
	if f.count("switch") != 0 {
		t.Fatal("a harness switch was sent while the supervisor is blocked")
	}
}

func TestTheHarnessSwitchIsOfferedToAHeldSupervisor(t *testing.T) {
	f := menuFixture(t, func(f *fixture) {
		st := f.status[fleetID]
		st.AgentState = "blocked"
		f.status[fleetID] = st
	})
	tt := newTester(newApp(t, f))
	openMenu(t, tt, f)
	pickHarness(t, tt, "codex")
	mustNotText(t, tt, "s1 is blocked; answer its screen or wait until it is idle, then switch harness")
	click(t, tt, "Switch to codex")
	tt.Frame()
	if sw := lastSwitch(f); f.count("switch") != 1 || sw.Harness != "codex" || sw.Model != "gpt-6-luna" {
		t.Fatalf("switch sent %+v", sw)
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
	openStart(t, tt, "codex")
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
	f.modelsBlock, f.modelsBlockFor, f.modelsEntered = make(chan struct{}), fleetID, make(chan struct{}, 8)
	f.mu.Unlock()
	tt := sized(t, app, 1100, 760, false)
	openMenu(t, tt, f)
	select {
	case <-f.modelsEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the window never read the models")
	}
	reads := f.count("models:" + fleetID)
	click(t, tt, "Switch after this turn")
	waitPost(t, posted)()
	waitPost(t, posted)()
	select {
	case <-f.modelsEntered:
		t.Fatal("the control scheduled a read while one was still in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(f.modelsBlock)
	waitPost(t, posted)()
	select {
	case <-f.modelsEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the window never posted the follow-up read")
	}
	select {
	case <-f.modelsEntered:
		t.Fatal("the control scheduled a second follow-up read")
	case <-time.After(50 * time.Millisecond):
	}
	if got := f.count("models:" + fleetID); got != reads+1 {
		t.Fatalf("the models were read %d times, want exactly one follow-up after the read in flight", got-reads)
	}
}

func TestOpeningTheMenuDuringARefreshReadsAfterIt(t *testing.T) {
	f := menuFixture(t, nil)
	app := newApp(t, f)
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	f.mu.Lock()
	f.actionBlock, f.actionEntered = make(chan struct{}), make(chan struct{}, 4)
	f.mu.Unlock()
	tt := newTester(app)
	before := f.count("models:" + fleetID)
	gen := desktop.ModelsGen(app)
	menu(t, tt, "Refresh models")
	select {
	case <-f.actionEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the window never asked the board to refresh the models")
	}
	openMenu(t, tt, f)
	if got := desktop.ModelsGen(app); got != gen+1 {
		t.Fatalf("opening the menu moved the read generation to %d, want the refresh's %d", got, gen+1)
	}
	if got := f.count("models:" + fleetID); got != before {
		t.Fatalf("the menu read the models %d times with a refresh in flight", got-before)
	}
	renameFirstHarness(f, "refreshed")
	close(f.actionBlock)
	waitPost(t, posted)()
	waitPost(t, posted)()
	tt.Frame()
	mustText(t, tt, "Refreshing models…")
	if got := f.count("models:" + fleetID); got != before+1 {
		t.Fatalf("the models were read %d times, want exactly one deferred read after the refresh", got-before)
	}
	if len(app.Models.Harnesses) == 0 || app.Models.Harnesses[0].Name != "refreshed" {
		t.Fatalf("the catalogue after the refresh is %+v, want the refreshed one", app.Models.Harnesses)
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

func TestAnAcceptedRefreshKeepsTheCatalogue(t *testing.T) {
	f := menuFixture(t, nil)
	f.refreshStale = true
	app := newApp(t, f)
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	before := f.count("models:" + fleetID)
	menu(t, tt, "Refresh models")
	waitPost(t, posted)()
	tt.Frame()
	mustText(t, tt, "Refreshing models…")
	if got, want := len(app.Models.Harnesses), len(fullModels().Harnesses); got != want {
		t.Fatalf("an accepted refresh left %d harnesses, want %d", got, want)
	}
	waitPost(t, posted)()
	if got := f.count("models:" + fleetID); got != before+1 {
		t.Fatalf("the models were read %d times, want one follow-up read after an accepted refresh", got-before)
	}
}

func TestALateModelReadAfterTheSupervisorChangedIsDiscarded(t *testing.T) {
	f := menuFixture(t, nil)
	app := newApp(t, f)
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	renameFirstHarness(f, "stale")
	f.mu.Lock()
	f.modelsBlock, f.modelsBlockFor, f.modelsEntered = make(chan struct{}), fleetID, make(chan struct{}, 4)
	f.mu.Unlock()
	openMenu(t, tt, f)
	select {
	case <-f.modelsEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the window never read the models")
	}
	renameFirstHarness(f, "fresh")
	st := f.status[fleetID]
	sup := *st.Supervisor
	sup.Ref = "s2"
	st.Supervisor = &sup
	f.status[fleetID] = st
	app.Reload()
	waitPost(t, posted)()
	waitPost(t, posted)()
	if app.Status.Supervisor == nil || app.Status.Supervisor.Ref != "s2" {
		t.Fatalf("the status did not move to s2: %+v", app.Status.Supervisor)
	}
	close(f.modelsBlock)
	waitPost(t, posted)()
	tt.Frame()
	if len(app.Models.Harnesses) == 0 || app.Models.Harnesses[0].Name != "claude" {
		t.Fatalf("the late answer for s1 was stored after the supervisor became s2: %+v", app.Models.Harnesses)
	}
	mustNotText(t, tt, "stale")
}

func renameFirstHarness(f *fixture, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ms := f.models[fleetID]
	ms.Harnesses = append([]desktop.Harness(nil), ms.Harnesses...)
	ms.Harnesses[0].Name = name
	f.models[fleetID] = ms
}

func TestFormEditsSurviveAModelRead(t *testing.T) {
	f := menuFixture(t, nil)
	app := newApp(t, f)
	tt := newTester(app)
	openMenu(t, tt, f)
	click(t, tt, "keep sonnet")
	click(t, tt, "Opus 5.5 · opus")
	click(t, tt, "keep low")
	click(t, tt, "high")
	before := f.count("models:" + fleetID)
	st := f.status[fleetID]
	sup := *st.Supervisor
	sup.Ref = "s2"
	st.Supervisor = &sup
	f.status[fleetID] = st
	app.Reload()
	if got := f.count("models:" + fleetID); got != before+1 {
		t.Fatalf("the models were read %d times, want one read", got-before)
	}
	tt.Frame()
	click(t, tt, "Switch after this turn")
	tt.Frame()
	f.mu.Lock()
	sw := f.switchReq
	f.mu.Unlock()
	if sw.Model != "opus" || sw.Effort != "high" {
		t.Fatalf("switch sent %+v, want the edited opus high to survive the read", sw)
	}
}

func TestStopDisarmsWhenAKeyIsPressed(t *testing.T) {
	f := blockedScreen(t, true)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Stop")
	mustText(t, tt, "Press again to stop")
	click(t, tt, "1 Yes")
	tt.Frame()
	mustNotText(t, tt, "Press again to stop")
	if app.StopArmed {
		t.Fatal("Stop stayed armed through a key press")
	}
}

func opencodeOnly(efforts []string, list bool) func(*fixture) {
	return func(f *fixture) {
		st := f.status[fleetID]
		st.Supervisor = nil
		f.status[fleetID] = st
		ms := firstRunModels()
		oc := ms.Harnesses[2]
		oc.Efforts = efforts
		if efforts != nil {
			oc.Groups[0].Models[0].Efforts = efforts
		}
		if !list {
			oc.Groups = nil
		}
		ms.Harnesses = []desktop.Harness{oc}
		f.models[fleetID] = ms
	}
}

func lastStart(f *fixture) (string, string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.startHarness, f.startModel, f.startEffort
}

func TestTheStartStripOffersNoEffortSelectWithoutAModelOnRealShapedOpencodeData(t *testing.T) {
	plain := newTester(newApp(t, menuFixture(t, opencodeOnly(nil, true))))
	mustText(t, plain, "Start opencode")
	f := menuFixture(t, opencodeOnly([]string{"high", "max"}, true))
	tt := newTester(newApp(t, f))
	mustText(t, tt, "Start opencode")
	if got, want := countText(t, tt, "default"), countText(t, plain, "default"); got != want {
		t.Fatalf("%d default selects with the union of variants, %d without", got, want)
	}
	click(t, tt, "Start opencode")
	tt.Frame()
	h, m, e := lastStart(f)
	if h != "opencode" || m != "" || e != "" {
		t.Fatalf("start sent %q %q %q", h, m, e)
	}
	acceptedByStart(t, h, m, e)
	click(t, tt, "default")
	click(t, tt, "anthropic/sonnet")
	tt.Frame()
	if got, want := countText(t, tt, "default"), countText(t, plain, "default"); got != want {
		t.Fatalf("%d default selects with a model, want its variant select in place of the model's", got)
	}
	click(t, tt, "default")
	click(t, tt, "max")
	click(t, tt, "Start opencode")
	tt.Frame()
	h, m, e = lastStart(f)
	if h != "opencode" || m != "anthropic/sonnet" || e != "max" {
		t.Fatalf("start sent %q %q %q", h, m, e)
	}
	acceptedByStart(t, h, m, e)
}

func TestTheHarnessSwitchOffersNoEffortSelectWithoutAModelOnRealShapedOpencodeData(t *testing.T) {
	plain := menuFixture(t, idleFixture)
	pt := newTester(newApp(t, plain))
	openMenu(t, pt, plain)
	pickHarness(t, pt, "opencode")
	f := menuFixture(t, func(f *fixture) {
		idleFixture(f)
		ms := f.models[fleetID]
		ms.Harnesses[2].Efforts = []string{"high", "max"}
		ms.Harnesses[2].Groups[0].Models[0].Efforts = []string{"high", "max"}
		f.models[fleetID] = ms
	})
	tt := newTester(newApp(t, f))
	openMenu(t, tt, f)
	pickHarness(t, tt, "opencode")
	if got, want := countText(t, tt, "default"), countText(t, pt, "default"); got != want {
		t.Fatalf("%d default selects with the union of variants, %d without", got, want)
	}
	click(t, tt, "default")
	click(t, tt, "anthropic/sonnet")
	tt.Frame()
	click(t, tt, "default")
	click(t, tt, "high")
	click(t, tt, "Switch to opencode")
	tt.Frame()
	sw := lastSwitch(f)
	if sw.Harness != "opencode" || sw.Model != "anthropic/sonnet" || sw.Effort != "high" {
		t.Fatalf("switch sent %+v", sw)
	}
	acceptedByStart(t, sw.Harness, sw.Model, sw.Effort)
}

func TestTheStartStripAsksForAnOpencodeVariantOnlyOnceAModelIsTyped(t *testing.T) {
	f := menuFixture(t, opencodeOnly(nil, false))
	tt := newTester(newApp(t, f))
	if err := tt.Click("opencode variant"); err == nil {
		t.Fatal("a variant field is offered without a model")
	}
	click(t, tt, "opencode model")
	tt.Type("anthropic/sonnet")
	tt.Frame()
	click(t, tt, "opencode variant")
	tt.Type("thinking")
	click(t, tt, "Start opencode")
	tt.Frame()
	h, m, e := lastStart(f)
	if h != "opencode" || m != "anthropic/sonnet" || e != "thinking" {
		t.Fatalf("start sent %q %q %q", h, m, e)
	}
	acceptedByStart(t, h, m, e)
}

func TestTheHarnessSwitchAsksForAnOpencodeVariantOnlyOnceAModelIsTyped(t *testing.T) {
	f := menuFixture(t, func(f *fixture) {
		idleFixture(f)
		ms := f.models[fleetID]
		ms.Harnesses[2].Groups = nil
		f.models[fleetID] = ms
	})
	tt := newTester(newApp(t, f))
	openMenu(t, tt, f)
	pickHarness(t, tt, "opencode")
	if err := tt.Click("opencode variant"); err == nil {
		t.Fatal("a variant field is offered without a model")
	}
	click(t, tt, "opencode model")
	tt.Type("anthropic/sonnet")
	tt.Frame()
	click(t, tt, "opencode variant")
	tt.Type("thinking")
	click(t, tt, "Switch to opencode")
	tt.Frame()
	sw := lastSwitch(f)
	if sw.Harness != "opencode" || sw.Model != "anthropic/sonnet" || sw.Effort != "thinking" {
		t.Fatalf("switch sent %+v", sw)
	}
	acceptedByStart(t, sw.Harness, sw.Model, sw.Effort)
}

func runningOpencode(model string) func(*fixture) {
	return func(f *fixture) {
		idleFixture(f)
		st := f.status[fleetID]
		sup := *st.Supervisor
		sup.Harness, sup.Model, sup.Effort = "opencode", model, ""
		st.Supervisor = &sup
		f.status[fleetID] = st
		ms := f.models[fleetID]
		ms.Current, ms.Model, ms.Effort = "opencode", model, ""
		f.models[fleetID] = ms
	}
}

func TestTheSwitchOfAnOpencodeSupervisorOnItsDefaultModelSendsNoVariantWithoutAModel(t *testing.T) {
	f := menuFixture(t, runningOpencode(""))
	tt := newTester(newApp(t, f))
	click(t, tt, "opencode")
	tt.Frame()
	mustText(t, tt, "Switch now", "keep current")
	mustNotText(t, tt, "opencode variant")
	click(t, tt, "keep current")
	click(t, tt, "anthropic/sonnet")
	tt.Frame()
	click(t, tt, "opencode variant")
	tt.Type("thinking")
	click(t, tt, "anthropic/sonnet")
	click(t, tt, "keep current")
	tt.Frame()
	mustNotText(t, tt, "opencode variant")
	click(t, tt, "Switch now")
	tt.Frame()
	sw := lastSwitch(f)
	if sw.Model != "" || sw.Effort != "" {
		t.Fatalf("switch sent %+v, want no variant without a model", sw)
	}
	acceptedByStart(t, "opencode", sw.Model, sw.Effort)
}

func TestTheSwitchOfAnOpencodeSupervisorOnAModelKeepsItsVariantField(t *testing.T) {
	f := menuFixture(t, runningOpencode("anthropic/sonnet"))
	tt := newTester(newApp(t, f))
	click(t, tt, "anthropic/sonnet")
	tt.Frame()
	mustText(t, tt, "opencode variant")
	click(t, tt, "opencode variant")
	tt.Type("thinking")
	click(t, tt, "Switch now")
	tt.Frame()
	sw := lastSwitch(f)
	if sw.Model != "" || sw.Effort != "thinking" {
		t.Fatalf("switch sent %+v", sw)
	}
	acceptedByStart(t, "opencode", "anthropic/sonnet", sw.Effort)
}

func TestTheStartStripOffersNoDefaultModelToAHarnessThatNeedsOne(t *testing.T) {
	f := menuFixture(t, func(f *fixture) {
		st := f.status[fleetID]
		st.Supervisor = nil
		f.status[fleetID] = st
		ms := firstRunModels()
		ms.Harnesses = []desktop.Harness{ms.Harnesses[3]}
		f.models[fleetID] = ms
	})
	tt := newTester(newApp(t, f))
	mustNotText(t, tt, "default")
	click(t, tt, "Gemini Pro · gemini-pro")
	tt.Frame()
	if got := countText(t, tt, "default"); got != 0 {
		t.Fatalf("the model list offers default to agy")
	}
}

func menuBottom(t *testing.T, tt *ui.Tester, dark bool, height int) int {
	t.Helper()
	r, ok := tt.Find("Running now")
	if !ok {
		t.Fatalf("no menu; the window shows %q", tt.Texts())
	}
	img, tone := tt.Image(), kit.ColorsFor(dark).BG
	x := int(r.X) - 6
	y := int(r.Y)
	for y < height && sameInk(img.RGBAAt(x, y), tone) {
		y++
	}
	return y
}

func TestTheModelMenuFitsAShortWindowAndScrollsInside(t *testing.T) {
	for _, height := range []int{300, 480, 500, 760} {
		for _, dark := range []bool{false, true} {
			for _, name := range []string{"claude", "codex", "opencode", "agy"} {
				if height < desktop.MinWindowHeight && name != "claude" {
					continue
				}
				t.Run(fmt.Sprintf("%d-%t-%s", height, dark, name), func(t *testing.T) {
					f := menuFixture(t, idleFixture)
					tt := sized(t, newApp(t, f), 1100, height, dark)
					openMenu(t, tt, f)
					if name != "claude" {
						pickHarness(t, tt, name)
					}
					r, _ := tt.Find("Running now")
					bottom := menuBottom(t, tt, dark, height)
					if bottom >= height-1 {
						t.Fatalf("the menu runs to the window's edge at %d of %d", bottom, height)
					}
					if bottom <= int(r.Y) {
						t.Fatalf("no panel under %q", "Running now")
					}
					tail := "omp: omp not found on the board's PATH; restart the board after installing it"
					atEnd := func() bool {
						lr, ok := tt.Find(tail)
						return ok && lr.H > 0 && int(lr.Y+lr.H) <= bottom && bottom-int(lr.Y+lr.H) <= 24
					}
					if height == 760 && !atEnd() {
						t.Fatalf("the end of the menu is cut off at 760 in a panel ending at %d", bottom)
					}
					if !atEnd() {
						if bottom < height-14-4 {
							t.Fatalf("the panel ends at %d of %d with content left to scroll to: the room beside the button is not used", bottom, height)
						}
						for i := 0; i < 20; i++ {
							tt.Scroll(r.X+20, r.Y+20, 0, 120)
							tt.Frame()
						}
						if !atEnd() {
							lr, _ := tt.Find(tail)
							t.Fatalf("the end of the menu is out of reach after scrolling: %v in a panel ending at %d", lr, bottom)
						}
					}
					if after := menuBottom(t, tt, dark, height); after != bottom {
						t.Errorf("the panel moved from %d to %d while scrolling", bottom, after)
					}
				})
			}
		}
	}
}

func TestTheMenuRoomIsTheSpaceOnTheRoomierSideOfTheButton(t *testing.T) {
	anchor := func(y float32) ui.Rect { return ui.Rect{X: 700, Y: y, W: 100, H: 32} }
	tests := []struct {
		name   string
		window float32
		anchor ui.Rect
		want   float32
	}{
		{"below", 760, anchor(60), 760 - 92 - 14},
		{"above", 760, anchor(600), 600 - 14},
		{"floor", 200, anchor(90), 120},
		{"no bounds yet", 500, ui.Rect{}, 500 - 14},
	}
	for _, tc := range tests {
		if got := desktop.MenuRoom(tc.window, tc.anchor); got != tc.want {
			t.Errorf("%s: room %v, want %v", tc.name, got, tc.want)
		}
	}
}

func firstRunStrip(t *testing.T) (*fixture, *ui.Tester) {
	t.Helper()
	f := menuFixture(t, func(f *fixture) {
		st := f.status[fleetID]
		st.Supervisor = nil
		f.status[fleetID] = st
		f.models[fleetID] = firstRunModels()
	})
	return f, newTester(newApp(t, f))
}

func TestTheStartStripOpensOneHarnessAtATime(t *testing.T) {
	_, tt := firstRunStrip(t)
	mustText(t, tt, "Start claude", "codex", "opencode", "agy", "omp")
	mustNotText(t, tt, "Start codex")
	mustNotText(t, tt, "Start opencode")
	mustNotText(t, tt, "Start agy")
	click(t, tt, "codex")
	tt.Frame()
	mustText(t, tt, "Start codex", "GPT-6 Luna · gpt-6-luna", "claude")
	mustNotText(t, tt, "Start claude")
	click(t, tt, "opencode")
	tt.Frame()
	mustText(t, tt, "Start opencode", "codex")
	mustNotText(t, tt, "Start codex")
	mustNotText(t, tt, "Start claude")
	click(t, tt, "opencode")
	tt.Frame()
	mustNotText(t, tt, "Start opencode")
	mustNotText(t, tt, "Start codex")
	mustNotText(t, tt, "Start claude")
	click(t, tt, "claude")
	tt.Frame()
	mustText(t, tt, "Start claude")
	mustNotText(t, tt, "Start opencode")
}

func TestTheStartStripKeepsWhatWasChosenInAClosedHarness(t *testing.T) {
	f, tt := firstRunStrip(t)
	click(t, tt, "default")
	click(t, tt, "high")
	click(t, tt, "codex")
	tt.Frame()
	click(t, tt, "claude")
	tt.Frame()
	mustText(t, tt, "high")
	click(t, tt, "Start claude")
	tt.Frame()
	f.mu.Lock()
	harness, effort := f.startHarness, f.startEffort
	f.mu.Unlock()
	if harness != "claude" || effort != "high" {
		t.Fatalf("start sent %q %q", harness, effort)
	}
}

func TestTheStartStripShowsWhyAHarnessCannotStartWhileItIsClosed(t *testing.T) {
	_, tt := firstRunStrip(t)
	mustText(t, tt, "omp not found on the board's PATH; restart the board after installing it")
	click(t, tt, "omp")
	tt.Frame()
	mustText(t, tt, "Start omp", "omp not found on the board's PATH; restart the board after installing it")
}

func topRight(t *testing.T, tt *ui.Tester, dark bool, text string) int {
	t.Helper()
	r, ok := tt.Find(text)
	head, _ := tt.Find("Running now")
	if !ok {
		t.Fatalf("no %q in %q", text, tt.Texts())
	}
	img, tone := tt.Image(), kit.ColorsFor(dark).BG
	mid := int(r.Y + r.H/2)
	from, to := int(head.X), int(head.X)+276
	best, row := 0, 0
	for y := mid - 20; y <= mid-12; y++ {
		ink := 0
		for x := from; x < to; x++ {
			if !sameInk(img.RGBAAt(x, y), tone) {
				ink++
			}
		}
		if ink > best {
			best, row = ink, y
		}
	}
	last := 0
	for x := from; x < to; x++ {
		if !sameInk(img.RGBAAt(x, row), tone) {
			last = x
		}
	}
	return last
}

func TestTheSwitchButtonSpansTheMenuAsTheFieldsDo(t *testing.T) {
	for _, dark := range []bool{false, true} {
		f := menuFixture(t, idleFixture)
		tt := darkTester(newApp(t, f), dark)
		openMenu(t, tt, f)
		tt.Frame()
		field, button := topRight(t, tt, dark, "claude (running)"), topRight(t, tt, dark, "Switch now")
		if button != field {
			t.Errorf("dark %t: the Switch button's top border ends at %d, the field's at %d", dark, button, field)
		}
	}
}

func TestTheStartStripOpensTheFirstHarnessThatCanStart(t *testing.T) {
	f := menuFixture(t, func(f *fixture) {
		st := f.status[fleetID]
		st.Supervisor = nil
		f.status[fleetID] = st
		ms := firstRunModels()
		ms.Harnesses = []desktop.Harness{ms.Harnesses[4], ms.Harnesses[1], ms.Harnesses[0]}
		f.models[fleetID] = ms
	})
	tt := newTester(newApp(t, f))
	mustText(t, tt, "Start codex")
	mustNotText(t, tt, "Start omp")
	mustNotText(t, tt, "Start claude")
}

func TestScenesTheModelMenuInAShortWindow(t *testing.T) {
	for _, dark := range []bool{false, true} {
		mode := map[bool]string{false: "light", true: "dark"}[dark]
		t.Run(mode, func(t *testing.T) {
			f := menuFixture(t, nil)
			tt := sized(t, newApp(t, f), 1100, desktop.MinWindowHeight, dark)
			openMenu(t, tt, f)
			tt.Frame()
			shotSized(t, tt, "42-menu-short-1100x480-"+mode, 1100, desktop.MinWindowHeight)
			mustText(t, tt, "Running now", "Change harness, model or effort")
		})
	}
}

func chevronPointsDown(t *testing.T, tt *ui.Tester, name string) bool {
	t.Helper()
	r, ok := tt.Find(name)
	if !ok {
		t.Fatalf("no %q in %q", name, tt.Texts())
	}
	img := tt.Image()
	mid := int(r.Y + r.H/2)
	const x0, x1 = 1050, 1080
	bg := img.RGBAAt(1046, mid)
	y0, y1 := -1, -1
	for y := mid - 12; y <= mid+12; y++ {
		for x := x0; x < x1; x++ {
			if img.RGBAAt(x, y) != bg {
				if y0 < 0 {
					y0 = y
				}
				y1 = y
				break
			}
		}
	}
	if y0 < 0 || y1-y0 < 3 {
		t.Fatalf("no chevron beside %q: ink rows %d to %d", name, y0, y1)
	}
	span := func(y int) int {
		lo, hi := -1, -1
		for x := x0; x < x1; x++ {
			if img.RGBAAt(x, y) != bg {
				if lo < 0 {
					lo = x
				}
				hi = x
			}
		}
		return hi - lo
	}
	top, bottom := span(y0)+span(y0+1), span(y1)+span(y1-1)
	if top == bottom {
		t.Fatalf("the chevron of %q is symmetric: %d and %d", name, top, bottom)
	}
	return top > bottom
}

func TestTheStartStripOpenRowTurnsItsChevron(t *testing.T) {
	for _, dark := range []bool{false, true} {
		f, _ := firstRunStrip(t)
		tt := darkTester(newApp(t, f), dark)
		if !chevronPointsDown(t, tt, "codex") {
			t.Errorf("dark %t: the closed row's chevron does not point down", dark)
		}
		if chevronPointsDown(t, tt, "claude") {
			t.Errorf("dark %t: the open row's chevron points down", dark)
		}
		click(t, tt, "codex")
		tt.Frame()
		if chevronPointsDown(t, tt, "codex") {
			t.Errorf("dark %t: the chevron of the row that was opened points down", dark)
		}
		if !chevronPointsDown(t, tt, "claude") {
			t.Errorf("dark %t: the chevron of the row that was closed does not point down", dark)
		}
	}
}
