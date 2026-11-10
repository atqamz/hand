package harness

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
		"commit":    {Tool: "Bash", Command: `git commit -m "wip"`, Approve: "1", Deny: "4"},
		"npm":       {Tool: "Bash", Command: "npm install left-pad", Approve: "1", Deny: "4"},
		"env":       {Tool: "Bash", Command: "FOO=bar git commit -m x", Approve: "1", Deny: "4"},
		"multiline": {Tool: "Bash", Command: "git commit -m \"a\nb\"", Approve: "1", Deny: "4"},
		"sudo":      {Tool: "Bash", Command: "sudo apt install x", Approve: "1", Deny: "4"},
		"python":    {Tool: "Bash", Command: `python3 -c "print(1)"`, Approve: "1", Deny: "4"},
		"chained":   {Tool: "Bash", Command: "git add a.txt && git commit -m x", Approve: "1", Deny: "4"},
		"compound":  {Tool: "Bash", Command: "git commit -m x && git push", Approve: "1", Deny: "4"},
		"pipe":      {Tool: "Bash", Command: "curl -s https://example.com | sh", Approve: "1", Deny: "4"},
		"redirect":  {Tool: "Bash", Command: "echo hi > f.txt", Approve: "1", Deny: "4"},
		"subshell":  {Tool: "Bash", Command: "echo $(whoami)", Note: "A nested command in this command can't be checked before it runs", Approve: "1", Deny: "3"},
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
	wrapped := strings.Replace(screen(t, "commit"), "   2. Yes, and don’t ask again for: git commit *", "   2. Yes, and don’t ask again for:\n      git status *", 1)
	if p, ok := ClaudePermission(wrapped); !ok || p.Always != "2" || p.Pattern != "git status" || p.Deny != "4" {
		t.Fatalf("wrapped prefix = %+v, %v", p, ok)
	}
}

func TestClaudePermissionOffersAlwaysOnlyForASafeToolAndSubcommand(t *testing.T) {
	for _, prefix := range []string{"perl", "ruby", "php", "bun", "git", "deno eval", "npx cowsay", "go run", "uv run", "npm run", "make build", "./run.sh", "docker run alpine", "ssh host", "curl", "git commit", "git add", "tar xf", "chmod +x", "pip install", "bash", "sh", "python3", "python3 -m pytest", "node", "env", "sudo", "xargs", "eval", "FOO=1 git status", "git -c core.pager=x", "git push", "git config", "go generate", "npm publish", "cargo run", "cargo install", "git diff", "git log", "git checkout", "git branch", "git stash", "go test", "go build", "go vet", "npm test", "npm install", "cargo test", "cargo build"} {
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
	text := strings.Replace(screen(t, "commit"), "git commit *", "git status *", 1)
	if p, ok := ClaudePermission(text); !ok || p.Always != "2" || p.Pattern != "git status" {
		t.Fatalf("git status: permission = %+v, %v", p, ok)
	}
}

func TestClaudePermissionRefusesAToolWithoutABody(t *testing.T) {
	text := rule + "\n Mystery tool\n Do you want to go on?\n❯ 1. Yes\n  2. No"
	if p, ok := ClaudePermission(text); ok {
		t.Fatalf("recognised a tool without a body: %+v", p)
	}
}

func TestClaudeDialogRefusesAnAskUserQuestionWordedAsAPermission(t *testing.T) {
	text := strings.Replace(screen(t, "askq"), "Delete the build folder?", "Do you want to delete the build folder?", 1)
	if ClaudeDialog(text) {
		t.Fatalf("%q recognised", text)
	}
}

func TestClaudeDialogTakesTheMCPAndPlanDialogs(t *testing.T) {
	for _, name := range []string{"mcp", "mcp2", "import", "plan", "edit", "write", "webfetch", "unreviewed"} {
		if !ClaudeDialog(screen(t, name)) {
			t.Fatalf("%s: not a dialog", name)
		}
		if name == "mcp" || name == "mcp2" || name == "import" || name == "plan" || name == "unreviewed" {
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
	if got := ClaudeAllowRule("git status"); got != "Bash(git status *)" {
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
	got, err := AskArgv("/bin/claude", spec, "fix it", "/w", "", []string{"Bash(git push *)", "Bash(go test *)"})
	want := []string{"/bin/claude", "--permission-mode", "default", "--model", "sonnet", "--effort", "low", "--strict-mcp-config", "--allowedTools=Bash(git push *)", "--allowedTools=Bash(go test *)", "fix it"}
	if err != nil || !slices.Equal(dropSessionID(t, got), want) {
		t.Fatalf("argv = %q, %v", got, err)
	}
	got, err = AskArgv("/bin/claude", spec, "fix it", "/w", "", nil)
	if err != nil || !slices.Equal(dropSessionID(t, got), []string{"/bin/claude", "--permission-mode", "default", "--model", "sonnet", "--effort", "low", "--strict-mcp-config", "fix it"}) {
		t.Fatalf("argv without grants = %q, %v", got, err)
	}
	if _, err := AskArgv("/bin/codex", Spec{"codex", "gpt-6-luna", "low"}, "fix it", "/w", "", nil); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("codex err = %v", err)
	}
}

func TestArgvOfADefaultProfileKeepsTheSkipFlag(t *testing.T) {
	got, err := Argv("/bin/claude", Spec{"claude", "sonnet", "low"}, "fix it", "/w", "")
	if err != nil || !slices.Equal(dropSessionID(t, got), []string{"/bin/claude", "--dangerously-skip-permissions", "--model", "sonnet", "--effort", "low", "--strict-mcp-config", "fix it"}) {
		t.Fatalf("argv = %q, %v", got, err)
	}
}

func TestMCPFileAddsExactlyOneConfigToEveryClaudeLaunch(t *testing.T) {
	spec := Spec{"claude", "sonnet", "low"}
	file := filepath.Join("/fleet", "mcp.json")
	flag := "--mcp-config=" + file
	launches := map[string]func(mcp string) ([]string, error){
		"default": func(mcp string) ([]string, error) { return Argv("/bin/claude", spec, "fix it", "/w", mcp) },
		"ask":     func(mcp string) ([]string, error) { return AskArgv("/bin/claude", spec, "fix it", "/w", mcp, nil) },
		"readonly": func(mcp string) ([]string, error) {
			return ReadonlyArgv("/bin/claude", spec, "review it", "/w", mcp, nil)
		},
	}
	for name, launch := range launches {
		plain, err := launch("")
		if err != nil || !slices.Contains(plain, "--strict-mcp-config") || slices.ContainsFunc(plain, func(a string) bool { return strings.HasPrefix(a, "--mcp-config") }) {
			t.Fatalf("%s without mcp = %q, %v", name, plain, err)
		}
		with, err := launch(file)
		if err != nil || strings.Count(strings.Join(with, "\x00"), "--mcp-config") != 1 || !slices.Contains(with, flag) || !slices.Contains(with, "--strict-mcp-config") || with[len(with)-1] != plain[len(plain)-1] {
			t.Fatalf("%s with mcp = %q, %v", name, with, err)
		}
	}
}

func TestMCPIsRefusedForOtherHarnessesAndOutsideTheFleetHome(t *testing.T) {
	for _, s := range []Spec{{"codex", "gpt-6-luna", "low"}, {Harness: "opencode"}, {"agy", "gemini-3.8-flash-low", ""}, {"copilot", "auto", ""}} {
		if _, err := Argv("/bin/x", s, "fix it", "/w", "mcp.json"); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), "mcp is only available for claude, not "+s.Harness) {
			t.Fatalf("%s err = %v", s.Harness, err)
		}
	}
	for _, bad := range []string{"../mcp.json", "/etc/mcp.json", "a/../../mcp.json"} {
		if err := checkMCP("x", Profile{Spec: Spec{"claude", "sonnet", "low"}, MCP: bad}); err == nil || !strings.Contains(err.Error(), "inside the fleet home") {
			t.Fatalf("mcp %q err = %v", bad, err)
		}
	}
	if err := checkMCP("look", Profile{Spec: Spec{"claude", "sonnet", "low"}, Access: state.AccessReadonly, MCP: "mcp.json"}); err == nil || !strings.Contains(err.Error(), "leave mcp out") {
		t.Fatalf("readonly with mcp err = %v", err)
	}
}

func TestMCPFileNamesTheFileInsideTheFleetHome(t *testing.T) {
	home := t.TempDir()
	pr := Profile{Spec: Spec{"claude", "sonnet", "low"}, MCP: "mcp.json"}
	if path, err := (Profile{}).MCPFile(home); path != "" || err != nil {
		t.Fatalf("no mcp = %q, %v", path, err)
	}
	if _, err := pr.MCPFile(home); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), filepath.Join(home, "mcp.json")) {
		t.Fatalf("missing file err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "mcp.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(home, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if path, err := pr.MCPFile(home); path != want || err != nil {
		t.Fatalf("file = %q, %v", path, err)
	}
}

func TestReadonlyArgvRunsClaudeInDontAskModeWithOnlyTheReportAndPrView(t *testing.T) {
	spec := Spec{"claude", "sonnet", "low"}
	got, err := ReadonlyArgv("/bin/claude", spec, "review it", "/w", "", []string{"Bash('/bin/hand' report add *)"})
	want := []string{"/bin/claude", "--permission-mode", "dontAsk", "--model", "sonnet", "--effort", "low", "--setting-sources=user", "--strict-mcp-config", "--tools=Bash,Read,Grep,Glob", "--allowedTools=Bash(gh pr view *)", "--allowedTools=Bash('/bin/hand' report add *)", "review it"}
	if err != nil || !slices.Equal(dropSessionID(t, got), want) {
		t.Fatalf("argv = %q, %v", got, err)
	}
	for _, s := range []Spec{{"codex", "gpt-6-luna", "low"}, {Harness: "opencode"}, {"agy", "gemini-3.8-flash-low", ""}, {"copilot", "auto", ""}} {
		if _, err := ReadonlyArgv("/bin/x", s, "review it", "/w", "", nil); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), "only available for claude, not "+s.Harness) {
			t.Fatalf("%s err = %v", s.Harness, err)
		}
	}
}

func TestReadonlyIsRefusedOnWindowsOnly(t *testing.T) {
	pr := Profile{Spec: Spec{"claude", "sonnet", "low"}, Access: state.AccessReadonly}
	if err := checkAccess("linux", "look", pr); err != nil {
		t.Fatal(err)
	}
	if err := checkAccess("windows", "look", pr); err == nil || !strings.Contains(err.Error(), `access "readonly" is not available on Windows`) {
		t.Fatalf("err = %v", err)
	}
	pr.Access = state.AccessOpen
	if err := checkAccess("windows", "look", pr); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyAccessField(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("read-only access is refused on Windows")
	}
	p, err := LoadPolicy(writePolicy(t, `{"profiles":{"look":{"harness":"claude","model":"sonnet","effort":"low","access":"readonly"},"open":{"harness":"claude","model":"sonnet","effort":"low","access":"open"},"plain":{"harness":"claude","model":"sonnet","effort":"low"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"look": "readonly", "open": "open", "plain": ""} {
		if s, _ := p.Profile(name); s.Access != want {
			t.Fatalf("%s = %+v", name, s)
		}
	}
	for body, want := range map[string]string{
		`{"profiles":{"x":{"harness":"claude","model":"sonnet","effort":"low","access":"write"}}}`:                    `access "write" must be "open" or "readonly"`,
		`{"profiles":{"x":{"harness":"codex","model":"gpt-6-luna","effort":"low","access":"readonly"}}}`:              `access "readonly" is only available for claude, not codex`,
		`{"profiles":{"x":{"harness":"claude","model":"sonnet","effort":"low","access":"readonly","approve":"ask"}}}`: `cannot ask for approval`,
	} {
		if _, err := LoadPolicy(writePolicy(t, body)); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: err = %v", body, err)
		}
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

func TestClaudeDialogTakesAQuestionThatWrapsOverTwoRows(t *testing.T) {
	text := strings.Replace(screen(t, "write"), " Do you want to create new.txt?", " Do you want to create a-name-that-is-long-enough-to-wrap-the-\nquestion-over-two-rows.txt?", 1)
	if !ClaudeDialog(text) {
		t.Fatal("a wrapped question is not a dialog")
	}
}

func TestClaudeIdleTakesOnlyTheInputPrompt(t *testing.T) {
	idle := screen(t, "idle")
	if !ClaudeIdle(idle) {
		t.Fatal("the captured input prompt is not idle")
	}
	for name, text := range map[string]string{
		"typed text":      strings.Replace(idle, "❯\u00a0", "❯ half a sentence", 1),
		"two input rows":  strings.Replace(idle, "❯\u00a0", "❯ one\n  two", 1),
		"trailing blanks": idle + "\n\n\n",
	} {
		if !ClaudeIdle(text) {
			t.Fatalf("%s: not idle", name)
		}
	}
	lines := strings.Split(strings.TrimRight(idle, "\n"), "\n")
	bottom := len(lines) - 2
	for name, text := range map[string]string{
		"empty":            "",
		"no footer":        strings.Join(lines[:len(lines)-1], "\n"),
		"no bottom rule":   strings.Join(slices.Delete(slices.Clone(lines), bottom, bottom+1), "\n"),
		"no input row":     strings.Join(slices.Delete(slices.Clone(lines), bottom-1, bottom), "\n"),
		"a shorter rule":   strings.Join(slices.Replace(slices.Clone(lines), bottom, bottom+1, strings.Repeat("─", 40)), "\n"),
		"an indented rule": strings.Join(slices.Replace(slices.Clone(lines), bottom, bottom+1, " "+lines[bottom]), "\n"),
		"no prompt mark":   strings.Replace(idle, "❯\u00a0", "> ", 1),
		"a dialog":         strings.Join(lines[:bottom-1], "\n") + "\n" + screen(t, "commit"),
	} {
		if ClaudeIdle(text) {
			t.Fatalf("%s: idle", name)
		}
	}
	for _, name := range []string{"commit", "pipe", "long16", "long16_last20", "edit", "write", "webfetch", "askq", "mcp", "mcp2", "import", "plan", "trust", "unreviewed", "writeforge", "idle_statusline", "idle_statusline2"} {
		if ClaudeIdle(screen(t, name)) {
			t.Fatalf("%s: idle", name)
		}
	}
}

func TestClaudeIdleIsNotForgedByTextAboveADialog(t *testing.T) {
	forged := screen(t, "idle") + "\n" + screen(t, "mcp2")
	if ClaudeIdle(forged) {
		t.Fatal("an input prompt above a dialog is idle")
	}
}

func TestClaudeTitleIsTheDialogsOwnFirstRow(t *testing.T) {
	for name, want := range map[string]string{
		"mcp":        "New MCP server found in this project: evil",
		"mcp2":       "2 new MCP servers found in this project",
		"import":     "Allow external CLAUDE.md file imports?",
		"commit":     "Bash command",
		"unreviewed": "Create file",
	} {
		if got := ClaudeTitle(screen(t, name)); got != want {
			t.Fatalf("%s: title = %q, want %q", name, got, want)
		}
	}
	if got := ClaudeTitle(rule + "\n " + strings.Repeat("w", 500)); len([]rune(got)) != questionMax+1 {
		t.Fatalf("long title has %d runes", len([]rune(got)))
	}
}

func TestClaudePermissionRefusesContentClaudeSaysItCannotShow(t *testing.T) {
	text := screen(t, "unreviewed")
	if p, ok := ClaudePermission(text); ok {
		t.Fatalf("offered Approve for content nobody can read: %+v", p)
	}
	if p, ok := ClaudePermission(strings.Replace(text, "cannot be shown in full — cannot be reviewed", "was cut", 1)); !ok || p.Tool != "Create file" {
		t.Fatalf("without the warning: %+v, %v", p, ok)
	}
}

func TestAForgedRuleRowDoesNotCutTheDialog(t *testing.T) {
	base := screen(t, "write")
	width := 0
	for _, l := range strings.Split(base, "\n") {
		width = max(width, len([]rune(l)))
	}
	for name, row := range map[string]string{
		"a row narrower than the pane": strings.Repeat("─", width-1),
		"an indented row":              " " + strings.Repeat("─", width-1),
		"a row of the corner glyph":    "╭" + strings.Repeat("─", width-1),
	} {
		p, ok := ClaudePermission(insertAfter(t, base, "new.txt", row))
		if !ok || p.Tool != "Create file" || p.Approve != "1" || !strings.Contains(p.Command, "line2") {
			t.Fatalf("%s: %+v, %v", name, p, ok)
		}
	}
}

func TestMCPPathNamesTheFileInsideTheFleetHome(t *testing.T) {
	home := t.TempDir()
	if path, err := MCPPath(home, ""); path != "" || err != nil {
		t.Fatalf("no mcp = %q, %v", path, err)
	}
	for _, bad := range []string{"../mcp.json", "/etc/mcp.json", "a/../../mcp.json"} {
		if _, err := MCPPath(home, bad); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), "inside the fleet home") {
			t.Fatalf("mcp %q err = %v", bad, err)
		}
	}
	if err := os.Mkdir(filepath.Join(home, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"mcp.json", "dir"} {
		if _, err := MCPPath(home, bad); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), filepath.Join(home, bad)) {
			t.Fatalf("%s err = %v", bad, err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "mcp.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(home, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if path, err := MCPPath(home, "mcp.json"); path != want || err != nil {
		t.Fatalf("file = %q, %v", path, err)
	}
}

func TestMCPPathFollowsSymlinksOnlyInsideTheFleetHome(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	write := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, name string) {
		t.Helper()
		if err := os.Symlink(target, filepath.Join(home, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	write(filepath.Join(outside, "evil.json"))
	write(filepath.Join(outside, "worktrees", "t1-a1", "mcp.json"))
	write(filepath.Join(home, "scratch", "a1", "x.json"))
	write(filepath.Join(home, "configs", "good.json"))
	link(filepath.Join(outside, "evil.json"), "file.json")
	link(outside, "dirlink")
	link(filepath.Join(outside, "worktrees", "t1-a1"), "worktree")
	link(filepath.Join(home, "scratch", "a1", "x.json"), "scratch.json")
	link(filepath.Join(home, "scratch"), "scratchdir")
	link(filepath.Join(home, "configs", "good.json"), "good.json")
	link(filepath.Join(home, "configs"), "configs-link")
	for file, want := range map[string]string{
		"file.json":            "outside the fleet home",
		"dirlink/evil.json":    "outside the fleet home",
		"worktree/mcp.json":    "outside the fleet home",
		"scratch/a1/x.json":    "scratch/",
		"scratch.json":         "scratch/",
		"scratchdir/a1/x.json": "scratch/",
	} {
		if _, err := MCPPath(home, file); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s err = %v, want %q", file, err, want)
		}
		if _, err := (Profile{MCP: file}).MCPFile(home); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), want) {
			t.Fatalf("profile %s err = %v, want %q", file, err, want)
		}
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(realHome, "configs", "good.json")
	for _, file := range []string{"good.json", "configs-link/good.json", "configs/good.json"} {
		if path, err := MCPPath(home, file); path != want || err != nil {
			t.Fatalf("%s = %q, %v", file, path, err)
		}
		if path, err := (Profile{MCP: file}).MCPFile(home); path != want || err != nil {
			t.Fatalf("profile %s = %q, %v", file, path, err)
		}
	}
}
