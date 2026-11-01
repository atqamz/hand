package harness

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

const bashScreen = `╭──────────────────────────────────────────────────────────────╮
│ Bash command                                                 │
│                                                              │
│   git push origin main                                       │
│   Push the branch                                            │
│                                                              │
│ Do you want to proceed?                                      │
│ ❯ 1. Yes                                                     │
│   2. Yes, and don't ask again for git push commands in /w/t1 │
│   3. No, and tell Claude what to do differently (esc)        │
╰──────────────────────────────────────────────────────────────╯`

func TestClaudePermissionReadsTheCommandAndThreeActions(t *testing.T) {
	p, ok := ClaudePermission(bashScreen)
	want := Permission{Tool: "Bash", Command: "git push origin main\nPush the branch", Approve: "1", Always: "2", Deny: "3", Pattern: "git push"}
	if !ok || p != want {
		t.Fatalf("permission = %+v, %v", p, ok)
	}
}

func TestClaudePermissionKeepsAMultiLineCommandAsPrinted(t *testing.T) {
	screen := "────────────────────────\n Bash command\n\n   for f in *.go; do\n     gofmt -l $f\n   done\n   Check formatting\n\n Claude Code will automatically deny this request in 1:59\n Do you want to proceed?\n❯ 1. Yes\n  2. No\n Esc to cancel · Tab to amend"
	p, ok := ClaudePermission(screen)
	if !ok || p.Command != "for f in *.go; do\n  gofmt -l $f\ndone\nCheck formatting" || p.Approve != "1" || p.Deny != "2" || p.Always != "" {
		t.Fatalf("permission = %+v, %v", p, ok)
	}
}

func TestClaudePermissionOffersNoAlwaysWithoutADerivableCommand(t *testing.T) {
	for name, always := range map[string]string{
		"compound":   "2. Yes, and don't ask again for cd and git status commands in /w/t1",
		"cut":        "2. Yes, and don't ask again for git commit -m … commands in /w/t1",
		"session":    "2. Yes, allow all edits during this session (shift+tab)",
		"other tool": "2. Yes, and don't ask again for Fetch commands in /w/t1",
	} {
		header := "Bash command"
		switch name {
		case "session":
			header = "Edit file"
		case "other tool":
			header = "Fetch"
		}
		screen := "────────────────────────\n " + header + "\n  git status\n Do you want to proceed?\n❯ 1. Yes\n  " + always + "\n  3. No (esc)"
		p, ok := ClaudePermission(screen)
		if !ok || p.Always != "" || p.Pattern != "" || p.Approve != "1" || p.Deny != "3" {
			t.Fatalf("%s: permission = %+v, %v", name, p, ok)
		}
	}
}

func TestClaudePermissionStripsAPrefixRule(t *testing.T) {
	screen := strings.Replace(bashScreen, "git push commands", "git push:* commands", 1)
	if p, ok := ClaudePermission(screen); !ok || p.Pattern != "git push" || p.Always != "2" {
		t.Fatalf("permission = %+v, %v", p, ok)
	}
}

func TestClaudePermissionFallsBackOnScreensItDoesNotKnow(t *testing.T) {
	for name, screen := range map[string]string{
		"empty":       "",
		"chat":        "● Done.\n\n❯ \n",
		"trust":       "────────────────────────\n Do you trust the files in this folder?\n❯ 1. Yes, proceed\n  2. No, exit",
		"no question": "────────────────────────\n Bash command\n  ls\n❯ 1. Yes\n  2. No",
		"no command":  "────────────────────────\n Bash command\n Do you want to proceed?\n❯ 1. Yes\n  2. No",
		"no deny":     "────────────────────────\n Bash command\n  ls\n Do you want to proceed?\n❯ 1. Yes\n  2. Maybe",
		"gap":         "────────────────────────\n Bash command\n  ls\n Do you want to proceed?\n❯ 1. Yes\n  3. No",
	} {
		if p, ok := ClaudePermission(screen); ok {
			t.Fatalf("%s: recognised %+v", name, p)
		}
	}
}

func TestAskArgvDropsTheSkipFlagAndPassesAllowRules(t *testing.T) {
	spec := Spec{"claude", "sonnet", "low"}
	got, err := AskArgv("/bin/claude", spec, "fix it", "/w", []string{"Bash(git push:*)", "Bash(go test:*)"})
	want := []string{"/bin/claude", "--model", "sonnet", "--effort", "low", "--allowedTools=Bash(git push:*)", "--allowedTools=Bash(go test:*)", "fix it"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("argv = %q, %v", got, err)
	}
	got, err = AskArgv("/bin/claude", spec, "fix it", "/w", nil)
	if err != nil || !slices.Equal(got, []string{"/bin/claude", "--model", "sonnet", "--effort", "low", "fix it"}) {
		t.Fatalf("argv without grants = %q, %v", got, err)
	}
	if _, err := AskArgv("/bin/codex", Spec{"codex", "gpt-6-luna", "low"}, "fix it", "/w", nil); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("codex err = %v", err)
	}
}

func TestArgvOfADefaultProfileKeepsTheSkipFlag(t *testing.T) {
	got, err := Argv("/bin/claude", Spec{"claude", "sonnet", "low"}, "fix it", "/w")
	if err != nil || !slices.Equal(got, []string{"/bin/claude", "--dangerously-skip-permissions", "--model", "sonnet", "--effort", "low", "fix it"}) {
		t.Fatalf("argv = %q, %v", got, err)
	}
}

func TestPolicyApproveField(t *testing.T) {
	p, err := LoadPolicy(writePolicy(t, `{"profiles":{"safe":{"harness":"claude","model":"sonnet","effort":"low","approve":"ask"},"plain":{"harness":"claude","model":"sonnet","effort":"low","approve":"never"},"codex":{"harness":"codex","model":"gpt-6-luna","effort":"low"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := p.Profile("safe"); s.Approve != "ask" {
		t.Fatalf("safe = %+v", s)
	}
	if s, _ := p.Profile("codex"); s.Approve != "" {
		t.Fatalf("codex = %+v", s)
	}
	for _, body := range []string{
		`{"profiles":{"x":{"harness":"claude","model":"sonnet","effort":"low","approve":"always"}}}`,
		`{"profiles":{"x":{"harness":"codex","model":"gpt-6-luna","effort":"low","approve":"ask"}}}`,
		`{"profiles":{"x":{"harness":"opencode","model":"m","effort":"low","approve":"ask"}}}`,
	} {
		if _, err := LoadPolicy(writePolicy(t, body)); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), "approve") {
			t.Fatalf("policy %s err = %v", body, err)
		}
	}
}

func TestApproveTimeout(t *testing.T) {
	profiles := `"profiles":{"a":{"harness":"claude","model":"sonnet","effort":"low"}}`
	for body, want := range map[string]time.Duration{
		`{` + profiles + `}`:                         10 * time.Minute,
		`{` + profiles + `,"approve_timeout":"3m"}`:  3 * time.Minute,
		`{` + profiles + `,"approve_timeout":"24h"}`: 24 * time.Hour,
	} {
		p, err := LoadPolicy(writePolicy(t, body))
		if err != nil || p.ApproveWait() != want {
			t.Fatalf("%s: wait = %v, %v", body, p.ApproveWait(), err)
		}
	}
	for _, bad := range []string{`"30s"`, `"25h"`, `"soon"`, `5`, `null`} {
		if _, err := LoadPolicy(writePolicy(t, `{`+profiles+`,"approve_timeout":`+bad+`}`)); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), "approve_timeout") {
			t.Fatalf("approve_timeout %s err = %v", bad, err)
		}
	}
}

func TestTheStarterPolicyStaysFreeOfApprove(t *testing.T) {
	home := t.TempDir()
	if _, err := WriteStarterPolicy(home); err != nil {
		t.Fatal(err)
	}
	p, err := LoadPolicy(home)
	if err != nil || p.ApproveWait() != DefaultApproveTimeout {
		t.Fatalf("starter = %+v, %v", p, err)
	}
	for name, s := range p.Profiles {
		if s.Approve != "" {
			t.Fatalf("%s approve = %q", name, s.Approve)
		}
	}
}
