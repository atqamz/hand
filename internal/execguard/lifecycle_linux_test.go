//go:build linux

package execguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/osfacts"
	"github.com/atqamz/hand/internal/toolchain"
)

func TestGuardActsOnlyOnAnInterruptRequestNamingItsExactIncarnation(t *testing.T) {
	l := newLaunch(t, "/bin/sh", "-c", "exec sleep 300")
	l.write(t)
	g := startGuard(t, l.locator)
	running := waitRecord(t, l.dir, KindRunning, g)

	predecessor := running.Guard
	predecessor.StartTicks--
	if err := RequestInterrupt(l.dir, predecessor, "op_interrupt_stale"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * pollInterval)
	if g.exited() || osfacts.Observe(*running.Root) != osfacts.Alive {
		t.Fatal("a request naming another incarnation of the guard PID terminated the tree (EG-13)")
	}
	if err := RequestInterrupt(l.dir, running.Guard, "op_interrupt"); err != nil {
		t.Fatal(err)
	}
	g.succeed(t)
	ceased := mustRecord(t, l.dir, KindCeased)
	if ceased.Cause != CauseInterruptRequest || ceased.InterruptOperationID != "op_interrupt" || ceased.Predicate != cessationRule || *ceased.Root != *running.Root {
		t.Fatalf("ceased = %+v, want the exact interrupt request after wait4 ECHILD (EG-9)", ceased)
	}
	if osfacts.Observe(*running.Root) != osfacts.Absent {
		t.Fatal("the harness root outlived its ceased record (EG-8)")
	}
}

func TestEscapedGrandchildDelaysCessationUntilReaped(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	l := newLaunch(t, "/bin/sh", "-c", roleEnv+`=escape "$0" "$1" && exec sleep 300`, testExecutable(t), pidFile)
	l.write(t)
	g := startGuard(t, l.locator)
	running := waitRecord(t, l.dir, KindRunning, g)
	waitFor(t, "grandchild pid", func() bool { return exists(pidFile) })
	pid := readPID(t, pidFile)
	grandchild, err := osfacts.ReadIncarnation(pid)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "grandchild reparented to the guard", func() bool {
		stat, err := readStat(pid)
		return err == nil && stat.ppid == running.Guard.PID
	})

	if err := RequestInterrupt(l.dir, running.Guard, "op_interrupt"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(testDeadline)
	for !g.exited() {
		recorded := exists(filepath.Join(l.dir, KindCeased))
		if recorded && osfacts.Observe(grandchild) == osfacts.Alive {
			t.Fatal("ceased was recorded while the escaped grandchild was alive (EG-8)")
		}
		if time.Now().After(deadline) {
			t.Fatal("guard did not finish after the interrupt request")
		}
		time.Sleep(time.Millisecond)
	}
	g.succeed(t)
	mustRecord(t, l.dir, KindCeased)
	if got := osfacts.Observe(grandchild); got != osfacts.Absent {
		t.Fatalf("Observe(grandchild) after ceased = %s, want absent (EG-8)", got)
	}
}

func TestForkLoopIsFrozenAndReapedByTheSweep(t *testing.T) {
	l := newLaunch(t, "/bin/sh", "-c", `trap '' TERM; while :; do sleep 30 & done`)
	l.write(t)
	g := startGuard(t, l.locator)
	running := waitRecord(t, l.dir, KindRunning, g)
	if err := RequestInterrupt(l.dir, running.Guard, "op_interrupt"); err != nil {
		t.Fatal(err)
	}
	g.succeed(t)
	if ceased := mustRecord(t, l.dir, KindCeased); ceased.Predicate != cessationRule {
		t.Fatalf("ceased = %+v, want the wait4 ECHILD predicate (EG-8)", ceased)
	}
}

func TestDescendantPIDNamespaceEndsWithTheTree(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "namespaced.pid")
	l := newLaunch(t, "/bin/sh", "-c", roleEnv+`=pidns exec "$0" "$1"`, testExecutable(t), pidFile)
	l.write(t)
	g := startGuard(t, l.locator)
	running := waitRecord(t, l.dir, KindRunning, g)
	waitFor(t, "namespaced child", func() bool { return exists(pidFile) })
	if strings.TrimSpace(read(t, pidFile)) == "unsupported" {
		t.Skip("unprivileged user and PID namespaces are unavailable")
	}
	namespaced, err := osfacts.ReadIncarnation(readPID(t, pidFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := RequestInterrupt(l.dir, running.Guard, "op_interrupt"); err != nil {
		t.Fatal(err)
	}
	g.succeed(t)
	mustRecord(t, l.dir, KindCeased)
	if got := osfacts.Observe(namespaced); got != osfacts.Absent {
		t.Fatalf("Observe(namespace init) after ceased = %s, want absent (EG-8)", got)
	}
}

func TestSIGKILLedGuardLeavesNoCessationRecord(t *testing.T) {
	l := newLaunch(t, "/bin/sh", "-c", "exec sleep 300")
	l.write(t)
	g := startGuard(t, l.locator)
	running := waitRecord(t, l.dir, KindRunning, g)
	_ = g.cmd.Process.Kill()
	g.wait(t)
	if exists(filepath.Join(l.dir, KindCeased)) {
		t.Fatal("a guard killed with SIGKILL wrote ceased (EG-8)")
	}
	if osfacts.Observe(running.Guard) != osfacts.Absent || osfacts.Observe(*running.Root) != osfacts.Alive {
		t.Fatal("want the guard gone and its harness still alive: a missing ceased record is unknown, never ceased (EG-8)")
	}
}

func TestImmediateHarnessExitRecordsRunningAndCeased(t *testing.T) {
	for _, test := range []struct {
		script string
		code   int
		kind   string
	}{
		{"exit 0", 0, "completed"},
		{"exit 3", 3, "failed"},
	} {
		l := newLaunch(t, "/bin/sh", "-c", test.script)
		l.write(t)
		g := startGuard(t, l.locator)
		if code := g.succeed(t); code != test.code {
			t.Fatalf("%s: guard exit %d, want the harness status", test.script, code)
		}
		mustRecord(t, l.dir, KindRunning)
		ceased := mustRecord(t, l.dir, KindCeased)
		if ceased.Cause != CauseHarnessExit || ceased.Exit == nil || ceased.Exit.Code != test.code || TerminalKind(ceased, "") != test.kind {
			t.Fatalf("%s: ceased = %+v, want harness-exit %d labelled %s (EG-5, EG-8)", test.script, ceased, test.code, test.kind)
		}
	}
}

func TestTerminalHangupRecordsCeasedWithCauseHangup(t *testing.T) {
	master, tty := openPTY(t)
	l := newLaunch(t, "/bin/sh", "-c", "exec sleep 300")
	l.write(t)
	g := newGuard(t, l.locator)
	onTerminal(g.cmd, tty)
	g.start(t)
	waitRecord(t, l.dir, KindRunning, g)
	if err := master.Close(); err != nil {
		t.Fatal(err)
	}
	g.succeed(t)
	if ceased := mustRecord(t, l.dir, KindCeased); ceased.Cause != CauseHangup || TerminalKind(ceased, "") != "provider-gone" {
		t.Fatalf("ceased = %+v, want cause hangup (EG-8)", ceased)
	}
}

func TestHarnessProcessGroupOwnsTheTerminalForeground(t *testing.T) {
	_, tty := openPTY(t)
	l := newLaunch(t, "/bin/sh", "-c", `set -- $(cat /proc/$$/stat); [ "$5" = "$8" ]`)
	l.write(t)
	g := newGuard(t, l.locator)
	onTerminal(g.cmd, tty)
	g.start(t)
	if code := g.succeed(t); code != 0 {
		t.Fatalf("exit %d: the harness group P was not the terminal's foreground group", code)
	}
}

func TestGuardReturnsTheForegroundAndFlushesInputFromItsBackgroundGroup(t *testing.T) {
	master, tty := openPTY(t)
	l := newLaunch(t, "/bin/sh", "-c", "sleep 1")
	l.write(t)
	shell := exec.Command(testExecutable(t), l.locator)
	shell.Env = append(os.Environ(), roleEnv+"=pane-shell", "GORACE=atexit_sleep_ms=0")
	onTerminal(shell, tty)
	if err := shell.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = shell.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = shell.Process.Kill()
		<-done
		reapOrphans(t)
	})
	waitFor(t, "running record", func() bool { return exists(filepath.Join(l.dir, KindRunning)) })
	if _, err := master.WriteString("| pending doorbell\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(testDeadline):
		t.Fatal("pane shell did not finish: the guard's tcsetpgrp or tcflush never completed")
	}
	if code := shell.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("pane shell exit %d: 3 means the foreground was not returned, 4 that input was not flushed", code)
	}
}

func TestIgnoredDispositionsDoNotLeakIntoTheHarness(t *testing.T) {
	grep, err := exec.LookPath("grep")
	if err != nil {
		t.Skip("no grep on PATH")
	}
	l := newLaunch(t, grep, "SigIgn", "/proc/self/status")
	l.write(t)
	g := newGuard(t, l.locator)
	g.cmd.Args = []string{"/bin/sh", "-c", `trap '' HUP INT QUIT PIPE TSTP TTIN TTOU; exec "$0" "$1"`, g.cmd.Path, l.locator}
	g.cmd.Path = "/bin/sh"
	g.start(t)
	g.succeed(t)
	mask, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(read(t, g.output), "SigIgn:")), 16, 64)
	if err != nil {
		t.Fatal(err)
	}
	for _, sig := range []int{1, 2, 3, 13, 20, 21, 22} {
		if mask&(1<<(sig-1)) != 0 {
			t.Errorf("signal %d the guard inherited as ignored is still ignored in the harness", sig)
		}
	}
}

func TestGuardHoldsItsHandGenerationLeaseWhileRunning(t *testing.T) {
	runtimeStore, err := toolchain.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	managed, err := runtimeStore.MaterializeHandExecutable(testExecutable(t))
	if err != nil {
		t.Fatal(err)
	}
	l := newLaunch(t, "/bin/sh", "-c", "exec sleep 300")
	l.write(t)
	g := newGuard(t, l.locator)
	g.cmd.Path, g.cmd.Args[0] = managed, managed
	g.start(t)
	running := waitRecord(t, l.dir, KindRunning, g)
	lease := toolchain.LeaseRequest{
		Generation: "sha256:" + filepath.Base(filepath.Dir(managed)),
		LeaseID:    "exec-guard:" + l.handoff.LaunchOperationID,
		FleetID:    l.handoff.FleetID,
		Consumer:   "exec-guard",
		Evidence:   "launch=" + l.handoff.LaunchOperationID,
	}
	if held, err := runtimeStore.HandLeaseHeld(lease); err != nil || !held {
		t.Fatalf("lease held = %t, %v while the guard runs, want held (EG-17)", held, err)
	}
	if err := RequestInterrupt(l.dir, running.Guard, "op_interrupt"); err != nil {
		t.Fatal(err)
	}
	g.succeed(t)
	if held, err := runtimeStore.HandLeaseHeld(lease); err != nil || held {
		t.Fatalf("lease held = %t, %v after the guard exited, want released", held, err)
	}
}
