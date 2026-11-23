package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestOrientCommand(t *testing.T) {
	t.Parallel()
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Fix login")
	out := h.ok("orient")
	if !strings.Contains(out, "tasks: inbox=1 active=0 done=0 abandoned=0") || !strings.Contains(out, "inbox[1]{id,project,title}:") {
		t.Fatalf("orient = %q", out)
	}
}

func TestOrientSinceListsOnlyWhatChanged(t *testing.T) {
	t.Parallel()
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Fix login")
	cursor := regexp.MustCompile(`(?m)^cursor: (\d+)$`).FindStringSubmatch(h.ok("orient"))[1]
	h.ok("task", "add", "hand", "Write docs")
	out := h.ok("orient", "--since", cursor)
	if !strings.Contains(out, "since: "+cursor+"\n") || !strings.Contains(out, "events[1]{seq,kind,task}:") || !strings.Contains(out, "task.added,t2") || strings.Contains(out, "Fix login") || strings.Contains(out, "help[") {
		t.Fatalf("orient --since = %q", out)
	}
	if _, errOut, code := h.run("orient", "--since", "-5"); code == 0 || !strings.Contains(errOut, "run `hand orient`") {
		t.Fatalf("orient --since -5 = %q, exit %d", errOut, code)
	}
	if _, errOut, code := h.run("orient", "--since", "99999"); code == 0 || !strings.Contains(errOut, "run `hand orient`") {
		t.Fatalf("orient --since 99999 = %q, exit %d", errOut, code)
	}
}

func turnLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "turn_long:") {
			lines = append(lines, l)
		}
	}
	return lines
}

func TestOrientFlagsALongSupervisorTurn(t *testing.T) {
	t.Parallel()
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	cursor := regexp.MustCompile(`(?m)^cursor: (\d+)$`).FindStringSubmatch(h.ok("orient"))[1]
	both := func() (string, string) { return h.ok("orient"), h.ok("orient", "--since", cursor) }
	none := func(when string) {
		t.Helper()
		full, short := both()
		if l := turnLines(full + short); len(l) != 0 {
			t.Fatalf("%s: orient flags the turn: %q", when, l)
		}
	}
	none("fresh turn")
	h.advance(30 * time.Second)
	none("under the bound")
	h.advance(time.Minute)
	full, short := both()
	for _, out := range []string{full, short} {
		l := turnLines(out)
		if len(l) != 1 || !strings.Contains(l[0], "s1 is still working 1m30s after it started") || strings.Contains(l[0], "has run this turn") || !strings.Contains(l[0], "message waits") || !strings.Contains(l[0], "hand wake") {
			t.Fatalf("over the bound: %q in %q", l, out)
		}
	}
	h.ok("supervisor", "send", "--text", "hello")
	none("the age counts from the last delivery")
	h.advance(2 * time.Minute)
	if l := turnLines(h.ok("orient")); len(l) != 1 || !strings.Contains(l[0], "s1 is still working 2m0s after its last message or wake") {
		t.Fatalf("over the bound again: %q", l)
	}
	if err := os.WriteFile(filepath.Join(h.home, "routing.json"), []byte(`{"supervisor":{"answer_within":"5m"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	none("a longer answer_within")
	if err := os.WriteFile(filepath.Join(h.home, "routing.json"), []byte(`{"supervisor":{"answer_within":"1m"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if full, _ := both(); len(turnLines(full)) != 1 {
		t.Fatalf("answer_within 1m: %q", full)
	}
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	none("an idle supervisor")
}

func TestOrientDoesNotFlagATurnWithoutAManagedSupervisor(t *testing.T) {
	t.Parallel()
	h, _ := newSupervisorFixture(t)
	h.advance(time.Hour)
	if l := turnLines(h.ok("orient")); len(l) != 0 {
		t.Fatalf("orient flags a turn with no supervisor: %q", l)
	}
}

func TestOrientWarnsOnceWhenAnswerWithinIsInvalid(t *testing.T) {
	t.Parallel()
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	cursor := regexp.MustCompile(`(?m)^cursor: (\d+)$`).FindStringSubmatch(h.ok("orient"))[1]
	warns := func(out string) []string {
		var lines []string
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "turn_warn:") {
				lines = append(lines, l)
			}
		}
		return lines
	}
	if l := warns(h.ok("orient")); len(l) != 0 {
		t.Fatalf("a valid policy warns: %q", l)
	}
	if err := os.WriteFile(filepath.Join(h.home, "routing.json"), []byte(`{"supervisor":{"answer_within":"soon"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h.advance(90 * time.Second)
	for _, out := range []string{h.ok("orient"), h.ok("orient", "--since", cursor)} {
		l := warns(out)
		if len(l) != 1 || !strings.Contains(l[0], "routing.json unreadable") || !strings.Contains(l[0], "answer_within") || !strings.Contains(l[0], "1m0s") {
			t.Fatalf("warning = %q in %q", l, out)
		}
		if len(turnLines(out)) != 1 {
			t.Fatalf("an invalid bound hides the default 60s hint: %q", out)
		}
	}
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	if l := warns(h.ok("orient")); len(l) != 1 {
		t.Fatalf("an idle supervisor hides the warning: %q", l)
	}
}
