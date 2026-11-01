package cli_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
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
	if !strings.Contains(out, "scratch_removed[1]{path,bytes}") || !strings.Contains(out, scratch+",10") {
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
	fx.h.ok("attempt", "stop", "a1")
	scratch := fx.scratch(fx.h.worktree("t1-a1"))
	out := fx.h.ok("attempt", "clean", "--keep-scratch", "a1")
	if !strings.Contains(out, "scratch_kept[1]{path,bytes}") || !strings.Contains(out, scratch) || strings.Contains(out, "scratch_removed") {
		t.Fatalf("clean = %q", out)
	}
	if !exists(scratch) {
		t.Fatal("--keep-scratch removed the directory")
	}
}

func TestCleanWithoutScratchPrintsNothingAboutIt(t *testing.T) {
	fx := newScratchFixture(t)
	fx.start()
	fx.h.ok("attempt", "stop", "a1")
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

func TestGCOlderThanFiltersByModificationTime(t *testing.T) {
	fx := newScratchFixture(t)
	oldDir := fx.scratch(fx.h.worktree("t5-a8"))
	newDir := fx.scratch(fx.h.worktree("t5-a9"))
	past := time.Now().Add(-100 * time.Hour)
	if err := os.Chtimes(oldDir, past, past); err != nil {
		t.Fatal(err)
	}
	out := fx.h.ok("attempt", "gc", "--older-than", "72h")
	if !strings.Contains(out, "orphans[1]") || !strings.Contains(out, oldDir) || strings.Contains(out, newDir) {
		t.Fatalf("gc = %q", out)
	}
}
