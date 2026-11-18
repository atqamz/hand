package transcript

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func claudeUse(msg, id, name, command string) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":"2026-11-17T04:40:05.000Z","message":{"id":%q,"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":%q,"name":%q,"input":{"command":%q}}]}}`, msg, id, name, command)
}

func claudeText(msg, text string) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":"2026-11-17T04:41:00.000Z","message":{"id":%q,"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":%q}]}}`, msg, text)
}

func claudeResult(id string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":"2026-11-17T04:40:09.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":"ok"}]}}`, id)
}

func claudeRunning(t *testing.T, lines ...string) []Call {
	t.Helper()
	home, _ := claudeHome(t, strings.Join(lines, "\n")+"\n")
	r := &Reader{Paths: Paths{Claude: home}}
	calls, err := r.Running(context.Background(), "claude", sessionUUID, "/fleets/demo")
	if err != nil {
		t.Fatal(err)
	}
	return calls
}

func commands(calls []Call) []string {
	var out []string
	for _, c := range calls {
		out = append(out, c.Tool+":"+c.Command)
	}
	return out
}

func TestClaudeRunningIsTheToolCallWithNoResultYet(t *testing.T) {
	got := claudeRunning(t, claudeUse("m1", "a", "Bash", "sleep 300"))
	if want := []string{"Bash:sleep 300"}; !slices.Equal(commands(got), want) {
		t.Fatalf("running = %q, want %q", commands(got), want)
	}
	if got := claudeRunning(t, claudeUse("m1", "a", "Bash", "sleep 300"), claudeResult("a")); len(got) != 0 {
		t.Fatalf("a finished call is still running: %q", commands(got))
	}
}

func TestClaudeRunningKeepsEveryParallelCallThatIsOpen(t *testing.T) {
	got := claudeRunning(t,
		claudeUse("m1", "a", "Bash", "sleep 300"),
		claudeUse("m1", "b", "Bash", "gh pr merge 1"),
		claudeResult("a"))
	if want := []string{"Bash:gh pr merge 1"}; !slices.Equal(commands(got), want) {
		t.Fatalf("running = %q, want %q", commands(got), want)
	}
}

func TestClaudeRunningDropsACallThatANewerMessageOutlived(t *testing.T) {
	got := claudeRunning(t,
		claudeUse("m1", "a", "Bash", "sleep 300"),
		claudeText("m2", "moving on"))
	if len(got) != 0 {
		t.Fatalf("a call from an earlier message is still running: %q", commands(got))
	}
	got = claudeRunning(t,
		claudeUse("m1", "a", "Bash", "sleep 300"),
		`{"type":"user","timestamp":"2026-11-17T04:41:00.000Z","message":{"role":"user","content":"hello"}}`)
	if len(got) != 0 {
		t.Fatalf("a call before a new prompt is still running: %q", commands(got))
	}
}

func TestClaudeRunningIgnoresASubagentsOwnCalls(t *testing.T) {
	side := `{"type":"assistant","isSidechain":true,"timestamp":"2026-11-17T04:40:05.000Z","message":{"id":"s1","role":"assistant","content":[{"type":"tool_use","id":"x","name":"Bash","input":{"command":"sleep 5"}}]}}`
	if got := claudeRunning(t, claudeUse("m1", "a", "Task", ""), side); !slices.Equal(commands(got), []string{"Task:"}) {
		t.Fatalf("running = %q, want only the Task call", commands(got))
	}
}

func TestClaudeRunningKeepsAToolThatTakesNoCommand(t *testing.T) {
	line := `{"type":"assistant","timestamp":"2026-11-17T04:40:05.000Z","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"a","name":"Read","input":{"file_path":"/x"}}]}}`
	if got := claudeRunning(t, line); !slices.Equal(commands(got), []string{"Read:"}) {
		t.Fatalf("running = %q", commands(got))
	}
}

func TestRunningRefusesAHarnessWhoseRecordDoesNotShowTheCall(t *testing.T) {
	r := &Reader{}
	for _, h := range []string{"codex", "omp", "opencode", "agy", "copilot", "other"} {
		if _, err := r.Running(context.Background(), h, "s1", "/x"); !errors.Is(err, ErrNoCalls) {
			t.Fatalf("%s: err = %v, want ErrNoCalls", h, err)
		}
	}
}
