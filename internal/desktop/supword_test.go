package desktop_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

type wordCase struct {
	name   string
	word   string
	sup    func(*desktop.Supervisor)
	status func(*desktop.Status)
	need   string
	open   string
}

func running(sup *desktop.Supervisor) { sup.Status, sup.Live, sup.Resumable = "running", true, false }

func wordCases() []wordCase {
	limited := desktop.Need{Kind: "limited", Ref: "s1", Title: "Usage limit reached", Word: "LIMITED", Tone: "wait"}
	resume := desktop.Need{Kind: "resume", Ref: "s1", Title: "The supervisor stopped unexpectedly", Word: "INTERRUPTED", Tone: "fail"}
	return []wordCase{
		{name: "working", word: "working", sup: running, status: func(s *desktop.Status) {
			s.AgentState, s.Pill, s.Line = "working", "working", "working"
		}},
		{name: "idle", word: "ready", sup: running, status: func(s *desktop.Status) {
			s.AgentState, s.Pill, s.Line = "ready", "ready", "ready"
		}},
		{name: "blocked", word: "blocked", sup: func(sup *desktop.Supervisor) {
			running(sup)
			sup.Blocked = true
			sup.Keys = &desktop.Keys{Revision: 6, Digest: screenDigest, Pressed: 2}
		}, status: func(s *desktop.Status) {
			s.AgentState, s.Pill, s.Line = "blocked", "failing", "blocked"
			s.Needs.Items = append([]desktop.Need{{Kind: "blocked", Ref: "s1", Word: "BLOCKED", Tone: "fail"}}, s.Needs.Items...)
		}, need: "blocked", open: "Blocked · s1"},
		{name: "decision open", word: "working", sup: running, status: func(s *desktop.Status) {
			s.AgentState, s.Pill, s.Line = "working", "working", "working"
			s.Needs.Items = append([]desktop.Need{{Kind: "decision", Ref: "d1", Task: "t1", Title: "Keep the old cookie name?", Word: "DECISION", Tone: "wait", Decision: "d1"}}, s.Needs.Items...)
		}},
		{name: "limited", word: "limited", sup: running, status: func(s *desktop.Status) {
			s.AgentState, s.Pill, s.Line = "ready", "ready", "ready"
			s.Needs.Items = append([]desktop.Need{limited}, s.Needs.Items...)
		}, need: "limited"},
		{name: "held", word: "held", sup: func(sup *desktop.Supervisor) { running(sup); sup.Held = true }, status: func(s *desktop.Status) {
			s.AgentState, s.Pill, s.Line = "blocked", "failing", "held"
		}},
		{name: "unreachable", word: "unreachable", sup: running, status: func(s *desktop.Status) {
			s.AgentState, s.Pill, s.Line = "none", "failing", "unreachable"
		}},
		{name: "starting", word: "starting", sup: func(sup *desktop.Supervisor) { sup.Status, sup.Live = "launching", true }, status: func(s *desktop.Status) {
			s.AgentState, s.Pill, s.Line = "none", "neutral", "starting"
		}},
		{name: "stopped", word: "stopped", sup: func(sup *desktop.Supervisor) { sup.Status, sup.Live, sup.Resumable = "stopped", false, true }, status: func(s *desktop.Status) {
			s.AgentState, s.Pill, s.Line = "none", "neutral", "stopped"
		}},
		{name: "exited", word: "exited", sup: func(sup *desktop.Supervisor) { sup.Status, sup.Live, sup.Resumable = "exited", false, true }, status: func(s *desktop.Status) {
			s.AgentState, s.Pill, s.Line = "none", "failing", "exited"
			s.Needs.Items = append([]desktop.Need{resume}, s.Needs.Items...)
		}, need: "resume", open: resume.Title},
		{name: "interrupted", word: "interrupted", sup: func(sup *desktop.Supervisor) { sup.Status, sup.Live, sup.Resumable = "interrupted", false, true }, status: func(s *desktop.Status) {
			s.AgentState, s.Pill, s.Line = "none", "failing", "interrupted"
			s.Needs.Items = append([]desktop.Need{resume}, s.Needs.Items...)
		}, need: "resume", open: resume.Title},
		{name: "no supervisor", word: "no supervisor", status: func(s *desktop.Status) {
			s.Supervisor, s.AgentState, s.Pill, s.Line = nil, "none", "neutral", "no supervisor"
		}},
	}
}

func (c wordCase) fixture(t *testing.T) *fixture {
	f := goldenFixture(t)
	st := f.status[fleetID]
	if c.sup != nil {
		sup := *st.Supervisor
		sup.Blocked, sup.Held, sup.Keys, sup.Ask = false, false, nil, nil
		c.sup(&sup)
		st.Supervisor = &sup
	}
	st.Needs.Items = slices.DeleteFunc(slices.Clone(st.Needs.Items), func(n desktop.Need) bool {
		return n.Kind == "blocked" || n.Kind == "limited" || n.Kind == "resume"
	})
	c.status(&st)
	st.Needs.Waiting = len(st.Needs.Items)
	f.status[fleetID] = st
	return f
}

func TestEverySurfaceShowsTheSupervisorsOneStateWord(t *testing.T) {
	all := []string{"working", "ready", "blocked", "limited", "held", "unreachable", "starting", "stopped", "interrupted", "exited", "no supervisor", "failing", "idle"}
	for _, c := range wordCases() {
		t.Run(c.name, func(t *testing.T) {
			f := c.fixture(t)
			if got := desktop.SupervisorWord(f.status[fleetID]); got != c.word {
				t.Fatalf("supervisorWord = %q, want %q", got, c.word)
			}
			for _, dark := range []bool{false, true} {
				tt := sized(t, newApp(t, f), windowWidth, windowHeight, dark)
				var got []string
				for _, text := range tt.Texts() {
					if slices.Contains(all, text) {
						got = append(got, text)
					}
				}
				want := []string{c.word, c.word}
				if c.sup == nil {
					want = want[:1]
				}
				if !slices.Equal(got, want) {
					t.Errorf("dark=%v: state words on screen %q, want %q", dark, got, want)
				}
				if c.need != "" {
					head := strings.ToUpper(c.word[:1]) + c.word[1:]
					if n := countExact(tt, head); n != 1 {
						t.Errorf("dark=%v: the Needs group %q is shown %d times, want once", dark, head, n)
					}
				}
				if c.open != "" {
					badge := strings.ToUpper(c.word)
					if countExact(tt, badge) == 0 {
						click(t, tt, c.open)
					}
					if n := countExact(tt, badge); n != 1 {
						t.Errorf("dark=%v: the Needs badge %q is shown %d times, want once", dark, badge, n)
					}
				}
				working := countExact(tt, "s1 working")
				if (c.word == "working") != (working == 1) || working > 1 {
					t.Errorf("dark=%v: the working line is shown %d times in state %q", dark, working, c.word)
				}
			}
		})
	}
}

func countExact(tt interface{ Texts() []string }, want string) int {
	n := 0
	for _, text := range tt.Texts() {
		if text == want {
			n++
		}
	}
	return n
}

func TestAnUnreadStatusShowsNoStateWord(t *testing.T) {
	f := wordCases()[2].fixture(t)
	app := newApp(t, f)
	f.fail = errors.New("the status read failed")
	app.Refresh()
	tt := sized(t, app, windowWidth, windowHeight, false)
	mustText(t, tt, "unknown")
	for _, word := range []string{"blocked", "failing", "ready"} {
		mustNotText(t, tt, word)
	}
}

func TestAWorkersLimitKeepsItsOwnWord(t *testing.T) {
	f := wordCases()[0].fixture(t)
	st := f.status[fleetID]
	st.Needs.Items = append([]desktop.Need{{Kind: "limited", Ref: "a2", Task: "t2", Title: "Usage limit", Word: "LIMITED", Tone: "wait"}}, st.Needs.Items...)
	f.status[fleetID] = st
	tt := sized(t, newApp(t, f), windowWidth, windowHeight, false)
	if n := countExact(tt, "Limited"); n != 1 {
		t.Errorf("the worker's limit group is shown %d times, want once", n)
	}
	if n := countExact(tt, "Working"); n != 1 {
		t.Errorf("Working is shown %d times, want only the Conversation dot", n)
	}
}

func TestEveryStateWordHasATone(t *testing.T) {
	for word, want := range map[string]string{
		"blocked": "fail", "held": "fail", "unreachable": "fail", "exited": "fail", "interrupted": "fail",
		"limited": "wait", "working": "accent", "ready": "pass",
		"stopped": "neutral", "starting": "neutral", "no supervisor": "neutral", "": "neutral",
	} {
		if got := desktop.ToneOf(word); got != want {
			t.Errorf("tone of %q = %q, want %q", word, got, want)
		}
	}
}
