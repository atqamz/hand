package board_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

var traySlug = regexp.MustCompile(`<details class="wait" data-kind="([a-z]+)" id="wait-[a-z0-9]+"[^>]*>\s*<summary>[\s\S]*?<span class="slug"><span class="code" data-code="([A-Z]+)">([A-Z]+)</span><span class="kind">([A-Z]+)</span><span class="ref">([a-z0-9]+)</span>`)

func TestTheTrayCarriesWireCodes(t *testing.T) {
	fx := newFixture(t)
	fx.status, fx.hint = "blocked", "Trust this folder?"
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ctx := context.Background()
	one := active(t, fx.st, "Fix login")
	if _, err := fx.st.Ask(ctx, one.ID, "Keep the old cookie name?"); err != nil {
		t.Fatal(err)
	}
	two := active(t, fx.st, "Flaky test")
	attempt(t, fx.st, two.ID, state.AttemptFailed, "launch did not finish")
	three := active(t, fx.st, "Docs")
	a := attempt(t, fx.st, three.ID, state.AttemptRunning, "")
	if _, err := fx.st.AddReport(ctx, a.ID, state.ReportProgress, "Halfway there"); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range traySlug.FindAllStringSubmatch(region(get(t, fx.handler(), "/"), "queue"), -1) {
		if m[2] != m[3] {
			t.Fatalf("code %s shows %s", m[2], m[3])
		}
		got = append(got, m[1]+" "+m[2]+" "+m[4]+" "+m[5])
	}
	want := []string{"blocked FLASH BLOCKED s1", "decision BULLETIN DECISION d1", "failure URGENT FAILED a1", "report ROUTINE REPORT r1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("tray = %q", got)
	}
	resume := newFixture(t)
	resume.supervisor(t, state.AttemptInterrupted, "gen-1")
	contains(t, "resume", region(get(t, resume.handler(), "/"), "queue"), `data-code="SERVICE">SERVICE</span><span class="kind">RESUME</span>`)
}

func TestTheBudgetIsOneLinePerTask(t *testing.T) {
	fx := newFixture(t)
	one := active(t, fx.st, "Fix login")
	attempt(t, fx.st, one.ID, state.AttemptRunning, "")
	active(t, fx.st, "Write docs")
	tasks := region(get(t, fx.handler(), "/"), "tasks")
	if n := strings.Count(tasks, `<li class="check"`); n != 2 {
		t.Fatalf("budget lines = %d:\n%s", n, tasks)
	}
	contains(t, "budget", tasks, `<span class="slug"><a class="ref" href="/task/t1">T1</a><span class="status">ACTIVE</span><span class="attempt">A1 CODEX GPT-6-LUNA RUNNING</span></span><span class="check-title">Fix login</span>`)
}
