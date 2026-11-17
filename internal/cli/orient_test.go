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
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Fix login")
	out := h.ok("orient")
	if !strings.Contains(out, "tasks: inbox=1 active=0 done=0 abandoned=0") || !strings.Contains(out, "inbox[1]{id,project,title}:") {
		t.Fatalf("orient = %q", out)
	}
}

func TestOrientSinceListsOnlyWhatChanged(t *testing.T) {
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
		if len(l) != 1 || !strings.Contains(l[0], "s1 has run this turn for 1m30s") || !strings.Contains(l[0], "message waits") || !strings.Contains(l[0], "hand wake") {
			t.Fatalf("over the bound: %q in %q", l, out)
		}
	}
	h.ok("supervisor", "send", "--text", "hello")
	none("a delivered message starts a new turn")
	h.advance(2 * time.Minute)
	if full, _ := both(); len(turnLines(full)) != 1 {
		t.Fatalf("over the bound again: %q", full)
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
	h, _ := newSupervisorFixture(t)
	h.advance(time.Hour)
	if l := turnLines(h.ok("orient")); len(l) != 0 {
		t.Fatalf("orient flags a turn with no supervisor: %q", l)
	}
}
