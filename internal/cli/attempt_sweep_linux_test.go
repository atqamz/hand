//go:build linux

package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/luvus"
)

const sweepFleet = "f0123456789ab"

func detached(t *testing.T, dir string, argv ...string) int {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	return cmd.Process.Pid
}

func sleeper(t *testing.T, dir string) int {
	t.Helper()
	return detached(t, dir, "sleep", "300")
}

func attemptDirs(t *testing.T, names ...string) []string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "worktrees", sweepFleet)
	var dirs []string
	for _, name := range names {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	return dirs
}

func TestStopSweepsWhatLeftTheProcessGroup(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	wt := fx.h.worktree("t1-a1")
	stray := sleeper(t, wt)
	elsewhere := sleeper(t, t.TempDir())
	if out := fx.h.ok("attempt", "stop", "a1"); !strings.Contains(out, "status: stopped") {
		t.Fatalf("stop = %q", out)
	}
	if !gone(stray) {
		t.Fatalf("setsid child %d of the worktree survived the stop", stray)
	}
	if !alive(elsewhere) {
		t.Fatal("a process with another working folder was signalled")
	}
}

func TestStopSweepsWhenTheRootIsAlreadyGone(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.rt.set(func(rt *fakeRuntime) { rt.marker, rt.runsIn = "1", t.TempDir() })
	fx.start()
	standIn := fx.rt.lastPID()
	stray := sleeper(t, fx.h.worktree("t1-a1"))
	out := fx.h.ok("attempt", "stop", "a1")
	if !strings.Contains(out, "status: exited") || !strings.Contains(out, "root process already gone") {
		t.Fatalf("stop = %q", out)
	}
	if !gone(stray) {
		t.Fatalf("setsid child %d of the worktree survived the stop", stray)
	}
	if !alive(standIn) {
		t.Fatal("the process holding the recorded pid was signalled")
	}
}

func TestSettleSweepsAnAttemptWhoseTerminalIsGone(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	root := fx.rt.lastPID()
	stray := sleeper(t, fx.h.worktree("t1-a1"))
	if err := syscall.Kill(-root, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if !gone(root) {
		t.Fatalf("root %d still alive", root)
	}
	if out := fx.h.ok("attempt", "show", "a1"); !strings.Contains(out, "status: exited") {
		t.Fatalf("show = %q", out)
	}
	if !gone(stray) {
		t.Fatalf("setsid child %d of the worktree survived the settle", stray)
	}
}

func TestCleanSweepsBeforeRemovingTheWorktree(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.h.ok("attempt", "stop", "a1")
	wt := fx.h.worktree("t1-a1")
	stray := sleeper(t, wt)
	elsewhere := sleeper(t, t.TempDir())
	fx.h.ok("attempt", "clean", "a1")
	if !gone(stray) {
		t.Fatalf("setsid child %d of the worktree survived the clean", stray)
	}
	if !alive(elsewhere) {
		t.Fatal("a process with another working folder was signalled")
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree still there: %v", err)
	}
}

func TestSweepEndsAChildThatIgnoresTerm(t *testing.T) {
	wt := attemptDirs(t, "t1-a1")[0]
	pid := detached(t, wt, "sh", "-c", "trap '' TERM; while :; do sleep 1; done")
	cli.Sweep(wt)
	if !gone(pid) {
		t.Fatalf("child %d that ignores SIGTERM survived", pid)
	}
}

func TestSweepEndsAChildForkedDuringTheGrace(t *testing.T) {
	wt := attemptDirs(t, "t1-a1")[0]
	pidFile := filepath.Join(t.TempDir(), "forked")
	detached(t, wt, "sh", "-c", "trap 'sleep 301 & echo $! > "+pidFile+"' TERM; while :; do sleep 1; done")
	time.Sleep(300 * time.Millisecond)
	cli.Sweep(wt)
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the handler never forked: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if !gone(pid) {
		t.Fatalf("child %d forked after the first scan survived", pid)
	}
}

func TestSweepNeverSignalsAStaleStartMarker(t *testing.T) {
	pid := sleeper(t, t.TempDir())
	marker, err := luvus.ProcStartMarker(pid)
	if err != nil {
		t.Fatal(err)
	}
	cli.EndLeftover(pid, marker+"0")
	if !alive(pid) {
		t.Fatal("a pid whose start marker changed was signalled")
	}
	cli.EndLeftover(pid, marker)
	if !gone(pid) {
		t.Fatal("a pid with its recorded start marker survived")
	}
}

func TestSweepStaysInsideOneAttemptFolder(t *testing.T) {
	dirs := attemptDirs(t, "t1-a1", "t1-a1/src", "t1-a10")
	own, ownSub, sibling := dirs[0], dirs[1], dirs[2]
	parent := filepath.Dir(own)
	inOwn, inSub := sleeper(t, own), sleeper(t, ownSub)
	inSibling, inParent := sleeper(t, sibling), sleeper(t, parent)
	cli.Sweep(parent)
	for name, pid := range map[string]int{"folder": inOwn, "subfolder": inSub, "sibling": inSibling, "parent": inParent} {
		if !alive(pid) {
			t.Fatalf("sweeping the parent of the worktrees ended the process in the %s", name)
		}
	}
	cli.Sweep(own)
	if !gone(inOwn) || !gone(inSub) {
		t.Fatal("a process inside the attempt folder survived")
	}
	if !alive(inSibling) || !alive(inParent) {
		t.Fatal("a process outside the attempt folder was signalled")
	}
}

func TestSweepNeverSignalsHandOrItsParents(t *testing.T) {
	wt := attemptDirs(t, "t1-a1")[0]
	t.Chdir(wt)
	cli.Sweep(wt)
	chain := cli.Ancestry(os.Getpid())
	if !chain[os.Getpid()] || !chain[os.Getppid()] {
		t.Fatalf("ancestry = %v, want this process and its parent", chain)
	}
}

func TestSweepRefusesAnythingButOneAttemptFolder(t *testing.T) {
	home, _ := os.UserHomeDir()
	good := filepath.Join("/srv/hand", "worktrees", sweepFleet, "t12-a34")
	for dir, want := range map[string]bool{
		good:                                   true,
		"":                                     false,
		"/":                                    false,
		home:                                   false,
		"/srv/hand":                            false,
		"/srv/hand/worktrees":                  false,
		filepath.Dir(good):                     false,
		good + "/":                             false,
		good + "/src":                          false,
		good + "/../t1-a1":                     false,
		"worktrees/" + sweepFleet + "/t12-a34": false,
		"/srv/hand/worktrees/fleet/t12-a34":    false,
		"/srv/hand/trees/" + sweepFleet + "/t1-a1": false,
		filepath.Join(filepath.Dir(good), "t12"):   false,
		filepath.Join(filepath.Dir(good), "a34"):   false,
	} {
		if got := cli.AttemptDir(dir); got != want {
			t.Errorf("AttemptDir(%q) = %v, want %v", dir, got, want)
		}
	}
}
