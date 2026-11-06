package cli_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/toon"
)

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

type scratchFixture struct {
	*attemptFixture
	tmp string
}

func newScratchFixture(t *testing.T) *scratchFixture {
	t.Helper()
	if os.Getuid() < 0 {
		t.Skip("the scratch cleaner is Unix-only")
	}
	fx := &scratchFixture{attemptFixture: newAttemptFixture(t), tmp: t.TempDir()}
	fx.h.vars["TMPDIR"] = fx.tmp
	fx.h.vars["CLAUDE_CODE_TMPDIR"] = ""
	return fx
}

func (fx *scratchFixture) scratch(worktree string) string {
	fx.h.t.Helper()
	dir := filepath.Join(fx.tmp, "claude-"+strconv.Itoa(os.Getuid()), nonAlnum.ReplaceAllString(worktree, "-"))
	session := filepath.Join(dir, "session", "scratchpad")
	if err := os.MkdirAll(session, 0o755); err != nil {
		fx.h.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(session, "capture.log"), []byte("0123456789"), 0o644); err != nil {
		fx.h.t.Fatal(err)
	}
	return dir
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, fs.ErrNotExist)
}

func TestCleanRemovesTheClaudeScratchDirectoryAndKeepsTranscripts(t *testing.T) {
	fx := newScratchFixture(t)
	fx.start()
	eventually(t, func() bool { return exists(scratchOf(fx.h, fx.h.worktree("t1-a1"))) })
	fx.h.ok("attempt", "stop", "a1")
	wt := fx.h.worktree("t1-a1")
	scratch := fx.scratch(wt)
	sibling := fx.scratch(strings.TrimSuffix(wt, "a1") + "a2")
	transcript := filepath.Join(fx.h.vars["HOME"], ".claude", "projects", nonAlnum.ReplaceAllString(wt, "-"), "s.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := fx.h.ok("attempt", "clean", "a1")
	if !strings.Contains(out, "scratch_removed[2]{path,bytes}") || !strings.Contains(out, scratch+",10") || !strings.Contains(out, scratchOf(fx.h, wt)) {
		t.Fatalf("clean = %q", out)
	}
	if exists(scratch) {
		t.Fatal("scratch directory kept")
	}
	if !exists(sibling) || !exists(transcript) {
		t.Fatal("clean removed more than the attempt's scratch directory")
	}
}

func TestCleanKeepScratchLeavesItAndNamesIt(t *testing.T) {
	fx := newScratchFixture(t)
	fx.start()
	eventually(t, func() bool { return exists(scratchOf(fx.h, fx.h.worktree("t1-a1"))) })
	fx.h.ok("attempt", "stop", "a1")
	scratch := fx.scratch(fx.h.worktree("t1-a1"))
	out := fx.h.ok("attempt", "clean", "--keep-scratch", "a1")
	if !strings.Contains(out, "scratch_kept[2]{path,bytes}") || !strings.Contains(out, scratch) || strings.Contains(out, "scratch_removed") {
		t.Fatalf("clean = %q", out)
	}
	if !exists(scratch) || !exists(scratchOf(fx.h, fx.h.worktree("t1-a1"))) {
		t.Fatal("--keep-scratch removed a directory")
	}
}

func TestCleanWithoutScratchPrintsNothingAboutIt(t *testing.T) {
	fx := newScratchFixture(t)
	fx.start()
	eventually(t, func() bool { return exists(scratchOf(fx.h, fx.h.worktree("t1-a1"))) })
	fx.h.ok("attempt", "stop", "a1")
	if err := os.RemoveAll(scratchOf(fx.h, fx.h.worktree("t1-a1"))); err != nil {
		t.Fatal(err)
	}
	if out := fx.h.ok("attempt", "clean", "a1"); strings.Contains(out, "scratch") {
		t.Fatalf("clean = %q", out)
	}
}

func TestGCListsOrphansByDefaultAndRemovesThemWithApply(t *testing.T) {
	fx := newScratchFixture(t)
	fx.start()
	live := fx.scratch(fx.h.worktree("t1-a1"))
	orphan := fx.scratch(fx.h.worktree("t5-a9"))
	foreign := fx.scratch(filepath.Join(fx.tmp, "elsewhere", "worktrees", "f0123456789ab", "t5-a9"))
	out := fx.h.ok("attempt", "gc")
	if !strings.Contains(out, "applied: false") || !strings.Contains(out, "orphans[1]{path,bytes}") || !strings.Contains(out, orphan+",10") || strings.Contains(out, live) || strings.Contains(out, foreign) {
		t.Fatalf("gc = %q", out)
	}
	if !exists(orphan) {
		t.Fatal("a dry run removed the directory")
	}
	out = fx.h.ok("attempt", "gc", "--apply")
	if !strings.Contains(out, "applied: true") || !strings.Contains(out, orphan) {
		t.Fatalf("gc --apply = %q", out)
	}
	if exists(orphan) || !exists(live) || !exists(foreign) {
		t.Fatalf("after --apply: orphan=%v live=%v foreign=%v", exists(orphan), exists(live), exists(foreign))
	}
	if out := fx.h.ok("attempt", "gc"); !strings.Contains(out, "orphans[0]") {
		t.Fatalf("second gc = %q", out)
	}
}

func ageTree(t *testing.T, dir string, age time.Duration) {
	t.Helper()
	past := time.Now().Add(-age)
	var paths []string
	_ = filepath.WalkDir(dir, func(p string, _ fs.DirEntry, _ error) error {
		paths = append(paths, p)
		return nil
	})
	for i := len(paths) - 1; i >= 0; i-- {
		if err := os.Chtimes(paths[i], past, past); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGCOlderThanUsesTheNewestTimeInTheTree(t *testing.T) {
	fx := newScratchFixture(t)
	oldDir := fx.scratch(fx.h.worktree("t5-a8"))
	newDir := fx.scratch(fx.h.worktree("t5-a9"))
	deepDir := fx.scratch(fx.h.worktree("t5-a7"))
	ageTree(t, oldDir, 100*time.Hour)
	ageTree(t, newDir, 100*time.Hour)
	ageTree(t, deepDir, 100*time.Hour)
	fresh := time.Now()
	if err := os.Chtimes(filepath.Join(newDir, "session", "scratchpad", "capture.log"), fresh, fresh); err != nil {
		t.Fatal(err)
	}
	out := fx.h.ok("attempt", "gc", "--older-than", "72h")
	if !strings.Contains(out, "orphans[2]") || !strings.Contains(out, oldDir) || !strings.Contains(out, deepDir) || strings.Contains(out, newDir) {
		t.Fatalf("gc = %q", out)
	}
}

func TestGCOlderThanUsesTheAttemptScratchRoot(t *testing.T) {
	fx := newAttemptFixture(t)
	home, err := filepath.EvalSymlinks(fx.h.home)
	if err != nil {
		t.Fatal(err)
	}
	oldDir := filepath.Join(home, "scratch", "t5-a8")
	newDir := filepath.Join(home, "scratch", "t5-a9")
	for _, dir := range []string{oldDir, newDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "f"), []byte("0123456789"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ageTree(t, oldDir, 100*time.Hour)
	out := fx.h.ok("attempt", "gc", "--older-than", "72h")
	if !strings.Contains(out, toon.Value(oldDir)) || !strings.Contains(out, ",10") || strings.Contains(out, toon.Value(newDir)) {
		t.Fatalf("gc = %q", out)
	}
	out = fx.h.ok("attempt", "gc", "--older-than", "72h", "--apply")
	if !strings.Contains(out, toon.Value(oldDir)) || strings.Contains(out, toon.Value(newDir)) {
		t.Fatalf("gc --apply = %q", out)
	}
	if exists(oldDir) || !exists(newDir) {
		t.Fatalf("after --apply: old=%v fresh=%v", exists(oldDir), exists(newDir))
	}
}

func TestGCSkipsAnAttemptThatIsLiveInTheDatabase(t *testing.T) {
	fx := newScratchFixture(t)
	fx.start()
	wt := fx.h.worktree("t1-a1")
	scratch := fx.scratch(wt)
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	if out := fx.h.ok("attempt", "gc", "--apply"); strings.Contains(out, scratch) {
		t.Fatalf("gc = %q", out)
	}
	if !exists(scratch) {
		t.Fatal("gc removed a live attempt's scratch directory")
	}
	fx.h.ok("attempt", "stop", "a1")
	if out := fx.h.ok("attempt", "gc"); !strings.Contains(out, scratch) {
		t.Fatalf("gc after stop = %q", out)
	}
}

func undeletable(t *testing.T, scratch string) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a Unix user that cannot delete a read-only subtree")
	}
	sub := filepath.Join(scratch, "locked")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })
}

func TestGCApplyRemovesWhatItCanAndWarnsOnTheRest(t *testing.T) {
	fx := newScratchFixture(t)
	stuck := fx.scratch(fx.h.worktree("t5-a1"))
	undeletable(t, stuck)
	freed := fx.scratch(fx.h.worktree("t5-a9"))
	out := fx.h.ok("attempt", "gc", "--apply")
	if exists(freed) || !exists(stuck) {
		t.Fatalf("after --apply: freed=%v stuck=%v", exists(freed), exists(stuck))
	}
	if !strings.Contains(out, "orphans[1]") || !strings.Contains(out, freed+",10") || !strings.Contains(out, `warn: "scratch not removed: `) || !strings.Contains(out, "locked") {
		t.Fatalf("gc --apply = %q", out)
	}
}

func TestCleanWarnsWhenTheScratchDirectoryStaysAndStillCleans(t *testing.T) {
	fx := newScratchFixture(t)
	fx.start()
	eventually(t, func() bool { return exists(scratchOf(fx.h, fx.h.worktree("t1-a1"))) })
	fx.h.ok("attempt", "stop", "a1")
	wt := fx.h.worktree("t1-a1")
	stuck := fx.scratch(wt)
	undeletable(t, stuck)
	out := fx.h.ok("attempt", "clean", "a1")
	if !strings.Contains(out, "removed: "+wt) || !strings.Contains(out, `warn: "scratch not removed: `) || !strings.Contains(out, "scratch_removed[1]{path,bytes}") || !strings.Contains(out, scratchOf(fx.h, wt)) || !exists(stuck) {
		t.Fatalf("clean = %q", out)
	}
}
