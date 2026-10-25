package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
)

const (
	codexNewQuestion = "Trust this folder? Codex can read, edit, and run files here, subject to your permission settings. Folder settings can run code automatically, even without a model request. Continue only if you trust these files. Your trust decision will be saved."
	codexOldQuestion = "Do you trust the contents of this directory? Working with untrusted contents comes with higher risk of prompt injection. Trusting the directory allows project-local config, hooks, and exec policies to load."
	codexRepoNote    = "Note: You’re in a subdirectory of a Git project. Trusting will apply to the repository root:"
	codexResume      = "\n  Working directory · Resume\n\n› 1. Use session directory (C:\\old)\n  2. Use current directory (C:\\new)\n"
)

func wrapped(width int, text string) string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		for r := []rune(word); len(r) > width; r = []rune(word) {
			if line != "" {
				lines, line = append(lines, line), ""
			}
			lines, word = append(lines, string(r[:width])), string(r[width:])
		}
		switch {
		case line == "":
			line = word
		case len([]rune(line))+1+len([]rune(word)) <= width:
			line += " " + word
		default:
			lines, line = append(lines, line), word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	for i := range lines {
		lines[i] = "  " + lines[i]
	}
	return strings.Join(lines, "\n")
}

func codexTrustScreen(old bool, width int, folder, root string, onQuit bool) string {
	trust, quit, question := "Trust and continue", "Quit", codexNewQuestion
	head := "\n  Folder access\n  " + folder + "\n\n"
	note, foot := "", "\n  enter continue · esc quit\n"
	if root != "" {
		note = wrapped(width-2, codexRepoNote) + "\n  " + root + "\n\n"
	}
	if old {
		trust, quit, question = "Yes, continue", "No, quit", codexOldQuestion
		head, foot = "\n> You are in "+folder+"\n\n", "\n  Press enter to continue\n"
		if root != "" {
			note = wrapped(width-2, codexRepoNote+" "+root) + "\n\n"
		}
	}
	rows := "› 1. " + trust + "\n  2. " + quit
	if onQuit {
		rows = "  1. " + trust + "\n› 2. " + quit
	}
	return head + note + wrapped(width-2, question) + "\n\n" + rows + "\n" + foot
}

type codexRun struct {
	trust, note, saved string
	keys               []string
	rt                 *fakeRuntime
}

func runCodexTrust(t *testing.T, worktree string, repos []string, set func(*fakeRuntime)) codexRun {
	t.Helper()
	wait, confirm := *cli.TrustWait, *cli.TrustConfirm
	*cli.TrustWait, *cli.TrustConfirm = time.Second, time.Second
	t.Cleanup(func() { *cli.TrustWait, *cli.TrustConfirm = wait, confirm })
	rt := startRuntime(t, fakeuhp.ShortSocket(t))
	c := luvus.Client{Socket: rt.srv.Socket}
	term, err := c.Create(context.Background(), t.TempDir(), "hand-a1", []string{exe(fakeBin(t), "codex")})
	if err != nil {
		t.Fatal(err)
	}
	rt.set(set)
	trust, note, keys, saved := cli.AcceptTrust(context.Background(), c, "codex", term.PaneID, term.TerminalID, worktree, repos)
	return codexRun{trust, note, saved, keys, rt}
}

var codexPaths = []struct {
	name, worktree, repo, shownWorktree, shownRepo string
}{
	{"posix", "/home/me/.secondhand/worktrees/abc123/t1-a1", "/home/me/repo", "/home/me/.secondhand/worktrees/abc123/t1-a1", "/home/me/repo"},
	{"windows", `C:\Users\me\.secondhand\worktrees\abc123\t1-a1`, `C:\Users\me\repo`, `C:\Users\me\.secondhand\worktrees\abc123\t1-a1`, `C:\Users\me\repo`},
	{"space and non-ASCII", `C:\Users\Zoë Müller\.secondhand\worktrees\abc123\t1-a1`, `C:\Users\Zoë Müller\Projects\hand`, `C:\Users\Zoë Müller\.secondhand\worktrees\abc123\t1-a1`, `C:\Users\Zoë Müller\Projects\hand`},
	{"forward slashes", `C:\Users\me\.secondhand\worktrees\abc123\t1-a1`, `C:\Users\me\repo`, `C:/Users/me/.secondhand/worktrees/abc123/t1-a1`, `C:/Users/me/repo`},
	{"other case", `C:\Users\Zoë\.secondhand\worktrees\abc123\t1-a1`, `C:\Users\Zoë\Repo`, `c:\users\ZOË\.secondhand\worktrees\abc123\t1-A1`, `c:\USERS\zoë\repo`},
	{"verbatim", `C:\Users\me\.secondhand\worktrees\abc123\t1-a1`, `C:\Users\me\repo`, `\\?\C:\Users\me\.secondhand\worktrees\abc123\t1-a1`, `\\?\C:\Users\me\repo`},
}

func TestCodexTrustScreensAreAcceptedForTheirOwnFolderAndRepository(t *testing.T) {
	for _, old := range []bool{false, true} {
		for _, width := range []int{60, 120} {
			for _, p := range codexPaths {
				for _, root := range []string{p.shownRepo, ""} {
					name := map[bool]string{false: "new", true: "old"}[old] + "/" + p.name + "/" + strings.ReplaceAll(strings.TrimSpace(root+" no note"), "\\", "/")
					t.Run(name, func(t *testing.T) {
						screen := codexTrustScreen(old, width, p.shownWorktree, root, false)
						run := runCodexTrust(t, p.worktree, []string{p.repo}, func(rt *fakeRuntime) { rt.screen, rt.afterScreen = screen, "› \n" })
						if run.trust != "accepted" || run.note != "" || !slices.Equal(run.keys, []string{"enter"}) || !slices.Equal(run.rt.keysSent(), []string{"enter"}) {
							t.Fatalf("width %d root %q: trust=%q note=%q keys=%q\n%s", width, root, run.trust, run.note, run.keys, screen)
						}
						want := "this folder"
						if root != "" {
							want = p.repo
						}
						if saved := `codex accepted "` + map[bool]string{false: "Trust this folder?", true: "Do you trust the contents of this directory?"}[old] + `" for folder ` + p.worktree + "; it saves the trust for " + want + " in its config.toml"; run.saved != saved {
							t.Fatalf("saved = %q, want %q", run.saved, saved)
						}
					})
				}
			}
		}
	}
}

func TestCodexTrustScreenWrapsItsPathsAcrossLines(t *testing.T) {
	wt, repo := `C:\Users\Zoë Müller\.secondhand\worktrees\fab28ae721878\t209-a233`, `C:\Users\Zoë Müller\Documents\Projects\hand`
	for _, old := range []bool{false, true} {
		screen := strings.Replace(codexTrustScreen(old, 60, wt, repo, false), wt, "   \n  "+wt[:24]+"   \n  "+wt[24:]+"  ", 1)
		run := runCodexTrust(t, wt, []string{repo}, func(rt *fakeRuntime) { rt.screen, rt.afterScreen = screen, "› \n" })
		if run.trust != "accepted" || !slices.Equal(run.keys, []string{"enter"}) {
			t.Fatalf("old=%v trust=%q note=%q keys=%q\n%s", old, run.trust, run.note, run.keys, screen)
		}
	}
}

func TestCodexTrustScreenForAnotherFolderIsLeftAlone(t *testing.T) {
	wt := `C:\Users\me\.secondhand\worktrees\abc123\t1-a1`
	for name, folder := range map[string]string{
		"windows":  `C:\Windows`,
		"parent":   `C:\Users\me\.secondhand\worktrees\abc123`,
		"sibling":  wt + "2",
		"child":    wt + `\src`,
		"prefix":   `C:\Users\me\.secondhand\worktrees\abc123\t1-a`,
		"drive":    `D:\Users\me\.secondhand\worktrees\abc123\t1-a1`,
		"fleet":    `C:\Users\me\.secondhand\worktrees\other\t1-a1`,
		"verbatim": `\\?\C:\Users\me\.secondhand\worktrees\abc123\t1-a12`,
	} {
		for _, old := range []bool{false, true} {
			t.Run(map[bool]string{false: "new", true: "old"}[old]+"/"+name, func(t *testing.T) {
				screen := codexTrustScreen(old, 120, folder, `C:\Users\me\repo`, false)
				run := runCodexTrust(t, wt, []string{`C:\Users\me\repo`}, func(rt *fakeRuntime) { rt.screen = screen })
				if run.trust != "not pressed" || run.note != "codex asks to trust a folder that is not its worktree or project; check the screen" || len(run.keys) != 0 || len(run.rt.keysSent()) != 0 || run.saved != "" {
					t.Fatalf("trust=%q note=%q keys=%q saved=%q", run.trust, run.note, run.rt.keysSent(), run.saved)
				}
			})
		}
	}
}

func TestCodexTrustScreenNamingAnotherRepositoryIsLeftAlone(t *testing.T) {
	wt, repo := `C:\Users\me\.secondhand\worktrees\abc123\t1-a1`, `C:\Users\me\repo`
	for name, root := range map[string]string{
		"other":   `C:\Users\me\other`,
		"parent":  `C:\Users\me`,
		"sibling": repo + "2",
		"child":   repo + `\sub`,
		"drive":   `D:\Users\me\repo`,
		"home":    `C:\Users\me\.secondhand`,
	} {
		for _, old := range []bool{false, true} {
			t.Run(map[bool]string{false: "new", true: "old"}[old]+"/"+name, func(t *testing.T) {
				screen := codexTrustScreen(old, 120, wt, root, false)
				run := runCodexTrust(t, wt, []string{repo}, func(rt *fakeRuntime) { rt.screen = screen })
				if run.trust != "not pressed" || run.note != "codex asks to trust a folder that is not its worktree or project; check the screen" || len(run.rt.keysSent()) != 0 {
					t.Fatalf("trust=%q note=%q keys=%q", run.trust, run.note, run.rt.keysSent())
				}
			})
		}
	}
}

func TestCodexTrustScreenWithAnUnreadableRepositoryNoteIsLeftAlone(t *testing.T) {
	wt := `C:\Users\me\.secondhand\worktrees\abc123\t1-a1`
	for _, old := range []bool{false, true} {
		screen := strings.Replace(codexTrustScreen(old, 120, wt, `C:\Users\me\repo`, false), "repository root:", "repository:", 1)
		run := runCodexTrust(t, wt, []string{`C:\Users\me\repo`}, func(rt *fakeRuntime) { rt.screen = screen })
		if run.trust != "not pressed" || len(run.rt.keysSent()) != 0 {
			t.Fatalf("old=%v trust=%q keys=%q\n%s", old, run.trust, run.rt.keysSent(), screen)
		}
	}
}

func TestCodexTrustScreenWithTheRepositoryNoteAfterTheQuestionIsStillChecked(t *testing.T) {
	wt, repo := `C:\Users\me\.secondhand\worktrees\abc123\t1-a1`, `C:\Users\me\repo`
	for _, old := range []bool{false, true} {
		for root, want := range map[string]string{repo: "accepted", `C:\Users\me\other`: "not pressed"} {
			screen := codexTrustScreen(old, 120, wt, "", false) + "\n" + wrapped(118, codexRepoNote) + "\n  " + root + "\n"
			run := runCodexTrust(t, wt, []string{repo}, func(rt *fakeRuntime) { rt.screen, rt.afterScreen = screen, "› \n" })
			if run.trust != want || (want == "not pressed" && len(run.rt.keysSent()) != 0) {
				t.Fatalf("old=%v root %q: trust=%q keys=%q\n%s", old, root, run.trust, run.rt.keysSent(), screen)
			}
		}
	}
}

func TestCodexTrustScreenToleratesAWrappedAwaySpaceButNeverAnAddedOne(t *testing.T) {
	for name, c := range map[string]struct{ want, shown, trust string }{
		"dropped":      {`C:\Users\a b\wt`, `C:\Users\ab\wt`, "accepted"},
		"kept":         {`C:\Users\a b\wt`, `C:\Users\a b\wt`, "accepted"},
		"two, one":     {`C:\Users\a b c\wt`, `C:\Users\a bc\wt`, "accepted"},
		"added":        {`C:\Users\ab\wt`, `C:\Users\a b\wt`, "not pressed"},
		"other letter": {`C:\Users\a b\wt`, `C:\Users\ac\wt`, "not pressed"},
		"truncated":    {`C:\Users\a b\wt`, `C:\Users\a b\w`, "not pressed"},
	} {
		for _, old := range []bool{false, true} {
			t.Run(map[bool]string{false: "new", true: "old"}[old]+"/"+name, func(t *testing.T) {
				screen := codexTrustScreen(old, 120, c.shown, "", false)
				run := runCodexTrust(t, c.want, nil, func(rt *fakeRuntime) { rt.screen, rt.afterScreen = screen, "› \n" })
				if run.trust != c.trust {
					t.Fatalf("trust=%q keys=%q\n%s", run.trust, run.rt.keysSent(), screen)
				}
			})
		}
	}
}

func TestCodexTrustScreenWithNoProjectOnlyAcceptsItsOwnFolder(t *testing.T) {
	wt := `C:\Users\me\fleet`
	for _, old := range []bool{false, true} {
		for name, root := range map[string]string{"with a note": `C:\Users\me`, "without": ""} {
			screen := codexTrustScreen(old, 120, wt, root, false)
			run := runCodexTrust(t, wt, nil, func(rt *fakeRuntime) { rt.screen, rt.afterScreen = screen, "› \n" })
			if want := map[string]string{"with a note": "not pressed", "without": "accepted"}[name]; run.trust != want {
				t.Fatalf("old=%v %s: trust=%q keys=%q", old, name, run.trust, run.rt.keysSent())
			}
		}
	}
}

func TestCodexTrustScreenWithTheHighlightOnQuitMovesFirst(t *testing.T) {
	wt, repo := `C:\Users\me\.secondhand\worktrees\abc123\t1-a1`, `C:\Users\me\repo`
	for _, old := range []bool{false, true} {
		quit, yes := codexTrustScreen(old, 120, wt, repo, true), codexTrustScreen(old, 120, wt, repo, false)
		run := runCodexTrust(t, wt, []string{repo}, func(rt *fakeRuntime) { rt.screen, rt.keyScreens = quit, []string{yes, "› \n"} })
		if run.trust != "accepted" || !slices.Equal(run.keys, []string{"up", "enter"}) || !slices.Equal(run.rt.keysSent(), []string{"up", "enter"}) {
			t.Fatalf("old=%v trust=%q keys=%q", old, run.trust, run.rt.keysSent())
		}
	}
}

func TestCodexTrustScreenThatStaysOnQuitIsNeverConfirmed(t *testing.T) {
	wt, repo := `C:\Users\me\.secondhand\worktrees\abc123\t1-a1`, `C:\Users\me\repo`
	for _, old := range []bool{false, true} {
		quit := codexTrustScreen(old, 120, wt, repo, true)
		run := runCodexTrust(t, wt, []string{repo}, func(rt *fakeRuntime) { rt.screen, rt.afterScreen = quit, quit })
		if run.trust != "not pressed" || run.note != "codex trust screen was not pressed; check the screen" || !slices.Equal(run.rt.keysSent(), []string{"up"}) || run.saved != "" {
			t.Fatalf("old=%v trust=%q note=%q keys=%q saved=%q", old, run.trust, run.note, run.rt.keysSent(), run.saved)
		}
	}
}

func TestCodexTrustScreenForAnotherFolderIsNotMovedEither(t *testing.T) {
	repo := `C:\Users\me\repo`
	quit := codexTrustScreen(false, 120, `C:\Windows`, repo, true)
	run := runCodexTrust(t, `C:\Users\me\.secondhand\worktrees\abc123\t1-a1`, []string{repo}, func(rt *fakeRuntime) { rt.screen = quit })
	if run.trust != "not pressed" || len(run.rt.keysSent()) != 0 {
		t.Fatalf("trust=%q keys=%q", run.trust, run.rt.keysSent())
	}
}

func TestCodexTrustScreenThatDoesNotAppearIsNotAsked(t *testing.T) {
	for name, set := range map[string]func(*fakeRuntime){
		"working": func(rt *fakeRuntime) { rt.screen, rt.status = "› Ask Codex to do anything\n", "working" },
		"idle":    func(rt *fakeRuntime) { rt.screen, rt.status = "› Ask Codex to do anything\n", "idle" },
	} {
		t.Run(name, func(t *testing.T) {
			run := runCodexTrust(t, `C:\Users\me\fleet`, nil, set)
			if run.trust != "not asked" || run.note != "" || len(run.rt.keysSent()) != 0 || run.saved != "" {
				t.Fatalf("trust=%q note=%q keys=%q", run.trust, run.note, run.rt.keysSent())
			}
		})
	}
}

func TestCodexTrustScreenThatComesLateIsPressed(t *testing.T) {
	wt, repo := `C:\Users\me\.secondhand\worktrees\abc123\t1-a1`, `C:\Users\me\repo`
	screen := codexTrustScreen(false, 120, wt, repo, false)
	run := runCodexTrust(t, wt, []string{repo}, func(rt *fakeRuntime) {
		rt.screens, rt.screen, rt.afterScreen, rt.status = []string{"", ""}, screen, "› \n", "idle"
	})
	if run.trust != "accepted" || !slices.Equal(run.rt.keysSent(), []string{"enter"}) {
		t.Fatalf("trust=%q keys=%q", run.trust, run.rt.keysSent())
	}
}

func TestCodexTrustRetriesOneRevisionConflict(t *testing.T) {
	wt, repo := `C:\Users\me\.secondhand\worktrees\abc123\t1-a1`, `C:\Users\me\repo`
	screen := codexTrustScreen(false, 120, wt, repo, false)
	run := runCodexTrust(t, wt, []string{repo}, func(rt *fakeRuntime) {
		rt.screen, rt.afterScreen, rt.keysFailOnce = screen, "› \n", "content_revision_conflict"
	})
	if run.trust != "accepted" || !slices.Equal(run.rt.keysSent(), []string{"enter"}) {
		t.Fatalf("trust=%q keys=%q", run.trust, run.rt.keysSent())
	}
	run = runCodexTrust(t, wt, []string{repo}, func(rt *fakeRuntime) { rt.screen, rt.keysFail = screen, "content_revision_conflict" })
	if run.trust != "not pressed" || run.note != "codex trust screen was not pressed; check the screen" || len(run.rt.keysSent()) != 0 {
		t.Fatalf("trust=%q note=%q keys=%q", run.trust, run.note, run.rt.keysSent())
	}
}

func TestCodexTrustScreenThatStaysIsNotedAsAccepted(t *testing.T) {
	wt, repo := `C:\Users\me\.secondhand\worktrees\abc123\t1-a1`, `C:\Users\me\repo`
	screen := codexTrustScreen(false, 120, wt, repo, false)
	run := runCodexTrust(t, wt, []string{repo}, func(rt *fakeRuntime) { rt.screen, rt.afterScreen = screen, screen })
	if run.trust != "accepted" || run.note != "codex trust screen did not clear; check the screen" || !slices.Equal(run.rt.keysSent(), []string{"enter"}) || !strings.Contains(run.saved, "was sent Enter on") || strings.Contains(run.saved, "accepted") {
		t.Fatalf("trust=%q note=%q keys=%q saved=%q", run.trust, run.note, run.rt.keysSent(), run.saved)
	}
}

func TestCodexWorkerTrustIsAcceptedAndRecorded(t *testing.T) {
	for _, old := range []bool{false, true} {
		fx := claudeFixture(t)
		writeCodexModels(t, fx.h)
		wt := fx.h.worktree("t1-a1")
		screen := codexTrustScreen(old, 120, wt, fx.repo, false)
		fx.rt.set(func(rt *fakeRuntime) { rt.screen, rt.revision, rt.afterScreen = screen, 4, "› \n" })
		out := fx.h.ok("attempt", "start", "--harness", "codex", "--model", "gpt-6-luna", "--effort", "low", "--prompt-file", fx.brief, "t1")
		if !strings.Contains(out, "trust: accepted") || !strings.Contains(out, "trusted: ") || !slices.Equal(fx.rt.keysSent(), []string{"enter"}) {
			t.Fatalf("old=%v start = %q, keys = %q", old, out, fx.rt.keysSent())
		}
		events, err := openStore(t, fx.h).EventsAfter(context.Background(), 0, []string{"attempt.keys", "attempt.trusted", "attempt.blocked"}, 10)
		if err != nil || len(events) != 2 || events[0].Kind != "attempt.keys" || events[0].Detail != "a1: enter" || events[1].Kind != "attempt.trusted" {
			t.Fatalf("old=%v events = %+v, err %v", old, events, err)
		}
		question := map[bool]string{false: "Trust this folder?", true: "Do you trust the contents of this directory?"}[old]
		if want := `a1: codex accepted "` + question + `" for folder ` + wt + "; it saves the trust for " + fx.repo + " in its config.toml"; events[1].Detail != want {
			t.Fatalf("old=%v detail = %q, want %q", old, events[1].Detail, want)
		}
	}
}

func TestCodexWorkerTrustScreenForAnotherRepositoryIsLeftAlone(t *testing.T) {
	fx := claudeFixture(t)
	writeCodexModels(t, fx.h)
	screen := codexTrustScreen(false, 120, fx.h.worktree("t1-a1"), filepath.Join(filepath.Dir(fx.repo), "other"), false)
	fx.rt.set(func(rt *fakeRuntime) { rt.screen = screen })
	out := fx.h.ok("attempt", "start", "--harness", "codex", "--model", "gpt-6-luna", "--effort", "low", "--prompt-file", fx.brief, "t1")
	if !strings.Contains(out, "trust: not pressed") || !strings.Contains(out, "hand attempt read a1") || strings.Contains(out, "trusted:") || len(fx.rt.keysSent()) != 0 {
		t.Fatalf("start = %q, keys = %q", out, fx.rt.keysSent())
	}
	if woke := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(woke, `,attempt.blocked,t1,"a1: codex asks to trust a folder that is not its worktree or project; check the screen"`) {
		t.Fatalf("wait = %q", woke)
	}
	events, err := openStore(t, fx.h).EventsAfter(context.Background(), 0, []string{"attempt.trusted"}, 10)
	if err != nil || len(events) != 0 {
		t.Fatalf("events = %+v, err %v", events, err)
	}
}

func writeCodexModels(t *testing.T, h *harness) {
	t.Helper()
	codexHome := filepath.Join(h.vars["HOME"], ".codex")
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "models_cache.json"), []byte(`{"models":[{"slug":"gpt-6-luna","supported_reasoning_levels":[{"effort":"low"}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func startCodexSupervisor(t *testing.T, h *harness) string {
	t.Helper()
	writeCodexModels(t, h)
	return h.ok("supervisor", "start", "--harness", "codex", "--model", "gpt-6-luna", "--effort", "low")
}

func codexSession(t *testing.T, h *harness) {
	t.Helper()
	now := time.Now()
	dir := filepath.Join(h.vars["HOME"], ".codex", "sessions", now.Format("2006"), now.Format("01"), now.Format("02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd, err := filepath.EvalSymlinks(h.home)
	if err != nil {
		t.Fatal(err)
	}
	meta := `{"type":"session_meta","payload":{"id":"codex-session-1","cwd":"` + strings.ReplaceAll(cwd, `\`, `\\`) + `","timestamp":"` + now.UTC().Format(time.RFC3339Nano) + `"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"You are supervisor s1 of the Hand fleet x."}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "rollout-1.jsonl"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCodexSupervisorAcceptsTrustOfTheFleetHomeAndRecordsIt(t *testing.T) {
	for _, old := range []bool{false, true} {
		h, rt := newSupervisorFixture(t)
		fastTrust(t)
		screen := codexTrustScreen(old, 120, h.home, "", false)
		rt.set(func(rt *fakeRuntime) { rt.screen, rt.afterScreen = screen, "› \n" })
		out := startCodexSupervisor(t, h)
		has(t, "start", out, "harness: codex", "trust: accepted", "trusted: ")
		if !slices.Equal(rt.keysSent(), []string{"enter"}) {
			t.Fatalf("old=%v keys = %q", old, rt.keysSent())
		}
		events, err := openStore(t, h).EventsAfter(context.Background(), 0, []string{"supervisor.keys", "supervisor.trusted", "supervisor.blocked"}, 10)
		if err != nil || len(events) != 2 || events[0].Detail != "s1: enter" || events[1].Kind != "supervisor.trusted" || !strings.Contains(events[1].Detail, "for folder "+h.home+"; it saves the trust for this folder in its config.toml") {
			t.Fatalf("old=%v events = %+v, err %v", old, events, err)
		}
	}
}

func TestCodexSupervisorInsideAnotherRepositoryIsNotTrusted(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	fastTrust(t)
	screen := codexTrustScreen(false, 120, h.home, filepath.Dir(h.home), false)
	rt.set(func(rt *fakeRuntime) { rt.screen = screen })
	has(t, "start", startCodexSupervisor(t, h), "trust: not pressed")
	if got := rt.keysSent(); len(got) != 0 {
		t.Fatalf("keys = %q", got)
	}
}

func TestDeliveryWaitsWhileCodexAsksForTrust(t *testing.T) {
	for _, old := range []bool{false, true} {
		h, rt := newSupervisorFixture(t)
		fastTrust(t)
		rt.set(func(rt *fakeRuntime) { rt.screen = codexTrustScreen(old, 120, "/elsewhere", "", false) })
		has(t, "start", startCodexSupervisor(t, h), "trust: not pressed")
		codexSession(t, h)
		rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
		has(t, "send", h.ok("supervisor", "send", "--text", "hello"), "delivered: no", "codex is asking to trust a folder")
		if got := rt.prompts(); len(got) != 0 {
			t.Fatalf("old=%v typed into the trust screen: %q", old, got)
		}
	}
}

func TestDeliveryIgnoresTheTrustQuestionWhenNoTrustRowsShow(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	fastTrust(t)
	has(t, "start", startCodexSupervisor(t, h), "trust: not asked")
	codexSession(t, h)
	rt.set(func(rt *fakeRuntime) {
		rt.screen, rt.status = "› The Codex screen asks \"Trust this folder?\" before it starts\n", "idle"
	})
	has(t, "send", h.ok("supervisor", "send", "--text", "hello"), "delivered: yes")
	if got := rt.prompts(); !slices.Equal(got, []string{"hello"}) {
		t.Fatalf("prompts = %q", got)
	}
}

func TestACodexResumeScreenIsReportedAsBlockedNotAsATrustFailure(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	fastTrust(t)
	*cli.TrustWait = 300 * time.Millisecond
	rt.set(func(rt *fakeRuntime) {
		rt.screen, rt.status, rt.hint = codexResume, "blocked", "Working directory · Resume"
	})
	has(t, "start", startCodexSupervisor(t, h), "trust: not asked")
	if got := rt.keysSent(); len(got) != 0 {
		t.Fatalf("keys = %q", got)
	}
	codexSession(t, h)
	has(t, "send", h.ok("supervisor", "send", "--text", "hello"), "delivered: no", `why: "supervisor blocked: Working directory · Resume"`)
	if got := rt.prompts(); len(got) != 0 {
		t.Fatalf("typed into the resume screen: %q", got)
	}
}
