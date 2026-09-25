//go:build linux

package execguard

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/atomicfile"
	handlaunch "github.com/atqamz/hand/internal/launch"
	"github.com/atqamz/hand/internal/osfacts"
	"github.com/atqamz/hand/internal/testtag"
	"golang.org/x/sys/unix"
)

const (
	roleEnv      = "EXECGUARD_TEST_ROLE"
	testGrace    = 300 * time.Millisecond
	testDeadline = 10 * time.Second
	guardFailed  = 125
)

// The test binary doubles as the guard, the pane shell and the escaping descendants.
// The suite is a subreaper so a descendant a failed test orphans is reaped before return.
func TestMain(m *testing.M) {
	role := os.Getenv(roleEnv)
	_ = os.Unsetenv(roleEnv)
	switch role {
	case "guard":
		code, err := run(os.Args[1], testGrace)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(guardFailed)
		}
		os.Exit(code)
	case "escape":
		os.Exit(escape(os.Args[1]))
	case "grandchild":
		signal.Ignore(syscall.SIGTERM)
		if err := atomicfile.Write(os.Args[1], ".pid-*", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(1)
		}
		time.Sleep(time.Hour)
		os.Exit(0)
	case "pidns":
		os.Exit(namespacedSleep(os.Args[1]))
	case "pane-shell":
		os.Exit(paneShell(os.Args[1]))
	case "exit-zero":
		os.Exit(0)
	case "blocked-guard":
		os.Exit(execBlocked(os.Args))
	}
	if !testtag.Present {
		testtag.Refuse()
	}
	root, err := os.MkdirTemp("", "hand-execguard-test-")
	if err == nil {
		err = os.Setenv("SECONDHAND_HOME", filepath.Join(root, "secondhand"))
	}
	if err == nil {
		err = unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

func escape(pidFile string) int {
	self, err := os.Executable()
	if err == nil {
		_, err = syscall.ForkExec(self, []string{self, pidFile}, &syscall.ProcAttr{
			Env:   append(os.Environ(), roleEnv+"=grandchild"),
			Files: []uintptr{0, 1, 2},
			Sys:   &syscall.SysProcAttr{Setsid: true},
		})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func execBlocked(args []string) int {
	runtime.LockOSThread()
	var blocked unix.Sigset_t
	blocked.Val[0] = 1 << (unix.SIGUSR1 - 1)
	if err := unix.PthreadSigmask(unix.SIG_BLOCK, &blocked, nil); err != nil {
		return 1
	}
	self, err := os.Executable()
	if err == nil {
		err = syscall.Exec(self, args, append(os.Environ(), roleEnv+"=guard"))
	}
	fmt.Fprintln(os.Stderr, err)
	return 1
}

func namespacedSleep(pidFile string) int {
	child := exec.Command("sleep", "300")
	child.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:  syscall.CLONE_NEWUSER | syscall.CLONE_NEWPID,
		UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
		GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
	}
	content := "unsupported"
	if err := child.Start(); err == nil {
		content = strconv.Itoa(child.Process.Pid)
	}
	if err := atomicfile.Write(pidFile, ".pid-*", []byte(content), 0o600); err != nil || content == "unsupported" {
		return 0
	}
	time.Sleep(time.Hour)
	return 0
}

// Stands in for the interactive pane shell: runs the guard as a job in its own
// foreground group, then reports whether it got the terminal back with no input left.
func paneShell(locator string) int {
	self, err := os.Executable()
	if err != nil {
		return 1
	}
	guard := exec.Command(self, locator)
	guard.Env = append(os.Environ(), roleEnv+"=guard")
	guard.Stdin, guard.Stdout, guard.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := guard.Run(); err != nil {
		return 2
	}
	group, err := unix.IoctlGetUint32(0, unix.TIOCGPGRP)
	if err != nil || int(group) != unix.Getpgrp() {
		return 3
	}
	if pending, err := unix.IoctlGetInt(0, unix.TIOCINQ); err != nil || pending != 0 {
		return 4
	}
	return 0
}

type launch struct {
	dir      string
	locator  string
	worktree string
	handoff  Handoff
}

func newLaunch(t *testing.T, executable string, arguments ...string) *launch {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "exec-guard", "op_launch_"+randomHex(t, 8))
	worktree := filepath.Join(root, "worktree")
	for _, path := range []string{dir, worktree} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	self, err := osfacts.ReadIncarnation(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	namespace, err := osfacts.SelfPIDNamespace()
	if err != nil {
		t.Fatal(err)
	}
	uid, err := osfacts.RealUID(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	fleet, binding := "f_"+randomHex(t, 16), "eb_"+randomHex(t, 8)
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	credential := base64.RawURLEncoding.EncodeToString(secret)
	values := map[string]string{
		ExecutorBindingEnv: binding,
		CredentialEnv:      credential,
		"HAND_ROLE":        "worker",
		"TOKEN":            "resolved-token-" + randomHex(t, 4),
	}
	l := &launch{dir: dir, locator: filepath.Join(dir, handoffName), worktree: worktree, handoff: Handoff{
		LaunchOperationID: filepath.Base(dir),
		FleetID:           fleet,
		ExecutorBindingID: binding,
		RequestDigest:     "request-" + randomHex(t, 8),
		Spec: handlaunch.CanonicalSpec{
			Executable: executable,
			Arguments:  arguments,
			Cwd:        worktree,
			Environment: map[string]handlaunch.CanonicalEnvironmentValue{
				ExecutorBindingEnv: literal(binding),
				CredentialEnv: {
					ValueKind:     "secret-ref",
					ValueMaterial: Protocol,
					ValueDigest:   handlaunch.ExecGuardCredentialVerifier(fleet, binding, credential),
				},
				"HAND_ROLE": literal("worker"),
				"TOKEN": {
					ValueKind:     "secret-ref",
					ValueMaterial: "secret://worker/token",
					ValueDigest:   handlaunch.EnvironmentValueDigest(values["TOKEN"]),
				},
			},
		},
		Values:       values,
		WorktreePath: worktree,
		BootID:       self.BootID,
		PIDNamespace: namespace,
		UID:          uid,
	}}
	l.seal()
	return l
}

func literal(value string) handlaunch.CanonicalEnvironmentValue {
	return handlaunch.CanonicalEnvironmentValue{ValueKind: "literal", ValueMaterial: value, ValueDigest: handlaunch.EnvironmentValueDigest(value)}
}

func (l *launch) seal() {
	l.handoff.LaunchSpecDigest = handlaunch.CanonicalSpecDigest(l.handoff.Spec)
}

func (l *launch) write(t *testing.T) {
	t.Helper()
	if err := WriteHandoff(l.dir, l.handoff); err != nil {
		t.Fatal(err)
	}
}

func (l *launch) credential() string {
	return l.handoff.Values[CredentialEnv]
}

type guardRun struct {
	cmd    *exec.Cmd
	output string
	done   chan struct{}
}

func newGuard(t *testing.T, locator string) *guardRun {
	t.Helper()
	g := &guardRun{output: filepath.Join(t.TempDir(), "guard.out"), done: make(chan struct{})}
	out, err := os.Create(g.output)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = out.Close() })
	g.cmd = exec.Command(testExecutable(t), locator)
	// The race runtime otherwise sleeps a second before every clean helper exit.
	g.cmd.Env = append(os.Environ(), roleEnv+"=guard", "GORACE=atexit_sleep_ms=0")
	g.cmd.Stdout, g.cmd.Stderr = out, out
	g.cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	return g
}

func startGuard(t *testing.T, locator string) *guardRun {
	t.Helper()
	g := newGuard(t, locator)
	g.start(t)
	return g
}

func (g *guardRun) start(t *testing.T) {
	t.Helper()
	if err := g.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = g.cmd.Wait()
		close(g.done)
	}()
	t.Cleanup(func() {
		_ = g.cmd.Process.Kill()
		<-g.done
		reapOrphans(t)
	})
}

func (g *guardRun) exited() bool {
	select {
	case <-g.done:
		return true
	default:
		return false
	}
}

func (g *guardRun) wait(t *testing.T) int {
	t.Helper()
	select {
	case <-g.done:
	case <-time.After(testDeadline):
		t.Fatal("guard did not exit")
	}
	return g.cmd.ProcessState.ExitCode()
}

func (g *guardRun) succeed(t *testing.T) int {
	t.Helper()
	code := g.wait(t)
	if code == guardFailed {
		t.Fatalf("guard failed: %s", read(t, g.output))
	}
	return code
}

func waitRecord(t *testing.T, dir, kind string, g *guardRun) Record {
	t.Helper()
	waitFor(t, kind+" record", func() bool {
		_, err := ReadRecord(dir, kind)
		return err == nil || g.exited()
	})
	return mustRecord(t, dir, kind)
}

func mustRecord(t *testing.T, dir, kind string) Record {
	t.Helper()
	record, err := ReadRecord(dir, kind)
	if err != nil {
		t.Fatalf("read %s record: %v", kind, err)
	}
	return record
}

// Kills and reaps every child this suite still has. Anything a guard left behind was
// reparented here, and a kill that is not reaped still races t.TempDir's removal.
func reapOrphans(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(testDeadline)
	for {
		pids, err := childrenByScan(os.Getpid())
		if err != nil {
			t.Error(err)
			return
		}
		if len(pids) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("children %v outlived the test", pids)
			return
		}
		for _, pid := range pids {
			_ = unix.Kill(-pid, unix.SIGKILL)
			_ = unix.Kill(pid, unix.SIGKILL)
			_, _ = unix.Wait4(pid, nil, unix.WNOHANG, nil)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitFor(t *testing.T, what string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(testDeadline)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func openPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("no pseudo-terminal multiplexer")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close() })
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	index, err := unix.IoctlGetUint32(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	tty, err := os.OpenFile("/dev/pts/"+strconv.Itoa(int(index)), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tty.Close() })
	return master, tty
}

func onTerminal(cmd *exec.Cmd, tty *os.File) {
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Pdeathsig: syscall.SIGKILL}
}

func testExecutable(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return self
}

func randomHex(t *testing.T, bytes int) string {
	t.Helper()
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(buffer)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	pid, err := strconv.Atoi(strings.TrimSpace(read(t, path)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}
