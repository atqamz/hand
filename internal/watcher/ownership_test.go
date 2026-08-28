package watcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAcquireRefusesASecondWatcherAndNamesTheRecordedHolder(t *testing.T) {
	home := t.TempDir()

	ownership, err := Acquire(home, false)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer ownership.Release()

	// Two flocks from one process genuinely conflict on Linux: each os.OpenFile is
	// its own open file description, which is what an flock is held against.
	_, err = Acquire(home, false)
	if err == nil {
		t.Fatal("second Acquire succeeded, want a refusal while a watcher is attached")
	}
	if !strings.Contains(err.Error(), ErrAttached.Error()) {
		t.Fatalf("got %v, want it to wrap ErrAttached", err)
	}
	// The refusal names the real, durably-recorded holder rather than asserting
	// "a watcher" - atqamz/hand#410. Naming the pid is not the same instruction
	// the takeover ADR forbids: nothing here tells the operator to signal it.
	wantPID := fmt.Sprintf("pid %d", os.Getpid())
	if !strings.Contains(err.Error(), wantPID) {
		t.Fatalf("got %v, want it to name the recorded holder %s", err, wantPID)
	}
	if !strings.Contains(err.Error(), ownership.Generation()) {
		t.Fatalf("got %v, want it to name the recorded generation %s", err, ownership.Generation())
	}
	if !strings.Contains(err.Error(), "owning session") {
		t.Fatalf("got %v, want the remedy to name the owning session", err)
	}
	if !strings.Contains(err.Error(), "--takeover") {
		t.Fatalf("got %v, want the refusal to name the remedy", err)
	}
}

// The core of atqamz/hand#410: a busy lock held by an in-process supervision
// bridge cycle (never an interactive `hand watch`) must be named as such, and
// must not be offered a takeover it can never honor.
func TestContendNamesABridgeHolderAndOffersNoTakeover(t *testing.T) {
	home := t.TempDir()
	holder, err := AcquireBridgeContext(context.Background(), home)
	if err != nil {
		t.Fatalf("AcquireBridgeContext: %v", err)
	}
	defer holder.Release()

	_, err = Acquire(home, false)
	if err == nil {
		t.Fatal("Acquire succeeded, want a refusal while a bridge cycle holds the lock")
	}
	if !strings.Contains(err.Error(), ErrAttached.Error()) {
		t.Fatalf("got %v, want it to wrap ErrAttached", err)
	}
	wantPID := fmt.Sprintf("pid %d", os.Getpid())
	if !strings.Contains(err.Error(), wantPID) {
		t.Fatalf("got %v, want it to name the recorded holder %s", err, wantPID)
	}
	if !strings.Contains(err.Error(), holder.Generation()) {
		t.Fatalf("got %v, want it to name the recorded generation %s", err, holder.Generation())
	}
	if strings.Contains(err.Error(), "owning session") {
		t.Fatalf("got %v, want no owning-session instruction: a bridge holder is not stopped that way", err)
	}
	if strings.Contains(err.Error(), "for cooperative replacement") {
		t.Fatalf("got %v, want --takeover never offered as a remedy against a holder that never honors one", err)
	}
}

// A --takeover contender against a bridge holder must fail honestly and fast:
// waiting out the full grace period against a holder that never observes a
// takeover request would just be a slower version of the same wrong promise.
func TestTakeoverAgainstABridgeHolderFailsFastWithoutWaitingOutTheGrace(t *testing.T) {
	home := t.TempDir()
	holder, err := AcquireBridgeContext(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()

	restoreTakeoverClocks(t)
	takeoverGrace, takeoverPoll = 3*time.Second, 20*time.Millisecond

	start := time.Now()
	_, err = Acquire(home, true)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrAttached) {
		t.Fatalf("got %v, want ErrAttached", err)
	}
	if elapsed >= takeoverGrace {
		t.Fatalf("took %s to fail, want it to fail well before the %s grace since this holder can never honor takeover", elapsed, takeoverGrace)
	}
	if strings.Contains(err.Error(), "did not release within") {
		t.Fatalf("got %v, want an immediate honest refusal, not a timeout message", err)
	}
	if strings.Contains(err.Error(), "owning session") {
		t.Fatalf("got %v, want no owning-session instruction against a bridge holder", err)
	}
}

// An absent owner record on a busy lock is its own answer: the refusal must
// not guess a holder or promise a treatment it cannot back with evidence.
func TestContendWithNoRecordSaysSoRatherThanGuessingAHolder(t *testing.T) {
	home := t.TempDir()
	holder, err := Acquire(home, false)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	if err := os.Remove(OwnerRecordPath(home)); err != nil {
		t.Fatal(err)
	}

	_, err = Acquire(home, false)
	if err == nil {
		t.Fatal("Acquire succeeded, want a refusal while the lock is held")
	}
	assertNoGuessedHolder(t, err)
}

// Same expectation for a record that exists but does not parse.
func TestContendWithAMalformedRecordSaysSoRatherThanGuessingAHolder(t *testing.T) {
	home := t.TempDir()
	holder, err := Acquire(home, false)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	if err := os.WriteFile(OwnerRecordPath(home), []byte(`{truncated`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = Acquire(home, false)
	if err == nil {
		t.Fatal("Acquire succeeded, want a refusal while the lock is held")
	}
	assertNoGuessedHolder(t, err)
}

func assertNoGuessedHolder(t *testing.T, err error) {
	t.Helper()
	if !strings.Contains(err.Error(), ErrAttached.Error()) {
		t.Fatalf("got %v, want it to wrap ErrAttached", err)
	}
	if strings.Contains(err.Error(), "pid ") {
		t.Fatalf("got %v, want no guessed pid when the record cannot be read", err)
	}
	if strings.Contains(err.Error(), "owning session") || strings.Contains(err.Error(), "--takeover") {
		t.Fatalf("got %v, want no treatment promised for an unidentified holder", err)
	}
	if !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("got %v, want it to say plainly that the record could not be read", err)
	}
}

func TestAcquireIgnoresAStaleFileWhoseRecordedPidIsDead(t *testing.T) {
	home := t.TempDir()
	writeOwnerFile(t, home, strconv.Itoa(deadPid(t))+"\n")

	ownership, err := Acquire(home, false)
	if err != nil {
		t.Fatalf("Acquire over a crashed watcher's leftover pid file: %v, want it to succeed - a lock that refuses forever after a crash locks this home out of watching itself", err)
	}
	defer ownership.Release()

	if got := readOwnerFile(t, home); got != strconv.Itoa(os.Getpid())+"\n" {
		t.Fatalf("got %q, want the leftover pid replaced with this process's own", got)
	}
}

// Half of the same guarantee: the stale case must not need --takeover, and must
// also not break under it.
func TestAcquireTakesOverAStaleFileWhoseRecordedPidIsDead(t *testing.T) {
	home := t.TempDir()
	writeOwnerFile(t, home, strconv.Itoa(deadPid(t))+"\n")

	ownership, err := Acquire(home, true)
	if err != nil {
		t.Fatalf("Acquire --takeover over a crashed watcher's leftover pid file: %v", err)
	}
	ownership.Release()
}

func TestAcquireIgnoresAStaleFileHoldingGarbage(t *testing.T) {
	home := t.TempDir()
	writeOwnerFile(t, home, "not-a-pid")

	ownership, err := Acquire(home, false)
	if err != nil {
		t.Fatalf("Acquire over an unparseable pid file: %v", err)
	}
	ownership.Release()
}

func TestReleaseClearsTheRecordedPid(t *testing.T) {
	home := t.TempDir()

	ownership, err := Acquire(home, false)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	ownership.Release()

	if got := readOwnerFile(t, home); got != "" {
		t.Fatalf("got %q in %s after release, want it empty - a pid left behind names a process that has exited", got, OwnerPath(home))
	}

	ownership, err = Acquire(home, false)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	ownership.Release()
}

// Returns a pid that was real and is now gone, so a test about a crashed watcher is about an
// actually dead process rather than an invented number that might collide with a live one.
func deadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run a throwaway process to consume a pid: %v", err)
	}
	return cmd.Process.Pid
}

func writeOwnerFile(t *testing.T, home, content string) {
	t.Helper()
	path := OwnerPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readOwnerFile(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(OwnerPath(home))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A stale advisory pid that happens to be a live innocent process must never be
// attacked: when the kernel lock is free, Acquire ignores it entirely.
func TestAcquireReplacesStaleLivePIDAndLeavesTheInnocentProcessAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not support the signal-zero liveness probe")
	}
	home := t.TempDir()
	innocent := exec.Command("sleep", "3600")
	if err := innocent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = innocent.Process.Kill()
		_ = innocent.Wait()
	}()
	writeOwnerFile(t, home, strconv.Itoa(innocent.Process.Pid)+"\n")

	ownership, err := Acquire(home, false)
	if err != nil {
		t.Fatalf("Acquire over a stale live pid: %v", err)
	}
	ownership.Release()

	// A zero signal is a liveness probe, never a delivered signal; it proves the
	// innocent process was not terminated by any takeover handling.
	if err := innocent.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("innocent process no longer exists after stale-pid handling: %v", err)
	}
	if got := readOwnerFile(t, home); got != "" {
		t.Fatalf("owner file = %q after release, want it clear - not the innocent process's pid", got)
	}
}

// Simulates a holder that has not (or no longer) published a coherent routing
// record: a --takeover contender must perform no destructive action and simply
// wait for the authoritative lock.
func TestContenderWithNoRecordWaitsForTheLock(t *testing.T) {
	home := t.TempDir()
	holder, err := Acquire(home, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(OwnerRecordPath(home)); err != nil {
		t.Fatal(err)
	}

	restoreTakeoverClocks(t)
	takeoverGrace, takeoverPoll = 3*time.Second, 20*time.Millisecond

	type outcome struct {
		ownership *Ownership
		err       error
	}
	done := make(chan outcome, 1)
	go func() { o, e := Acquire(home, true); done <- outcome{o, e} }()

	// Held, no record: contender waits, does nothing destructive, is not yet owner.
	select {
	case r := <-done:
		t.Fatalf("contender returned early while the lock was held: %v / %v", r.ownership, r.err)
	case <-time.After(100 * time.Millisecond):
	}
	holder.Release()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("contender Acquire after release: %v", r.err)
		}
		r.ownership.Release()
	case <-time.After(3 * time.Second):
		t.Fatal("contender did not acquire once the holder released the lock")
	}
}

func TestContenderCannotActBetweenLockAcquisitionAndOwnerPublication(t *testing.T) {
	home := t.TempDir()
	incumbent, err := Acquire(home, false)
	if err != nil {
		t.Fatal(err)
	}
	generation := incumbent.Generation()
	incumbent.endpoint.Close()
	releaseLock(incumbent.lockFile)
	if err := publishOwnerRecord(home, OwnerRecord{Version: ownerRecordVersion, Generation: generation, PID: 424242}); err != nil {
		t.Fatal(err)
	}

	publicationReached := make(chan struct{})
	publicationContinue := make(chan struct{})
	oldAfterLockAcquired := afterLockAcquired
	afterLockAcquired = func() {
		close(publicationReached)
		<-publicationContinue
	}
	t.Cleanup(func() {
		afterLockAcquired = oldAfterLockAcquired
		select {
		case <-publicationContinue:
		default:
			close(publicationContinue)
		}
	})

	type outcome struct {
		ownership *Ownership
		err       error
	}
	successorDone := make(chan outcome, 1)
	go func() {
		ownership, acquireErr := Acquire(home, false)
		successorDone <- outcome{ownership: ownership, err: acquireErr}
	}()
	select {
	case <-publicationReached:
	case <-time.After(time.Second):
		t.Fatal("successor did not acquire the kernel lock before publication")
	}

	if _, err := os.Stat(OwnerRecordPath(home)); err != nil {
		t.Fatalf("stale owner record disappeared before the successor reached publication: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	contender, err := AcquireContext(ctx, home, true)
	cancel()
	if contender != nil {
		contender.Release()
		t.Fatal("takeover contender became owner while the successor held the kernel lock")
	}
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("takeover contender = %v, want interruption while the lock remains held", err)
	}

	close(publicationContinue)
	select {
	case result := <-successorDone:
		if result.err != nil {
			t.Fatalf("successor Acquire: %v", result.err)
		}
		result.ownership.Release()
	case <-time.After(time.Second):
		t.Fatal("successor did not finish owner publication")
	}
}

// Same expectation with a malformed record: no destructive action, Safe keeps
// waiting, and the lock alone decides ownership.
func TestContenderWithMalformedRecordWaitsForTheLock(t *testing.T) {
	home := t.TempDir()
	holder, err := Acquire(home, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(OwnerRecordPath(home), []byte(`{truncated`), 0o644); err != nil {
		t.Fatal(err)
	}

	restoreTakeoverClocks(t)
	takeoverGrace, takeoverPoll = 3*time.Second, 20*time.Millisecond

	type outcome struct {
		ownership *Ownership
		err       error
	}
	done := make(chan outcome, 1)
	go func() { o, e := Acquire(home, true); done <- outcome{o, e} }()
	select {
	case r := <-done:
		t.Fatalf("contender returned early while the lock was held: %v", r.err)
	case <-time.After(100 * time.Millisecond):
	}
	holder.Release()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("contender Acquire after release: %v", r.err)
		}
		r.ownership.Release()
	case <-time.After(3 * time.Second):
		t.Fatal("contender did not acquire once the holder released the lock")
	}
}

// If safe takeover cannot complete before the grace bound because the lock
// never releases, the contender fails and the incumbent stays the sole owner -
// there are never two owners.
func TestContenderFailsWithinGraceWithoutCreatingASecondOwner(t *testing.T) {
	home := t.TempDir()
	holder, err := Acquire(home, false)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()

	restoreTakeoverClocks(t)
	takeoverGrace, takeoverPoll = 200*time.Millisecond, 20*time.Millisecond

	_, err = Acquire(home, true)
	if !errors.Is(err, ErrAttached) {
		t.Fatalf("takeover contender = %v, want ErrAttached when the lock never releases", err)
	}
	// The holder still owns the single lock.
	if _, err := Acquire(home, true); !errors.Is(err, ErrAttached) {
		t.Fatalf("after a failed contender the holder was displaced: %v", err)
	}
}

func restoreTakeoverClocks(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		takeoverGrace = 5 * time.Second
		takeoverPoll = 50 * time.Millisecond
	})
}
