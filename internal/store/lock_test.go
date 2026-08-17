package store

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/filelock"
)

const (
	lockHelperEnv = "HAND_STORE_LOCK_TEST_HELPER"
	lockHomeEnv   = "HAND_STORE_LOCK_TEST_HOME"
	lockKeyEnv    = "HAND_STORE_LOCK_TEST_KEY"
)

func lockPathname(home, key string) string {
	return filepath.Join(Dir(home), fmt.Sprintf(".%x.lock", sha256.Sum256([]byte(key))))
}

func TestLockPathnameIsTheHashOfTheLogicalKey(t *testing.T) {
	home := t.TempDir()
	release, err := Lock(home, "task:x", false)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	defer release()

	if _, err := os.Stat(lockPathname(home, "task:x")); err != nil {
		t.Fatalf("pathname must be the hash of the logical key: %v", err)
	}
}

func TestLockPathnamePersistsAfterRelease(t *testing.T) {
	home := t.TempDir()
	release, err := Lock(home, "task:x", false)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	release()

	info, err := os.Stat(lockPathname(home, "task:x"))
	if err != nil {
		t.Fatalf("lock pathname must survive release, not be a leak: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("got size %d, want 0", info.Size())
	}
}

func TestReleasedLockIsReacquirableInAFreshProcess(t *testing.T) {
	if os.Getenv(lockHelperEnv) == "acquire-release" {
		runLockAcquireReleaseHelper(t)
		return
	}

	home := t.TempDir()
	const key = "task:reacquire"
	child := lockHelperCommand(t, "TestReleasedLockIsReacquirableInAFreshProcess", "acquire-release", home, key)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("acquire-release helper: %v\n%s", err, out)
	}

	release, err := Lock(home, key, true)
	if err != nil {
		t.Fatalf("reacquire after a fresh process released: %v", err)
	}
	release()
}

func runLockAcquireReleaseHelper(t *testing.T) {
	release, err := Lock(os.Getenv(lockHomeEnv), os.Getenv(lockKeyEnv), false)
	if err != nil {
		t.Fatalf("helper Lock: %v", err)
	}
	release()
}

func TestCrashReleasesKernelOwnership(t *testing.T) {
	if os.Getenv(lockHelperEnv) == "hold-forever" {
		runLockHoldForeverHelper(t)
		return
	}

	home := t.TempDir()
	const key = "task:crash"
	child := lockHelperCommand(t, "TestCrashReleasesKernelOwnership", "hold-forever", home, key)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatalf("start hold-forever helper: %v", err)
	}
	t.Cleanup(func() { killAndWait(child) })

	if !waitForLockLine(t, stdout, "acquired") {
		t.Fatal("helper never reported acquiring the lock")
	}
	if _, err := Lock(home, key, true); err != filelock.ErrBusy {
		t.Fatalf("got %v, want ErrBusy while the helper still holds the lock", err)
	}

	if err := child.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	_ = child.Wait()

	release, err := Lock(home, key, true)
	if err != nil {
		t.Fatalf("reacquire after the holder was killed: %v", err)
	}
	release()
}

func runLockHoldForeverHelper(t *testing.T) {
	_, err := Lock(os.Getenv(lockHomeEnv), os.Getenv(lockKeyEnv), false)
	if err != nil {
		t.Fatalf("helper Lock: %v", err)
	}
	fmt.Println("acquired")
	select {}
}

func TestBlockingLockWaitsThenSucceeds(t *testing.T) {
	if os.Getenv(lockHelperEnv) == "hold-then-release" {
		runLockHoldThenReleaseHelper(t)
		return
	}

	home := t.TempDir()
	const key = "task:block"
	child := lockHelperCommand(t, "TestBlockingLockWaitsThenSucceeds", "hold-then-release", home, key)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	t.Cleanup(func() { killAndWait(child) })

	if !waitForLockLine(t, stdout, "acquired") {
		t.Fatal("helper never reported acquiring the lock")
	}

	done := make(chan struct {
		release func()
		err     error
	}, 1)
	entered := make(chan struct{})
	go func() {
		close(entered)
		release, err := Lock(home, key, false)
		done <- struct {
			release func()
			err     error
		}{release, err}
	}()

	<-entered
	select {
	case result := <-done:
		t.Fatalf("blocking Lock returned (err=%v) before the holder released", result.err)
	default:
	}

	if _, err := io.WriteString(stdin, "release\n"); err != nil {
		t.Fatalf("signal the holder to release: %v", err)
	}

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("blocking Lock after release: %v", result.err)
		}
		result.release()
	case <-time.After(10 * time.Second):
		t.Fatal("blocking Lock never returned after the holder released")
	}

	if err := child.Wait(); err != nil {
		t.Fatalf("holder: %v", err)
	}
}

func runLockHoldThenReleaseHelper(t *testing.T) {
	release, err := Lock(os.Getenv(lockHomeEnv), os.Getenv(lockKeyEnv), false)
	if err != nil {
		t.Fatalf("helper Lock: %v", err)
	}
	fmt.Println("acquired")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	release()
}

func TestDistinctKeysDoNotCollideAndDistinctHomesDoNotShare(t *testing.T) {
	home := t.TempDir()
	releaseA, err := Lock(home, "task:a", true)
	if err != nil {
		t.Fatalf("lock task:a: %v", err)
	}
	defer releaseA()

	releaseB, err := Lock(home, "task:b", true)
	if err != nil {
		t.Fatalf("distinct key in the same home must not collide: %v", err)
	}
	defer releaseB()

	otherHome := t.TempDir()
	releaseSame, err := Lock(otherHome, "task:a", true)
	if err != nil {
		t.Fatalf("the same key in a distinct home must not share a lock: %v", err)
	}
	defer releaseSame()
}

func TestRealisticContentionHasExactlyOneWinner(t *testing.T) {
	if os.Getenv(lockHelperEnv) == "contend" {
		runLockContendHelper(t)
		return
	}

	const n = 5
	home := t.TempDir()
	const key = "task:contend"

	type contender struct {
		cmd    *exec.Cmd
		stdin  io.WriteCloser
		stdout *bufio.Scanner
	}
	contenders := make([]contender, n)
	for i := range contenders {
		child := lockHelperCommand(t, "TestRealisticContentionHasExactlyOneWinner", "contend", home, key)
		stdout, err := child.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdin, err := child.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			t.Fatalf("start contender %d: %v", i, err)
		}
		t.Cleanup(func() { killAndWait(child) })
		contenders[i] = contender{cmd: child, stdin: stdin, stdout: bufio.NewScanner(stdout)}
	}

	for i, c := range contenders {
		if !c.stdout.Scan() || c.stdout.Text() != "ready" {
			t.Fatalf("contender %d never reported ready", i)
		}
	}
	for i, c := range contenders {
		if _, err := io.WriteString(c.stdin, "go\n"); err != nil {
			t.Fatalf("release contender %d to attempt the lock: %v", i, err)
		}
	}

	winner := -1
	wins := 0
	for i, c := range contenders {
		if !c.stdout.Scan() {
			t.Fatalf("contender %d never reported a result", i)
		}
		switch c.stdout.Text() {
		case "won":
			wins++
			winner = i
		case "busy":
		default:
			t.Fatalf("contender %d reported %q", i, c.stdout.Text())
		}
	}
	if wins != 1 {
		t.Fatalf("got %d winners among %d contenders, want exactly 1", wins, n)
	}

	// A "busy" contender has already exited after printing its result; only the
	// winner is still waiting on a second stdin line.
	if _, err := io.WriteString(contenders[winner].stdin, "done\n"); err != nil {
		t.Fatalf("release winning contender to exit: %v", err)
	}
	for i, c := range contenders {
		if err := c.cmd.Wait(); err != nil {
			t.Fatalf("contender %d: %v", i, err)
		}
	}
}

func runLockContendHelper(t *testing.T) {
	fmt.Println("ready")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')

	release, err := Lock(os.Getenv(lockHomeEnv), os.Getenv(lockKeyEnv), true)
	if err == filelock.ErrBusy {
		fmt.Println("busy")
		return
	}
	if err != nil {
		t.Fatalf("helper Lock: %v", err)
	}
	fmt.Println("won")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	release()
}

func TestEveryLogicalKeyShapeInTheTreeIsCovered(t *testing.T) {
	home := t.TempDir()
	keys := []string{
		"task:example",
		"project:example",
		"worktree:/example/path",
		"send:example",
		"config:routing",
		"completions",
		MigrationLock,
		SchemaLock,
	}

	seen := make(map[string]string, len(keys))
	for _, key := range keys {
		release, err := Lock(home, key, true)
		if err != nil {
			t.Fatalf("lock key shape %q: %v", key, err)
		}
		defer release()

		path := lockPathname(home, key)
		if other, ok := seen[path]; ok {
			t.Fatalf("key shapes %q and %q hash to the same pathname %s", other, key, path)
		}
		seen[path] = key
	}
}

func lockHelperCommand(t *testing.T, testName, helper, home, key string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$")
	cmd.Env = append(os.Environ(), lockHelperEnv+"="+helper, lockHomeEnv+"="+home, lockKeyEnv+"="+key)
	return cmd
}

func waitForLockLine(t *testing.T, r io.Reader, want string) bool {
	t.Helper()
	lines := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(r)
		if scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	select {
	case line := <-lines:
		return line == want
	case <-time.After(10 * time.Second):
		return false
	}
}

func killAndWait(cmd *exec.Cmd) {
	if cmd.ProcessState != nil {
		return
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}
