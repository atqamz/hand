package cli_test

import (
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/gitcfg"
	"github.com/atqamz/hand/internal/toon"
)

func (fx *attemptFixture) startT1() (string, string, int) {
	fx.h.t.Helper()
	return fx.h.run("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", fx.brief, "t1")
}

func TestAStartIsRefusedForEverySettingBeforeAnythingRuns(t *testing.T) {
	fx := newAttemptFixture(t)
	command, log := markerCommand(t)
	for _, pattern := range gitcfg.Listed {
		key, value := plant(pattern, command)
		restore := plantConfig(t, fx.repo, [2]string{key, value})
		out, errOut, code := fx.startT1()
		if code != 2 || out != "" || !strings.Contains(errOut, strconv.Quote(strings.ToLower(key))) || !strings.Contains(errOut, "Hand ran nothing") || !strings.Contains(errOut, "`hand project trust app`") {
			t.Errorf("%s: code=%d out=%q stderr=%q", key, code, out, errOut)
		}
		restore()
	}
	nothingRan(t, log)
	if _, err := os.Stat(filepath.Dir(fx.h.worktree("t1-a1"))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a worktree folder was made: %v", err)
	}
	if list := fx.h.ok("attempt", "list"); strings.Contains(list, "a1,") {
		t.Fatalf("a refused start left an attempt: %q", list)
	}
	if out, _, code := fx.startT1(); code != 0 || !strings.Contains(out, "status: running") {
		t.Fatalf("after the settings were removed: code=%d out=%q", code, out)
	}
}

func TestTrustingAcceptsTheSettingsAndTheStartRuns(t *testing.T) {
	fx := newAttemptFixture(t)
	command, log := markerCommand(t)
	gitIn(t, fx.repo, "config", "core.sshCommand", command)
	gitIn(t, fx.repo, "config", "--add", "filter.x.smudge", "cat")
	gitIn(t, fx.repo, "config", "--add", "filter.x.smudge", "tr a b")
	if _, errOut, code := fx.startT1(); code != 2 || !strings.Contains(errOut, `"core.sshcommand", "filter.x.smudge"`) || strings.Contains(errOut, command) {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	out := fx.h.ok("project", "trust", "app")
	for _, want := range []string{"project: app\n", "status: accepted\n", "settings[3]{setting,value}:\n", "  core.sshcommand," + toon.Value(command) + "\n", "  filter.x.smudge,cat\n", "  filter.x.smudge,tr a b\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("trust = %q, missing %q", out, want)
		}
	}
	if again := fx.h.ok("project", "trust", "app"); !strings.Contains(again, "status: unchanged\n") || !strings.Contains(again, "settings[3]") {
		t.Fatalf("second trust = %q", again)
	}
	if out, errOut, code := fx.startT1(); code != 0 || !strings.Contains(out, "status: running") {
		t.Fatalf("start after trust: code=%d out=%q stderr=%q", code, out, errOut)
	}
	nothingRan(t, log)
	if recent := fx.h.ok("orient"); !strings.Contains(recent, ",project.trusted,") {
		t.Fatalf("orient = %q", recent)
	}
	gitIn(t, fx.repo, "config", "core.sshCommand", command+" -v")
	if _, errOut, code := fx.h.run("attempt", "clean", "a1"); code != 3 {
		t.Fatalf("clean of a live attempt: code=%d stderr=%q", code, errOut)
	}
	fx.h.ok("attempt", "stop", "a1")
	if _, errOut, code := fx.h.run("attempt", "clean", "a1"); code != 2 || !strings.Contains(errOut, `"core.sshcommand"`) {
		t.Fatalf("clean after a change: code=%d stderr=%q", code, errOut)
	}
}

func TestSettingsPresentBeforeRegistrationAreNeverRefused(t *testing.T) {
	fx := newAttemptFixture(t)
	command, log := markerCommand(t)
	late := gitRepo(t)
	for _, kv := range [][2]string{
		{"core.sshCommand", command},
		{"credential.helper", command},
		{"hook.x.command", command},
		{"filter.lfs.clean", "git-lfs clean -- %f"},
		{"filter.lfs.smudge", "git-lfs smudge -- %f"},
		{"filter.lfs.process", "git-lfs filter-process"},
		{"filter.lfs.required", "true"},
	} {
		gitIn(t, late, "config", "--add", kv[0], kv[1])
	}
	fx.h.ok("project", "add", "late", late)
	fx.h.ok("task", "add", "late", "Later work")
	fx.h.ok("task", "start", "t2")
	out := fx.h.ok("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", fx.brief, "t2")
	for _, want := range []string{"attempt: a1", "task: t2", "status: running", "branch: " + fx.h.branch("t2-a1")} {
		if !strings.Contains(out, want) {
			t.Fatalf("start = %q, missing %q", out, want)
		}
	}
	if _, err := os.Stat(filepath.Join(fx.h.worktree("t2-a1"), ".git")); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}
	if got := fx.h.ok("project", "trust", "late"); !strings.Contains(got, "status: unchanged\n") || !strings.Contains(got, "settings[6]") {
		t.Fatalf("trust = %q", got)
	}
	fx.h.ok("attempt", "stop", "a1")
	fx.h.ok("attempt", "clean", "a1")
	nothingRan(t, log)
}

func TestAProjectWithNothingRecordedAdoptsWhatIsThereOnTheFirstGitRun(t *testing.T) {
	fx := newAttemptFixture(t)
	command, log := markerCommand(t)
	gitIn(t, fx.repo, "config", "core.sshCommand", command)
	db, err := sql.Open("sqlite", "file:"+filepath.Join(fx.h.home, "hand.db")+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE project SET settings = ''`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if doc := fx.h.ok("project", "list"); !strings.Contains(doc, "app,") {
		t.Fatalf("list = %q", doc)
	}
	if out, errOut, code := fx.startT1(); code != 0 || !strings.Contains(out, "status: running") {
		t.Fatalf("first run: code=%d out=%q stderr=%q", code, out, errOut)
	}
	if recent := fx.h.ok("orient"); !strings.Contains(recent, ",project.adopted,") {
		t.Fatalf("orient = %q", recent)
	}
	fx.h.ok("attempt", "stop", "a1")
	gitIn(t, fx.repo, "config", "core.pager", command)
	if _, errOut, code := fx.h.run("attempt", "clean", "a1"); code != 2 || !strings.Contains(errOut, `"core.pager"`) || strings.Contains(errOut, "core.sshcommand") {
		t.Fatalf("after the adoption: code=%d stderr=%q", code, errOut)
	}
	nothingRan(t, log)
}

func TestCleanIsRefusedAndRemovesNothingWhileASettingIsChanged(t *testing.T) {
	fx := newAttemptFixture(t)
	command, log := markerCommand(t)
	fx.start()
	fx.h.ok("attempt", "stop", "a1")
	wt := fx.h.worktree("t1-a1")
	gitIn(t, fx.repo, "config", "filter.x.clean", command)
	if _, errOut, code := fx.h.run("attempt", "clean", "a1"); code != 2 || !strings.Contains(errOut, `"filter.x.clean"`) {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("a refused clean removed the worktree: %v", err)
	}
	gitIn(t, fx.repo, "config", "--unset", "filter.x.clean")
	gitIn(t, fx.repo, "config", "extensions.worktreeConfig", "true")
	gitIn(t, wt, "config", "--worktree", "diff.x.textconv", command)
	if _, errOut, code := fx.h.run("attempt", "clean", "a1"); code != 2 || !strings.Contains(errOut, `"diff.x.textconv (worktree)"`) || !strings.Contains(errOut, "delete the worktree's folder") {
		t.Fatalf("worktree setting: code=%d stderr=%q", code, errOut)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("a refused clean removed the worktree: %v", err)
	}
	if out := fx.h.ok("project", "trust", "app"); !strings.Contains(out, "status: unchanged\n") {
		t.Fatalf("trust cannot accept a worktree's own setting: %q", out)
	}
	gitIn(t, wt, "config", "--worktree", "--unset", "diff.x.textconv")
	if out := fx.h.ok("attempt", "clean", "a1"); !strings.Contains(out, "removed: ") {
		t.Fatalf("clean = %q", out)
	}
	nothingRan(t, log)
}

func TestCleanOfAWorktreeFolderDeletedByHandNeedsNoCheckOfIt(t *testing.T) {
	fx := newAttemptFixture(t)
	command, log := markerCommand(t)
	fx.start()
	fx.h.ok("attempt", "stop", "a1")
	wt := fx.h.worktree("t1-a1")
	gitIn(t, fx.repo, "config", "extensions.worktreeConfig", "true")
	gitIn(t, wt, "config", "--worktree", "filter.x.clean", command)
	if _, errOut, code := fx.h.run("attempt", "clean", "--discard", "a1"); code != 2 || !strings.Contains(errOut, `"filter.x.clean (worktree)"`) {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	if out := fx.h.ok("attempt", "clean", "a1"); !strings.Contains(out, "removed: ") {
		t.Fatalf("clean = %q", out)
	}
	nothingRan(t, log)
}

func TestIncludedSettingsAreCheckedAndTrustedLikeAnyOther(t *testing.T) {
	fx := newAttemptFixture(t)
	command, log := markerCommand(t)
	extra := filepath.Join(t.TempDir(), "extra.cfg")
	if err := os.WriteFile(extra, []byte("[core]\n\teditor = vi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, fx.repo, "config", "include.path", filepath.ToSlash(extra))
	fx.h.ok("project", "trust", "app")
	if out, _, code := fx.startT1(); code != 0 || !strings.Contains(out, "status: running") {
		t.Fatalf("start with a trusted include: code=%d out=%q", code, out)
	}
	fx.h.ok("attempt", "stop", "a1")
	if err := os.WriteFile(extra, []byte("[core]\n\teditor = vi\n[filter \"x\"]\n\tsmudge = "+command+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := fx.h.run("attempt", "clean", "a1"); code != 2 || !strings.Contains(errOut, `"filter.x.smudge"`) {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	nothingRan(t, log)
}

func TestInitLeavesAProjectWhoseSettingsChangedUnrepaired(t *testing.T) {
	fx := newAttemptFixture(t)
	command, log := markerCommand(t)
	fx.start()
	gitIn(t, fx.repo, "config", "filter.x.process", command)
	out := fx.h.ok("init")
	if !strings.Contains(out, "Worktrees of project app were not repaired because its git settings changed") || !strings.Contains(out, "`hand project trust app`") {
		t.Fatalf("init = %q", out)
	}
	fx.h.ok("project", "trust", "app")
	if out := fx.h.ok("init"); strings.Contains(out, "not repaired") {
		t.Fatalf("init after trust = %q", out)
	}
	nothingRan(t, log)
}

func TestAReportFromAWorktreeRunsNoConfiguredProgram(t *testing.T) {
	fx := newAttemptFixture(t)
	command, log := markerCommand(t)
	fx.start()
	var all [][2]string
	for _, pattern := range gitcfg.Listed {
		key, value := plant(pattern, command)
		all = append(all, [2]string{key, value})
	}
	plantConfig(t, fx.repo, all...)
	if err := os.WriteFile(filepath.Join(fx.repo, ".git", "info", "attributes"), []byte("* filter=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.h.cwd = fx.h.worktree("t1-a1")
	if out := fx.h.ok("report", "add", "--status", "progress", "--text", "halfway"); !strings.Contains(out, "attempt: a1") {
		t.Fatalf("report = %q", out)
	}
	nothingRan(t, log)
}
