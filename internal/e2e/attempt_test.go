package e2e_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/e2e/mockllm"
	"github.com/atqamz/hand/internal/state"
)

const reportBody = "Created hello.txt as briefed.\n\nThe e2e worker ran the scripted report command."

const briefTarget = 15000

func longBrief() string {
	paragraphs := []string{
		"Create a file named hello.txt in the repository root containing exactly: hello",
		"Quotes: \"double\" and 'single' and `backticks`, then a trailing backslash before a quote: \"C:\\Users\\name\\dir\\\" and a doubled one \\\\ at the end of a line\\",
		"Shell characters: %PATH% $HOME ${USER} $(whoami) ^caret & ampersand | pipe < less > greater ! bang ; semicolon * star ? mark # hash ~ tilde",
		"Unicode: Ünïcödé àèìòù ß ñ — “curly quotes” … ✓ 日本語のテキスト 한국어 العربية 🚀🎉 and a tab:\tdone",
		"A path with spaces: C:\\Program Files\\Some App\\bin and /home/user/my project/src",
	}
	var b strings.Builder
	for i := 0; ; i++ {
		p := fmt.Sprintf("Paragraph %d. %s", i+1, paragraphs[i%len(paragraphs)])
		if b.Len()+len(p)+2 > briefTarget {
			return b.String()
		}
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(p)
	}
}

func TestClaudeAttempt(t *testing.T) { runAttempt(t, claudeKit) }

func TestCodexAttempt(t *testing.T) { runAttempt(t, codexKit) }

func runAttempt(t *testing.T, k kit) {
	sb := newSandbox(t)
	repo := sb.repo("app")
	reportFile := filepath.Join(sb.root, "report.md")
	sb.write(reportFile, reportBody)
	sb.startMock(mockllm.Step{Shell: k.shell(handBin, "report", "add", "--status", "done", "--file", reportFile)}, mockllm.Step{Text: "Reported."})
	k.seed(sb, sb.mock.URL)
	sb.init()
	sb.ok("project", "add", "app", repo)
	sb.ok("task", "add", "--goal", "Say hello", "app", "Hello")
	sb.ok("task", "start", "t1")
	brief := longBrief()
	briefFile := filepath.Join(sb.root, "brief.md")
	sb.write(briefFile, brief)

	start := sb.ok(append(append([]string{"attempt", "start"}, k.flags()...), "--prompt-file", briefFile, "t1")...)
	worktree := field(start, "worktree")
	if want := filepath.Join(sb.shm, "worktrees", sb.id); !strings.HasPrefix(worktree, want) && !strings.HasPrefix(worktree, evalLink(want)) {
		t.Fatalf("worktree %q is not under %q\n%s", worktree, want, start)
	}
	if got := field(start, "trust"); got != "accepted" {
		t.Fatalf("trust = %q, want accepted\n%s", got, start)
	}
	if k.name == "codex" {
		saved := field(start, "trusted")
		if !strings.Contains(saved, "for folder "+worktree) || !strings.Contains(saved, repo+" in its config.toml") {
			t.Fatalf("trusted = %q, want it to name %s and %s\n%s", saved, worktree, repo, start)
		}
	}

	var first mockllm.Request
	sb.until("the harness's first model request", 90*time.Second, func() bool {
		var ok bool
		first, ok = sb.turn(0)
		return ok
	})
	if got := k.cwd(first.Strings()); !sameDir(got, worktree) {
		t.Fatalf("the harness ran in %q, not in the worktree %q", got, worktree)
	}
	show := sb.ok("attempt", "show", "a1")
	want := brief
	var got string
	for _, text := range first.UserTexts() {
		if strings.HasPrefix(text, want) {
			got = text
		}
	}
	if got == "" {
		t.Fatalf("no user message starts with the %d-byte brief; messages: %q", len(brief), clip(first.UserTexts()))
	}
	if n := field(show, "prompt_bytes"); n != strconv.Itoa(len(got)) {
		t.Fatalf("the model received %d prompt bytes, Hand sent %s", len(got), n)
	}
	footer := got[len(brief):]
	if !strings.Contains(footer, "When you finish, report to Hand") || !strings.HasSuffix(footer, "write a report only when the work is done or you are stuck.") {
		t.Fatalf("the prompt's report footer was altered: %q", footer)
	}

	sb.until("the worker's report", 90*time.Second, func() bool { return len(sb.reports()) > 0 })
	r := sb.reports()[0]
	if r.Status != state.ReportDone || strings.TrimSpace(r.Body) != reportBody || r.AttemptID != 1 || r.TaskID != 1 {
		t.Fatalf("report = %+v, want a done report of a1 on t1 with the scripted body", r)
	}
	var second mockllm.Request
	sb.until("the harness's second model request", 30*time.Second, func() bool {
		var ok bool
		second, ok = sb.turn(1)
		return ok
	})
	if outs := second.ToolOutputs(); len(outs) != 1 || !strings.Contains(outs[0], "report: r1") {
		t.Fatalf("the report command's output reached the model as %q", outs)
	}

	var screen string
	sb.until("the model's final message on the worker's screen", 60*time.Second, func() bool {
		screen = sb.hand("attempt", "read", "a1").out
		return strings.Contains(screen, "Reported.")
	})
	rev := regexp.MustCompile(`(?m)^revision: (\d+)`).FindStringSubmatch(screen)
	if rev == nil {
		t.Fatalf("attempt read printed no revision:\n%s", screen)
	}
	keys := sb.hand("attempt", "keys", "--revision", rev[1], "a1", "down", "up")
	if keys.code == 3 {
		screen = sb.ok("attempt", "read", "a1")
		rev = regexp.MustCompile(`(?m)^revision: (\d+)`).FindStringSubmatch(screen)
		keys = sb.hand("attempt", "keys", "--revision", rev[1], "a1", "down", "up")
	}
	if keys.code != 0 || field(keys.out, "keys") != "down up" {
		t.Fatalf("attempt keys: exit %d\n%s%s", keys.code, keys.out, keys.errOut)
	}

	events := sb.events()
	if k.name == "claude" && !hasEvent(events, "attempt.keys", "down enter") {
		t.Fatalf("no attempt.keys event for Claude's trust screen: %+v", events)
	}
	if k.name == "codex" && !hasEvent(events, "attempt.keys", "enter") {
		t.Fatalf("no attempt.keys event for Codex's trust screen: %+v", events)
	}
	if k.name == "codex" && !hasEvent(events, "attempt.trusted", "for folder "+worktree) {
		t.Fatalf("no attempt.trusted event naming %s: %+v", worktree, events)
	}

	sb.ok("attempt", "stop", "a1")
	if a := sb.attempt(1); a.Status != state.AttemptStopped {
		t.Fatalf("attempt status = %s, want %s", a.Status, state.AttemptStopped)
	}
	sb.ok("attempt", "clean", "a1")
	if _, err := os.Stat(worktree); err == nil {
		t.Fatalf("worktree %s survived attempt clean", worktree)
	}
	if a := sb.attempt(1); a.CleanedAt == "" {
		t.Fatalf("attempt has no cleaned_at after clean")
	} else if out := sb.git(repo, "branch", "--list", a.Branch); !strings.Contains(out, a.Branch) {
		t.Fatalf("branch %s was removed by attempt clean", a.Branch)
	}
}

func hasEvent(events []state.Event, kind, detail string) bool {
	for _, e := range events {
		if e.Kind == kind && strings.Contains(e.Detail, detail) {
			return true
		}
	}
	return false
}

func evalLink(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func clip(texts []string) []string {
	out := make([]string, len(texts))
	for i, s := range texts {
		if len(s) > 120 {
			s = s[:120] + "..."
		}
		out[i] = s
	}
	return out
}
