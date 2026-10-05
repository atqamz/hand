package cli

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestExitLogIgnoresARecordOlderThanExitKeep(t *testing.T) {
	l := &exitLog{m: map[string]exitRecord{
		"old":   {"terminal exited (code 3)", time.Now().Add(-exitKeep - time.Second)},
		"fresh": {"terminal exited (code 4)", time.Now()},
	}}
	l.budget()
	if got := l.wait(context.Background(), "old"); got != "terminal exited" {
		t.Fatalf("old record reported as %q", got)
	}
	if got := l.wait(context.Background(), "fresh"); got != "terminal exited (code 4)" {
		t.Fatalf("fresh record reported as %q", got)
	}
}

func TestExitLogNoteReportsSignalOverCode(t *testing.T) {
	for _, tc := range []struct{ name, detail, want string }{
		{"signal", `{"exit_code":null,"signal":"SIGKILL"}`, "terminal exited (signal SIGKILL)"},
		{"signal beats code", `{"exit_code":9,"signal":"SIGTERM"}`, "terminal exited (signal SIGTERM)"},
		{"code", `{"exit_code":3,"signal":null}`, "terminal exited (code 3)"},
		{"zero code", `{"exit_code":0}`, "terminal exited (code 0)"},
		{"empty signal", `{"exit_code":2,"signal":""}`, "terminal exited (code 2)"},
		{"neither", `{}`, "terminal exited"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := &exitLog{m: map[string]exitRecord{}}
			l.note(json.RawMessage(`{"terminal_id":"t","detail":` + tc.detail + `}`))
			l.budget()
			if got := l.wait(context.Background(), "t"); got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExitLogWaitStopsWhenTheContextIsCancelled(t *testing.T) {
	l := &exitLog{m: map[string]exitRecord{}}
	l.budget()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if got := l.wait(ctx, "t"); got != "terminal exited" || time.Since(start) > exitWait/2 {
		t.Fatalf("wait = %q after %v", got, time.Since(start))
	}
}
