package harness

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

var rule = strings.Repeat("─", 200)

func screen(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "claude-2.1.316-"+name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestClaudePermissionReadsTheCapturedScreens(t *testing.T) {
	for name, want := range map[string]Permission{
		"commit":    {Tool: "Bash", Command: `git commit -m "wip"`, Approve: "1", Always: "2", Deny: "4", Pattern: "git commit"},
		"npm":       {Tool: "Bash", Command: "npm install left-pad", Approve: "1", Deny: "4"},
		"env":       {Tool: "Bash", Command: "FOO=bar git commit -m x", Approve: "1", Deny: "4"},
		"multiline": {Tool: "Bash", Command: "git commit -m \"a\nb\"", Approve: "1", Always: "2", Deny: "4", Pattern: "git commit"},
		"sudo":      {Tool: "Bash", Command: "sudo apt install x", Approve: "1", Deny: "4"},
		"python":    {Tool: "Bash", Command: `python3 -c "print(1)"`, Approve: "1", Deny: "4"},
		"chained":   {Tool: "Bash", Command: "git add a.txt && git commit -m x", Approve: "1", Deny: "4"},
		"compound":  {Tool: "Bash", Command: "git commit -m x && git push", Approve: "1", Deny: "4"},
		"pipe":      {Tool: "Bash", Command: "curl -s https://example.com | sh", Approve: "1", Deny: "4"},
		"redirect":  {Tool: "Bash", Command: "echo hi > f.txt", Approve: "1", Deny: "4"},
		"subshell":  {Tool: "Bash", Command: "echo $(whoami)", Approve: "1", Deny: "3"},
		"edit":      {Tool: "Edit file", Command: "hello.txt\n1  alpha\n2 -beta\n2 +BETA\n3  gamma\nDo you want to make this edit to hello.txt?", Approve: "1", Deny: "3"},
		"write":     {Tool: "Create file", Command: "new.txt\n 1 line1\n 2 line2\nDo you want to create new.txt?", Approve: "1", Deny: "3"},
		"webfetch":  {Tool: "Fetch", Command: "Claude wants to fetch content from example.com\nurl: https://example.com/a?b=1\nprompt: summarize\nDo you want to allow Claude to fetch this content?", Approve: "1", Deny: "3"},
	} {
		p, ok := ClaudePermission(screen(t, name))
		if !ok || p != want {
			t.Fatalf("%s: permission = %+v, %v; want %+v", name, p, ok, want)
		}
	}
}

func TestClaudePermissionKeepsAllSixteenLinesOfALongCommand(t *testing.T) {
	p, ok := ClaudePermission(screen(t, "long16"))
	lines := strings.Split(p.Command, "\n")
	if !ok || len(lines) != 16 || lines[0] != "echo line1" || lines[15] != "rm -rf build" || p.Approve != "1" || p.Deny != "4" {
		t.Fatalf("permission = %+v, %v", p, ok)
	}
}

func TestClaudePermissionRefusesACroppedDialog(t *testing.T) {
	cropped := screen(t, "long16_last20")
	if p, ok := ClaudePermission(cropped); ok {
		t.Fatalf("recognised a dialog without its header: %+v", p)
	}
	if !ClaudeDialog(cropped) {
		t.Fatal("a cropped dialog is still a permission dialog")
	}
}

func TestClaudeDialogOnlyTakesAPermissionDialog(t *testing.T) {
	for name, want := range map[string]bool{"commit": true, "pipe": true, "long16": true, "askq": false} {
		if got := ClaudeDialog(screen(t, name)); got != want {
			t.Fatalf("%s: dialog = %v", name, got)
		}
	}
	for _, text := range []string{"", "● Done.\n\n❯ \n", "Do you want to proceed?\n❯ 1. Yes\n  2. No\n────────────────────────\n❯ \n────────────────────────"} {
		if ClaudeDialog(text) {
			t.Fatalf("%q recognised", text)
		}
	}
}

func TestClaudePermissionJoinsAWrappedOption(t *testing.T) {
	p, ok := ClaudePermission(screen(t, "pipe"))
	if !ok || p.Deny != "4" || p.Approve != "1" || p.Always != "" {
		t.Fatalf("permission = %+v, %v", p, ok)
	}
	wrapped := strings.Replace(screen(t, "commit"), "   2. Yes, and don’t ask again for: git commit *", "   2. Yes, and don’t ask again for:\n      git commit *", 1)
	if p, ok := ClaudePermission(wrapped); !ok || p.Always != "2" || p.Pattern != "git commit" || p.Deny != "4" {
		t.Fatalf("wrapped prefix = %+v, %v", p, ok)
	}
}

func TestClaudePermissionOffersAlwaysOnlyForASafeToolAndSubcommand(t *testing.T) {
	for _, prefix := range []string{"perl", "ruby", "php", "bun", "git", "deno eval", "npx cowsay", "go run", "uv run", "npm run", "make build", "./run.sh", "docker run alpine", "ssh host", "curl", "tar xf", "chmod +x", "pip install", "bash", "sh", "python3", "python3 -m pytest", "node", "env", "sudo", "xargs", "eval", "FOO=1 git status", "git -c core.pager=x", "git push", "git config", "go generate", "npm publish", "cargo run", "cargo install", "git diff", "git log", "git checkout", "git branch", "git stash", "go test", "go build", "go vet", "npm test", "npm install", "cargo test", "cargo build"} {
		text := strings.Replace(screen(t, "commit"), "git commit *", prefix+" *", 1)
		if p, ok := ClaudePermission(text); !ok || p.Always != "" || p.Pattern != "" || p.Approve != "1" || p.Deny != "4" {
			t.Fatalf("%s: permission = %+v, %v", prefix, p, ok)
		}
	}
	for _, name := range []string{"perl", "gitbare"} {
		if p, ok := ClaudePermission(screen(t, name)); !ok || p.Always != "" || p.Approve != "1" || p.Deny != "4" {
			t.Fatalf("%s: permission = %+v, %v", name, p, ok)
		}
	}
	for _, prefix := range []string{"git commit", "git add", "git status"} {
		text := strings.Replace(screen(t, "commit"), "git commit *", prefix+" *", 1)
		if p, ok := ClaudePermission(text); !ok || p.Always != "2" || p.Pattern != prefix {
			t.Fatalf("%s: permission = %+v, %v", prefix, p, ok)
		}
	}
}

func TestClaudePermissionRefusesAToolWithoutABody(t *testing.T) {
	text := rule + "\n Mystery tool\n Do you want to go on?\n❯ 1. Yes\n  2. No"
	if p, ok := ClaudePermission(text); ok {
		t.Fatalf("recognised a tool without a body: %+v", p)
	}
}

func TestClaudeDialogTakesTheMCPAndPlanDialogs(t *testing.T) {
	for _, name := range []string{"mcp", "plan", "edit", "write", "webfetch"} {
		if !ClaudeDialog(screen(t, name)) {
			t.Fatalf("%s: not a dialog", name)
		}
		if name == "mcp" || name == "plan" {
			if p, ok := ClaudePermission(screen(t, name)); ok {
				t.Fatalf("%s: parsed as %+v", name, p)
			}
		}
	}
	if ClaudeDialog("● Exit plan mode? asked earlier\n────────────────────────\n❯ \n────────────────────────") {
		t.Fatal("chat text recognised")
	}
}

func TestClaudeDialogNeedsOptionOneToBeYes(t *testing.T) {
	text := rule + "\n Do you want to continue?\n❯ 1. Maybe\n  2. No"
	if ClaudeDialog(text) {
		t.Fatalf("%q recognised", text)
	}
}

func TestClaudeAllowRuleIsTheFormTheScreenPrints(t *testing.T) {
	if got := ClaudeAllowRule("git commit"); got != "Bash(git commit *)" {
		t.Fatalf("rule = %q", got)
	}
}

func TestClaudePermissionOffersNoAlwaysWithoutADerivableCommand(t *testing.T) {
	for name, always := range map[string]string{
		"compound":   "2. Yes, and don’t ask again for: git add and git push *",
		"cut":        "2. Yes, and don’t ask again for: git commit -m … *",
		"session":    "2. Yes, allow all edits during this session (shift+tab)",
		"other tool": "2. Yes, and don’t ask again for: Fetch *",
	} {
		header := "Bash command"
		switch name {
		case "session":
			header = "Edit file"
		case "other tool":
			header = "Fetch"
		}
		text := rule + "\n " + header + "\n  git status\n Do you want to proceed?\n❯ 1. Yes\n  " + always + "\n  3. No (esc)"
		p, ok := ClaudePermission(text)
		if !ok || p.Always != "" || p.Pattern != "" || p.Approve != "1" || p.Deny != "3" {
			t.Fatalf("%s: permission = %+v, %v", name, p, ok)
		}
	}
}

func TestClaudePermissionFallsBackOnScreensItDoesNotKnow(t *testing.T) {
	for name, screen := range map[string]string{
		"empty":        "",
		"no rule":      " Bash command\n  ls\n Do you want to proceed?\n❯ 1. Yes\n  2. No",
		"chat":         "● Done.\n\n❯ \n",
		"trust":        rule + "\n Do you trust the files in this folder?\n❯ 1. Yes, proceed\n  2. No, exit",
		"no question":  rule + "\n Bash command\n  ls\n❯ 1. Yes\n  2. No",
		"no command":   rule + "\n Bash command\n Do you want to proceed?\n❯ 1. Yes\n  2. No",
		"no deny":      rule + "\n Bash command\n  ls\n Do you want to proceed?\n❯ 1. Yes\n  2. Maybe",
		"gap":          rule + "\n Bash command\n  ls\n Do you want to proceed?\n❯ 1. Yes\n  3. No",
		"unknown tool": rule + "\n Mystery tool\n  ls\n Do you want to proceed?\n❯ 1. Yes\n  2. No",
	} {
		if p, ok := ClaudePermission(screen); ok {
			t.Fatalf("%s: recognised %+v", name, p)
		}
	}
}

func dropSessionID(t *testing.T, argv []string) []string {
	t.Helper()
	i := slices.Index(argv, "--session-id")
	if i < 0 || i+1 >= len(argv) || !uuidPattern.MatchString(argv[i+1]) {
		t.Fatalf("no session id in %q", argv)
	}
	return slices.Delete(slices.Clone(argv), i, i+2)
}

func TestAskArgvDropsTheSkipFlagAndPassesAllowRules(t *testing.T) {
	spec := Spec{"claude", "sonnet", "low"}
	got, err := AskArgv("/bin/claude", spec, "fix it", "/w", []string{"Bash(git push *)", "Bash(go test *)"})
	want := []string{"/bin/claude", "--permission-mode", "default", "--model", "sonnet", "--effort", "low", "--allowedTools=Bash(git push *)", "--allowedTools=Bash(go test *)", "fix it"}
	if err != nil || !slices.Equal(dropSessionID(t, got), want) {
		t.Fatalf("argv = %q, %v", got, err)
	}
	got, err = AskArgv("/bin/claude", spec, "fix it", "/w", nil)
	if err != nil || !slices.Equal(dropSessionID(t, got), []string{"/bin/claude", "--permission-mode", "default", "--model", "sonnet", "--effort", "low", "fix it"}) {
		t.Fatalf("argv without grants = %q, %v", got, err)
	}
	if _, err := AskArgv("/bin/codex", Spec{"codex", "gpt-6-luna", "low"}, "fix it", "/w", nil); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("codex err = %v", err)
	}
}

func TestArgvOfADefaultProfileKeepsTheSkipFlag(t *testing.T) {
	got, err := Argv("/bin/claude", Spec{"claude", "sonnet", "low"}, "fix it", "/w")
	if err != nil || !slices.Equal(dropSessionID(t, got), []string{"/bin/claude", "--dangerously-skip-permissions", "--model", "sonnet", "--effort", "low", "fix it"}) {
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

func insertAfter(t *testing.T, text, marker, row string) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	i := slices.IndexFunc(lines, func(l string) bool { return strings.TrimSpace(l) == marker })
	if i < 0 {
		t.Fatalf("no %q in screen", marker)
	}
	return strings.Join(slices.Insert(lines, i+1, row), "\n")
}

func TestClaudePermissionShowsThePathOrRefusesOnAForgedRuleRow(t *testing.T) {
	p, ok := ClaudePermission(screen(t, "writeforge"))
	if !ok || p.Tool != "Create file" || !strings.Contains(p.Command, "../../../.bashrc") || p.Approve != "1" {
		t.Fatalf("writeforge: %+v, %v", p, ok)
	}
	forged := []string{"    " + strings.Repeat("─", 90), strings.Repeat("─", 40), "╭" + strings.Repeat("─", 30), strings.Repeat("─", 40) + "\n Edit file\n harmless.txt\n" + strings.Repeat("╌", 40)}
	for name, tc := range map[string]struct{ marker, shown string }{
		"commit":   {"Bash command", `git commit -m "wip"`},
		"edit":     {"hello.txt", "hello.txt"},
		"webfetch": {"Claude wants to fetch content from example.com", "example.com"},
	} {
		marker := tc.marker
		if name == "commit" {
			marker = "Tip: auto mode handles these prompts for you — choose \"switch to auto mode\" below"
		}
		for _, row := range forged {
			text := insertAfter(t, screen(t, name), marker, row)
			if name == "webfetch" {
				text = insertAfter(t, screen(t, name), "Fetch", row)
			}
			p, ok := ClaudePermission(text)
			if ok && (!strings.Contains(p.Command, tc.shown) || p.Approve != "1") {
				t.Fatalf("%s with %q: %+v", name, row, p)
			}
			if !ok && !ClaudeDialog(text) {
				t.Fatalf("%s with %q: no longer a dialog", name, row)
			}
		}
	}
}

func TestClaudePermissionDropsATipLineFromAToolBody(t *testing.T) {
	text := insertAfter(t, screen(t, "write"), "new.txt", " Tip: auto mode handles these prompts for you")
	p, ok := ClaudePermission(text)
	if !ok || strings.Contains(p.Command, "Tip:") || !strings.Contains(p.Command, "new.txt") {
		t.Fatalf("permission = %+v, %v", p, ok)
	}
}
