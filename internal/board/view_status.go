package board

import (
	"context"
	"net/url"
	"strings"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

type gaugeView struct {
	Text  string
	Level string
	Pct   int
}

type paneState struct {
	Stale, Agent, Hint, Limited string
	ScreenNote                  string
	Blocked                     bool
	Screen                      string
	Revision                    int64
	Digest                      string
}

func (b *Board) pane(ctx context.Context, sup state.Supervisor) paneState {
	var v paneState
	caps, err := b.o.Luvus.Check(ctx)
	if err != nil || caps.ServerGeneration != sup.ServerGeneration {
		v.Stale = staleServer
		return v
	}
	ag, err := b.o.Luvus.Explain(ctx, sup.PaneID)
	if err != nil {
		v.Stale = staleServer
		return v
	}
	v.Agent, v.Hint = ag.Status, ag.Hint
	s, err := b.o.Luvus.Read(ctx, sup.PaneID, luvus.ScreenLines)
	read := err == nil && s.TerminalID == sup.TerminalID
	if read {
		v.Screen, v.Revision, v.Digest = s.Text, s.ContentRevision, luvus.ScreenDigest(s.Text)
		if ag.Status == "idle" || ag.Status == "done" {
			if line, ok := harness.Limit(sup.Harness, s.Text); ok {
				v.Limited, _, _ = strings.Cut(line, " (~")
			}
		}
	} else if ag.Status != "blocked" {
		if err != nil {
			v.ScreenNote = "The supervisor's screen could not be read: " + Scrub(err.Error())
		} else {
			v.ScreenNote = "The pane no longer holds " + state.SupervisorRef(sup.ID) + "; there is no live screen."
		}
	}
	if ag.Status != "blocked" {
		return v
	}
	if !read {
		v.Stale = staleServer
		return v
	}
	v.Blocked = true
	return v
}

type supervisorView struct {
	Sup            *state.Supervisor
	Ref            string
	Pending        int
	Profiles       []profile
	Keys           []key
	Chosen         string
	NoneStartable  string
	Pick           bool
	StartHarnesses []harnessChoice
	Resumable      bool
	Drop           string
	Switchable     bool
	SwitchProfiles []profile
	Current        *harnessChoice
	OtherHarnesses []harnessChoice
	ModelNote      string
	AgentState     string
	ComposerHint   string
	Pill           string
	Line           string
	Held           bool
	paneState
}

type viewContext struct {
	Controls bool
	Base     string
	Token    string
	Session  string
	Multi    bool
	Titles   func(string) string
	Local    func(string) string
}

func (b *Board) buildSupervisor(ctx context.Context, q url.Values) (supervisorView, error) {
	v := supervisorView{
		AgentState:   "none",
		ComposerHint: "No supervisor is running; your message waits.",
		Pill:         "neutral",
	}
	sup, ok, err := b.st.LatestSupervisor(ctx)
	if err != nil {
		return v, err
	}
	pending, err := b.st.PendingSupervisorInputs(ctx)
	if err != nil {
		return v, err
	}
	profiles := b.profiles()
	v.Pending, v.Profiles, v.Keys = len(pending), profiles, keyButtons()
	v.Chosen, v.NoneStartable = chosenProfile(profiles), noneStartable(profiles)
	v.Pick = q.Get("pick") == "1" || !ok
	label := "No supervisor"
	if ok {
		v.Sup, v.Ref = &sup, state.SupervisorRef(sup.ID)
		resumable := sup
		if !sup.Live() {
			started, found, err := b.st.LastStartedSupervisor(ctx)
			if err != nil {
				return v, err
			}
			if found {
				resumable = started
				if started.Switching() {
					v.Drop = started.SwitchText()
				}
			}
		}
		v.Resumable = !sup.Live() && resumable.Session != ""
		if sup.Status == state.AttemptRunning {
			v.paneState = b.pane(ctx, sup)
			if _, held, err := b.st.SupervisorHold(ctx, sup); err != nil {
				return v, err
			} else if held {
				v.Held = true
			}
		}
		v.Pill, label = pill(sup, v.paneState, v.Held)
		v.AgentState, v.ComposerHint = hint(sup, v.paneState, v.Held)
		if sup.Status == state.AttemptRunning {
			v.Switchable = true
			v.SwitchProfiles = b.profilesFor(sup.Harness)
			current := b.picker(sup.Harness)
			v.Current = &current
			v.OtherHarnesses = b.choices(sup.Harness)
			note, err := b.st.ModelNote(ctx, state.SupervisorRef(sup.ID), sup.Model, sup.Effort)
			if err != nil {
				return v, err
			}
			v.ModelNote = note
		}
	}
	if v.Pick {
		v.StartHarnesses = b.choices("")
	}
	v.Line = strings.ToLower(lineWord(label))
	return v, nil
}

type statusView struct {
	supervisorView
	Gauge       gaugeView
	Compactions int
	Waiting     int
	Worst       string
	WorstText   string
	WorstWord   string
	WorstCount  int
}

func (b *Board) readGauge(ctx context.Context) (gaugeView, int) {
	if b.o.Transcript == nil {
		return gaugeView{}, 0
	}
	sup, ok, err := b.st.LatestSupervisor(ctx)
	if err != nil || !ok {
		return gaugeView{}, 0
	}
	st := b.o.Transcript.Status(sup.Harness, sup.Session)
	text, level, pct := gauge(st)
	return gaugeView{Text: text, Level: level, Pct: pct}, st.Compactions
}

func (b *Board) buildStatus(ctx context.Context, sv supervisorView) statusView {
	v := statusView{supervisorView: sv}
	v.Gauge, v.Compactions = b.readGauge(ctx)
	return v
}
