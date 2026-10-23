package gitcfg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/state"
)

func TestMain(m *testing.M) {
	fakebin.Main(map[string]func([]string) int{"mark": func(args []string) int {
		fakebin.Append(fakebin.Params()["log"], strings.Join(args, " "))
		return 0
	}})
	os.Exit(m.Run())
}

func gitc(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %q: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitc(t, dir, "init", "-q", "-b", "main")
	gitc(t, dir, "commit", "-q", "--allow-empty", "-m", "root")
	return dir
}

func newMark(t *testing.T) (command, log string) {
	t.Helper()
	log = filepath.Join(t.TempDir(), "ran.log")
	return filepath.ToSlash(fakebin.Install(t, t.TempDir(), "mark", "mark", map[string]string{"log": log})), log
}

func ran(t *testing.T, log string) string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return string(b)
}

func recorded(t *testing.T, dir string) Repo {
	t.Helper()
	ctx := context.Background()
	now, err := Read(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, "app", dir, now.Digest())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func plantKeys(t *testing.T, dir, command string) (restore func()) {
	t.Helper()
	file := filepath.Join(dir, ".git", "config")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, p := range Listed {
		key := concrete(p)
		parts := strings.Split(key, ".")
		if sub := strings.Join(parts[1:len(parts)-1], "."); sub != "" {
			fmt.Fprintf(&b, "[%s %q]\n", parts[0], sub)
		} else {
			fmt.Fprintf(&b, "[%s]\n", parts[0])
		}
		fmt.Fprintf(&b, "\t%s = %q\n", parts[len(parts)-1], valueFor(key, command))
	}
	if err := os.WriteFile(file, append(slices.Clone(before), b.String()...), 0o644); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.WriteFile(file, before, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func concrete(pattern string) string {
	return strings.Replace(pattern, "*", "x", 1)
}

func valueFor(key, command string) string {
	if strings.HasPrefix(key, "include") {
		return "missing.cfg"
	}
	return command
}

func TestListedMatchesEveryPatternAndNothingElse(t *testing.T) {
	for _, p := range Listed {
		if key := concrete(p); !listed(key) || !listed(strings.ToLower(key)) {
			t.Errorf("%s does not match %s", key, p)
		}
	}
	for _, name := range []string{"core.filemode", "core.bare", "remote.origin.url", "remote.origin.fetch", "branch.main.remote", "user.name", "alias.co", "diff.algorithm", "diff.command", "filter.lfs.required", "core.hooksPath.x", "hook.jobs", "extensions.worktreeconfig", "credential.https://h.username"} {
		if listed(name) {
			t.Errorf("%s is listed", name)
		}
	}
	for _, name := range []string{"filter.lfs.clean", "credential.https://github.com.helper", "includeif.gitdir:/x/.path", "lfs.customtransfer.agent.path", "hook.jobs.command", "pager.log"} {
		if !listed(name) {
			t.Errorf("%s is not listed", name)
		}
	}
}

func TestEverySettingIsReadAndNamedAndRunsNothing(t *testing.T) {
	dir := newRepo(t)
	command, log := newMark(t)
	r := recorded(t, dir)
	ctx := context.Background()
	if err := r.Check(ctx); err != nil {
		t.Fatalf("unchanged repository: %v", err)
	}
	restore := plantKeys(t, dir, command)
	want := make([]string, len(Listed))
	for i, p := range Listed {
		want[i] = strings.ToLower(concrete(p))
	}
	slices.Sort(want)
	now, err := Read(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range now {
		got = append(got, s.Name)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("read %q, want %q", got, want)
	}
	if names := now.Changed(r.Recorded, false); !slices.Equal(names, want) {
		t.Fatalf("changed %q, want %q", names, want)
	}
	err = r.Check(ctx)
	if !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), strconv.Quote(want[0])) || !strings.Contains(err.Error(), "and "+strconv.Itoa(len(want)-shown)+" more") || !strings.Contains(err.Error(), "Hand ran nothing") {
		t.Fatalf("Check = %v", err)
	}
	if _, err := r.Git(ctx, "rev-parse", "HEAD"); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("Git = %v", err)
	}
	restore()
	if err := r.Check(ctx); err != nil {
		t.Fatalf("after the settings were removed: %v", err)
	}
	if got := ran(t, log); got != "" {
		t.Fatalf("a configured program ran: %q", got)
	}
}

func TestEveryGitCommandHandRunsIsRefusedWhenASettingChanged(t *testing.T) {
	dir := newRepo(t)
	command, log := newMark(t)
	r := recorded(t, dir)
	ctx := context.Background()
	gitc(t, dir, "config", "hook.x.command", command)
	gitc(t, dir, "config", "hook.x.event", "reference-transaction")
	gitc(t, dir, "config", "filter.x.smudge", command)
	for _, args := range [][]string{
		{"rev-parse", "--verify", "--quiet", "refs/heads/main"},
		{"remote", "get-url", "origin"},
		{"fetch", "origin"},
		{"symbolic-ref", "--short", "refs/remotes/origin/HEAD"},
		{"log", "-1", "--format=%h"},
		{"worktree", "add", "--no-track", "-b", "b", filepath.Join(t.TempDir(), "wt"), "HEAD"},
		{"worktree", "add", filepath.Join(t.TempDir(), "wt2"), "main"},
		{"worktree", "remove", "--force", filepath.Join(t.TempDir(), "wt")},
		{"worktree", "prune"},
		{"worktree", "repair"},
		{"branch", "-D", "b"},
		{"status", "--porcelain", "--untracked-files=all"},
		{"rev-list", "--count", "HEAD", "--not", "--branches", "--tags", "--remotes"},
	} {
		if _, err := r.Git(ctx, args...); !errors.Is(err, state.ErrInvalid) {
			t.Errorf("git %q: %v", args, err)
		}
		if _, err := r.In(dir).Git(ctx, args...); !errors.Is(err, state.ErrInvalid) {
			t.Errorf("git %q in a worktree: %v", args, err)
		}
	}
	if got := ran(t, log); got != "" {
		t.Fatalf("a configured program ran: %q", got)
	}
}

func TestTheSameSettingsDoRunWithoutTheCheck(t *testing.T) {
	if os.Getenv("GIT_SSH_COMMAND") != "" || os.Getenv("GIT_SSH") != "" {
		t.Skip("the environment overrides core.sshCommand")
	}
	ctx := context.Background()
	for name, set := range map[string]func(t *testing.T, dir, origin, command string) func() []string{
		"core.sshCommand": func(t *testing.T, dir, _, command string) func() []string {
			gitc(t, dir, "remote", "add", "origin", "ssh://example.invalid/repo.git")
			gitc(t, dir, "config", "core.sshCommand", command)
			return func() []string { return []string{"fetch", "origin"} }
		},
		"remote.origin.uploadpack": func(t *testing.T, dir, origin, command string) func() []string {
			gitc(t, dir, "remote", "add", "origin", origin)
			gitc(t, dir, "config", "remote.origin.uploadpack", command)
			return func() []string { return []string{"fetch", "origin"} }
		},
		"hook.x.command": func(t *testing.T, dir, _, command string) func() []string {
			gitc(t, dir, "config", "hook.x.command", command)
			gitc(t, dir, "config", "hook.x.event", "reference-transaction")
			return worktreeAdd(t)
		},
		"filter.x.smudge": func(t *testing.T, dir, _, command string) func() []string {
			if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			gitc(t, dir, "add", "f.txt")
			gitc(t, dir, "commit", "-q", "-m", "file")
			if err := os.WriteFile(filepath.Join(dir, ".git", "info", "attributes"), []byte("* filter=x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			gitc(t, dir, "config", "filter.x.smudge", command)
			return worktreeAdd(t)
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir, origin := newRepo(t), newRepo(t)
			command, log := newMark(t)
			r := recorded(t, dir)
			args := set(t, dir, origin, command)
			if _, err := r.Git(ctx, args()...); !errors.Is(err, state.ErrInvalid) {
				t.Fatalf("Git = %v", err)
			}
			if got := ran(t, log); got != "" {
				t.Fatalf("the check let %q run", got)
			}
			_, _ = run(ctx, dir, args()...)
			if ran(t, log) == "" {
				t.Skipf("git does not run %s here", name)
			}
			if err := os.Remove(log); err != nil {
				t.Fatal(err)
			}
			r = recorded(t, dir)
			_, _ = r.Git(ctx, args()...)
			if ran(t, log) == "" {
				t.Fatal("trusting the setting did not lift the refusal")
			}
		})
	}
}

func worktreeAdd(t *testing.T) func() []string {
	n := 0
	return func() []string {
		n++
		return []string{"worktree", "add", "--no-track", "-b", "b" + strconv.Itoa(n), filepath.Join(t.TempDir(), "wt"), "HEAD"}
	}
}

func TestSettingsPresentBeforeRecordingNeverRefuse(t *testing.T) {
	dir := newRepo(t)
	command, _ := newMark(t)
	gitc(t, dir, "config", "core.sshCommand", command)
	gitc(t, dir, "config", "filter.lfs.clean", "git-lfs clean -- %f")
	gitc(t, dir, "config", "filter.lfs.smudge", "git-lfs smudge -- %f")
	gitc(t, dir, "config", "filter.lfs.process", "git-lfs filter-process")
	gitc(t, dir, "config", "filter.lfs.required", "true")
	r := recorded(t, dir)
	wt := filepath.Join(t.TempDir(), "wt")
	if _, err := r.Git(context.Background(), "worktree", "add", "--no-track", "-b", "b", wt, "HEAD"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatal(err)
	}
	if out, err := r.Git(context.Background(), "log", "-1", "--format=%s"); err != nil || strings.TrimSpace(out) != "root" {
		t.Fatalf("log = %q, %v", out, err)
	}
}

func TestAnIncludedFileIsRead(t *testing.T) {
	dir := newRepo(t)
	command, log := newMark(t)
	extra := filepath.Join(t.TempDir(), "extra.cfg")
	if err := os.WriteFile(extra, []byte("[core]\n\teditor = vi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, dir, "config", "include.path", filepath.ToSlash(extra))
	gitc(t, dir, "config", "includeIf.gitdir:"+filepath.ToSlash(dir)+"/.path", filepath.ToSlash(extra))
	r := recorded(t, dir)
	now, err := Read(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	var editor, include int
	for _, s := range now {
		switch s.Name {
		case "core.editor":
			editor++
		case "include.path":
			include++
		}
	}
	if editor != 1 || include != 1 {
		t.Fatalf("settings = %+v", now)
	}
	if err := r.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extra, []byte("[core]\n\teditor = vi\n[filter \"x\"]\n\tclean = "+command+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Check(context.Background()); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), `"filter.x.clean"`) {
		t.Fatalf("Check = %v", err)
	}
	if got := ran(t, log); got != "" {
		t.Fatalf("ran %q", got)
	}
}

func TestAWorktreesOwnConfigurationIsCheckedWhereGitReadsIt(t *testing.T) {
	dir := newRepo(t)
	command, log := newMark(t)
	gitc(t, dir, "config", "extensions.worktreeConfig", "true")
	wt := filepath.Join(t.TempDir(), "wt")
	gitc(t, dir, "worktree", "add", "-q", "-b", "b", wt)
	ctx := context.Background()
	r := recorded(t, dir)
	if err := r.In(wt).Check(ctx); err != nil {
		t.Fatal(err)
	}
	gitc(t, wt, "config", "--worktree", "filter.x.clean", command)
	if err := r.Check(ctx); err != nil {
		t.Fatalf("the main worktree does not read another worktree's file: %v", err)
	}
	err := r.In(wt).Check(ctx)
	if !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), `"filter.x.clean (worktree)"`) || !strings.Contains(err.Error(), "delete the worktree's folder") {
		t.Fatalf("Check in the worktree = %v", err)
	}
	if _, err := r.In(wt).Git(ctx, "status", "--porcelain"); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("status = %v", err)
	}
	gitc(t, wt, "config", "--worktree", "--unset", "filter.x.clean")
	if err := r.In(wt).Check(ctx); err != nil {
		t.Fatal(err)
	}
	gitc(t, dir, "config", "--worktree", "core.editor", "vi")
	if err := r.Check(ctx); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), `"core.editor (worktree)"`) {
		t.Fatalf("main worktree configuration: %v", err)
	}
	r = recorded(t, dir)
	if err := r.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.In(wt).Check(ctx); err != nil {
		t.Fatalf("a recorded setting of the main worktree is not asked of another: %v", err)
	}
	if got := ran(t, log); got != "" {
		t.Fatalf("ran %q", got)
	}
}

func TestAttributesAreRecordedAndAnEmptyFileIsNone(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	file := filepath.Join(dir, ".git", "info", "attributes")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r := recorded(t, dir)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := r.Check(ctx); err != nil {
		t.Fatalf("an absent file and an empty one differ: %v", err)
	}
	if err := os.WriteFile(file, []byte("* filter=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Check(ctx); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), `".git/info/attributes"`) {
		t.Fatalf("Check = %v", err)
	}
	r = recorded(t, dir)
	if err := os.WriteFile(file, []byte("* filter=y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Check(ctx); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("a changed file: %v", err)
	}
}

func TestChangedNamesAreSortedAndOnlyNamesAreShown(t *testing.T) {
	dir := newRepo(t)
	command, _ := newMark(t)
	r := recorded(t, dir)
	for _, key := range []string{"filter.b.smudge", "core.sshCommand", "diff.a.command"} {
		gitc(t, dir, "config", key, command+" SECRETVALUE")
	}
	err := r.Check(context.Background())
	if err == nil || strings.Contains(err.Error(), "SECRETVALUE") || !strings.Contains(err.Error(), `"core.sshcommand", "diff.a.command", "filter.b.smudge"`) {
		t.Fatalf("Check = %v", err)
	}
}

func TestANameWithControlCharactersIsQuoted(t *testing.T) {
	dir := newRepo(t)
	r := recorded(t, dir)
	f, err := os.OpenFile(filepath.Join(dir, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("[filter \"a\x1b[31mb\"]\n\tsmudge = x\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	err = r.Check(context.Background())
	if !errors.Is(err, state.ErrInvalid) || strings.ContainsRune(err.Error(), 0x1b) || !strings.Contains(err.Error(), `\x1b`) {
		t.Fatalf("Check = %q", err)
	}
}

func TestMoreThanTenChangesAreCounted(t *testing.T) {
	dir := newRepo(t)
	r := recorded(t, dir)
	for i := range 12 {
		gitc(t, dir, "config", "filter."+strconv.Itoa(i)+".clean", "x")
	}
	err := r.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "and 2 more") {
		t.Fatalf("Check = %v", err)
	}
}

func TestChangedComparesNamesAndHashes(t *testing.T) {
	a := Settings{{Name: "core.editor", Values: []string{"vi"}}, {Name: "x.y (worktree)", Values: []string{"1"}}}
	rec := a.Digest()
	if got := a.Changed(rec, false); len(got) != 0 {
		t.Fatalf("same = %v", got)
	}
	b := Settings{{Name: "core.editor", Values: []string{"vim"}}, {Name: "core.pager", Values: []string{"less"}}}
	if got := strings.Join(b.Changed(rec, false), ","); got != "core.editor,core.pager,x.y (worktree)" {
		t.Fatalf("changed = %q", got)
	}
	if got := strings.Join(Settings{{Name: "core.editor", Values: []string{"vi"}}}.Changed(rec, true), ","); got != "" {
		t.Fatalf("a recorded worktree setting is not asked of a linked worktree: %q", got)
	}
	split := Settings{{Name: "core.editor", Values: []string{"a", "b"}}}
	joined := Settings{{Name: "core.editor", Values: []string{"ab"}}}
	if got := joined.Changed(split.Digest(), false); len(got) != 1 {
		t.Fatalf("value boundaries are not hashed: %v", got)
	}
	if got := a.Changed("not a digest", false); len(got) != 2 {
		t.Fatalf("an unreadable record = %v", got)
	}
	if got := (Settings{}).Digest(); got != "v1\n" {
		t.Fatalf("empty digest = %q", got)
	}
}

func TestARefusalIsAChangedErrorThatIsInvalid(t *testing.T) {
	dir := newRepo(t)
	r := recorded(t, dir)
	gitc(t, dir, "config", "core.editor", "vi")
	err := r.Check(context.Background())
	var changed ChangedError
	if !errors.As(err, &changed) || !errors.Is(err, state.ErrInvalid) || !strings.HasPrefix(err.Error(), "invalid: git settings of project app changed") {
		t.Fatalf("Check = %v", err)
	}
	if err := (Repo{Project: "app", Dir: dir}).Check(context.Background()); errors.As(err, &changed) {
		t.Fatalf("an unrecorded project is not a change: %v", err)
	}
}

func TestInitialisedSeesAWorkingTreeAndABareRepository(t *testing.T) {
	work, bare := newRepo(t), filepath.Join(t.TempDir(), "bare.git")
	gitc(t, work, "clone", "-q", "--bare", work, bare)
	for dir, want := range map[string]bool{work: true, bare: true, t.TempDir(): false, filepath.Join(t.TempDir(), "gone"): false} {
		if got := Initialised(dir); got != want {
			t.Errorf("Initialised(%s) = %v", dir, got)
		}
	}
}

func TestACheckWithNothingRecordedOrNoRepositoryFails(t *testing.T) {
	ctx := context.Background()
	if err := (Repo{Project: "app", Dir: newRepo(t)}).Check(ctx); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("nothing recorded: %v", err)
	}
	if _, err := Read(ctx, t.TempDir()); err == nil {
		t.Fatal("a folder that is no repository was read")
	}
	if _, err := Read(ctx, filepath.Join(t.TempDir(), "gone")); err == nil {
		t.Fatal("a missing folder was read")
	}
}

func TestToplevelAndReadRunNothingWithEverySettingPlanted(t *testing.T) {
	dir := newRepo(t)
	command, log := newMark(t)
	plantKeys(t, dir, command)
	if err := os.WriteFile(filepath.Join(dir, ".git", "info", "attributes"), []byte("* filter=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	top, err := Toplevel(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks(filepath.FromSlash(strings.TrimSpace(top)))
	if got != want {
		t.Fatalf("top = %q, want %q", got, want)
	}
	now, err := Read(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(now) != len(Listed)+1 {
		t.Fatalf("read %d settings, want %d", len(now), len(Listed)+1)
	}
	if got := ran(t, log); got != "" {
		t.Fatalf("ran %q", got)
	}
}
