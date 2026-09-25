//go:build linux

// Package execguard is the Linux `hand exec-guard` of the #346 revision-2 adapter
// contract (docs/architecture/v19-contracts/346-capability-adapters-v2.md). The guard
// claims one Launch handoff, starts the harness from the pinned executable, contains
// its whole tree as child subreaper, and writes typed records into the Launch's
// Fleet-private directory. It never opens SQLite and never decides currentness: Hand
// core checks every record against the OS facts it reads itself (internal/osfacts).
package execguard

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/osfacts"
	"github.com/atqamz/hand/internal/store"
	"github.com/atqamz/hand/internal/toolchain"
	"golang.org/x/sys/unix"
)

const (
	terminationGrace = 5 * time.Second
	pollInterval     = 100 * time.Millisecond
	cessationRule    = "wait4-echild"
)

type execution struct {
	dir    string
	launch string
	guard  osfacts.Incarnation
}

// Run executes the guard for the handoff at locator and returns the harness exit
// code. A refusal writes `refused`; a lost claim writes nothing.
func Run(locator string) (int, error) {
	return run(locator, terminationGrace)
}

func run(locator string, grace time.Duration) (int, error) {
	// Catching every signal before the harness starts gives it default dispositions:
	// an inherited SIG_IGN would otherwise survive exec. Nothing reads this channel.
	signal.Notify(make(chan os.Signal, 1))
	events := make(chan os.Signal, 16)
	signal.Notify(events, unix.SIGTERM, unix.SIGHUP, unix.SIGCHLD)
	if !filepath.IsAbs(locator) || filepath.Base(locator) != handoffName {
		return 0, fmt.Errorf("exec guard handoff locator %q is not an absolute %s path", locator, handoffName)
	}
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		return 0, fmt.Errorf("become child subreaper: %w", err)
	}
	guard, err := osfacts.ReadIncarnation(os.Getpid())
	if err != nil {
		return 0, fmt.Errorf("read guard incarnation: %w", err)
	}
	dir := filepath.Dir(locator)
	g := &execution{dir: dir, launch: filepath.Base(dir), guard: guard}
	claim := filepath.Join(dir, claimPrefix+claimName(guard))
	if err := os.Rename(locator, claim); err != nil {
		return 0, fmt.Errorf("claim exec guard handoff: %w", err)
	}
	if err := g.writeSynced(Record{Kind: KindClaimed}); err != nil {
		return 0, fmt.Errorf("record exec guard claim: %w", err)
	}
	handoff, readErr := readHandoff(claim)
	if err := os.Remove(claim); err != nil {
		return g.refuse("the claimed handoff could not be removed")
	}
	if readErr != nil {
		return g.refuse(readErr.Error())
	}
	verifier, reason := g.check(handoff)
	if reason != "" {
		return g.refuse(reason)
	}
	release, err := acquireGenerationLease(handoff.FleetID, g.launch)
	if err != nil {
		return g.refuse("the managed Hand generation lease could not be held")
	}
	defer func() { _ = release() }()
	return g.execute(handoff, verifier, events, grace)
}

func claimName(guard osfacts.Incarnation) string {
	return guard.BootID + "." + strconv.Itoa(guard.PID) + "." + strconv.FormatUint(guard.StartTicks, 10)
}

func readHandoff(path string) (Handoff, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Handoff{}, errors.New("the claimed handoff could not be read")
	}
	var version struct {
		Protocol string `json:"protocol"`
	}
	if err := json.Unmarshal(data, &version); err != nil {
		return Handoff{}, errors.New("the claimed handoff is not parsable")
	}
	if version.Protocol != Protocol {
		return Handoff{}, errors.New("the handoff protocol version is unknown")
	}
	var handoff Handoff
	if err := json.Unmarshal(data, &handoff); err != nil {
		return Handoff{}, errors.New("the claimed handoff is not parsable")
	}
	return handoff, nil
}

// Refusal reasons are fixed texts: none may carry the credential or a resolved value.
func (g *execution) check(handoff Handoff) (string, string) {
	if handoff.LaunchOperationID != g.launch {
		return "", "the handoff names another Launch"
	}
	if handoff.BootID != g.guard.BootID {
		return "", "the handoff was written on another boot"
	}
	namespace, err := osfacts.SelfPIDNamespace()
	if err != nil || namespace != handoff.PIDNamespace {
		return "", "the handoff was written in another PID namespace"
	}
	uid, err := osfacts.RealUID(os.Getpid())
	if err != nil || uid != handoff.UID {
		return "", "the handoff was written by another user"
	}
	spec := handoff.Spec
	if store.CanonicalV19LaunchSpecDigest(spec) != handoff.LaunchSpecDigest {
		return "", "the launch-spec digest does not match the persisted spec"
	}
	if !filepath.IsAbs(spec.Executable) {
		return "", "the Launch executable is not an absolute path"
	}
	binding := spec.Environment[ExecutorBindingEnv]
	if handoff.ExecutorBindingID == "" || binding.ValueKind != "literal" || binding.ValueMaterial != handoff.ExecutorBindingID {
		return "", "the ExecutorBinding environment entry does not name the handoff's binding"
	}
	credential := spec.Environment[CredentialEnv]
	if credential.ValueKind != "secret-ref" || credential.ValueMaterial != Protocol {
		return "", "the credential environment entry is not an exec-guard credential"
	}
	if secret, err := base64.RawURLEncoding.DecodeString(handoff.Values[CredentialEnv]); err != nil || len(secret) != 32 {
		return "", "the credential is not 32 bytes of unpadded base64url"
	}
	for _, name := range slices.Sorted(maps.Keys(spec.Environment)) {
		entry := spec.Environment[name]
		value, ok := handoff.Values[name]
		if !ok {
			return "", "a Launch environment entry has no resolved value"
		}
		if entry.ValueKind == "literal" && value != entry.ValueMaterial {
			return "", "a literal Launch environment value differs from its material"
		}
		digest := store.CanonicalV19LaunchEnvironmentValueDigest(value)
		if name == CredentialEnv {
			digest = store.CanonicalV19ExecGuardCredentialVerifier(handoff.FleetID, handoff.ExecutorBindingID, value)
		}
		if digest != entry.ValueDigest {
			return "", "a resolved Launch environment value does not match its committed digest"
		}
	}
	return credential.ValueDigest, ""
}

func acquireGenerationLease(fleetID, launch string) (func() error, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	runtimeStore, err := toolchain.DefaultStore()
	if err != nil {
		return nil, err
	}
	unmanaged := func() error { return nil }
	generations, err := os.Stat(filepath.Join(runtimeStore.Root, "runtime", "hand-generations"))
	if errors.Is(err, fs.ErrNotExist) {
		return unmanaged, nil
	}
	if err != nil {
		return nil, err
	}
	generation := filepath.Dir(executable)
	parent, err := os.Stat(filepath.Dir(generation))
	if err != nil {
		return nil, err
	}
	if !os.SameFile(parent, generations) {
		return unmanaged, nil
	}
	lease, err := runtimeStore.AcquireHandLease(toolchain.LeaseRequest{
		Generation: "sha256:" + filepath.Base(generation),
		LeaseID:    "exec-guard:" + launch,
		FleetID:    fleetID,
		Consumer:   "exec-guard",
		Evidence:   "launch=" + launch,
	})
	if err != nil {
		return nil, err
	}
	return lease.Close, nil
}

func (g *execution) execute(handoff Handoff, verifier string, events <-chan os.Signal, grace time.Duration) (int, error) {
	spec := handoff.Spec
	if err := os.Chdir(spec.Cwd); err != nil {
		return g.refuse("the Launch cwd could not be entered")
	}
	cwd, cwdErr := os.Stat(".")
	worktree, worktreeErr := os.Stat(handoff.WorktreePath)
	if cwdErr != nil || worktreeErr != nil || !os.SameFile(cwd, worktree) {
		return g.refuse("the Launch cwd is not the WorktreeBinding path")
	}
	exe, pinned, class, err := pin(spec.Executable)
	if err != nil {
		return g.refuse("the Launch executable could not be pinned")
	}
	defer func() { _ = exe.Close() }()
	digests := Record{RequestDigest: handoff.RequestDigest, LaunchSpecDigest: handoff.LaunchSpecDigest, CredentialVerifier: verifier}
	record := digests
	record.Kind, record.Executable = KindPinned, &pinned
	if err := g.write(record); err != nil {
		return g.refuse("the pinned record could not be written")
	}
	if !stillNames(spec.Executable, pinned) {
		return g.refuse("the Launch executable path no longer names the pinned object")
	}
	foreground := holdsForeground()
	root, err := startHarness(exe, append([]string{spec.Executable}, spec.Arguments...), harnessEnvironment(os.Environ(), handoff), foreground)
	if err != nil {
		return g.refuse("the harness could not be started")
	}
	signal.Ignore(unix.SIGTTIN, unix.SIGTTOU)
	running, err := g.running(digests, root, class)
	if err == nil {
		err = g.writeSynced(running)
	}
	if err != nil {
		if _, killErr := g.kill(root); killErr != nil {
			return 0, killErr
		}
		return g.refuse("the running record could not be written")
	}
	cause, interrupt, status, err := g.supervise(root, events, grace)
	if err != nil {
		return 0, err
	}
	if foreground {
		_ = unix.IoctlSetPointerInt(0, unix.TIOCSPGRP, unix.Getpgrp())
		_ = unix.IoctlSetInt(0, unix.TCFLSH, unix.TCIFLUSH)
	}
	code, sig := exitStatus(status)
	return code, g.write(Record{
		Kind:                 KindCeased,
		Root:                 running.Root,
		Cause:                cause,
		InterruptOperationID: interrupt,
		Exit:                 &Exit{Code: code, Signal: sig},
		Predicate:            cessationRule,
	})
}

func pin(path string) (*os.File, Object, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, Object{}, "", err
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("not a regular file")
	}
	hash := sha256.New()
	if err == nil {
		_, err = io.Copy(hash, file)
	}
	if err != nil {
		_ = file.Close()
		return nil, Object{}, "", err
	}
	magic := make([]byte, 4)
	class := ClassSampled
	if n, _ := file.ReadAt(magic, 0); n == len(magic) && string(magic) == "\x7fELF" {
		class = ClassExact
	}
	stat := info.Sys().(*syscall.Stat_t)
	return file, Object{Path: path, Device: uint64(stat.Dev), Inode: stat.Ino, SHA256: hex.EncodeToString(hash.Sum(nil))}, class, nil
}

func stillNames(path string, pinned Object) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	stat := info.Sys().(*syscall.Stat_t)
	return uint64(stat.Dev) == pinned.Device && stat.Ino == pinned.Inode
}

func harnessEnvironment(inherited []string, handoff Handoff) []string {
	env := make([]string, 0, len(inherited)+len(handoff.Spec.Environment))
	for _, entry := range inherited {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := handoff.Spec.Environment[name]; replaced || herdr.DaemonEnvironmentKey(name) {
			continue
		}
		env = append(env, entry)
	}
	for _, name := range slices.Sorted(maps.Keys(handoff.Spec.Environment)) {
		env = append(env, name+"="+handoff.Values[name])
	}
	return env
}

func holdsForeground() bool {
	group, err := unix.IoctlGetUint32(0, unix.TIOCGPGRP)
	return err == nil && int(group) == unix.Getpgrp()
}

// Exec goes through the pinned descriptor so the object run is the one hashed. It
// stays open across exec because a #! interpreter reopens the script by this path.
func startHarness(exe *os.File, argv, env []string, foreground bool) (int, error) {
	return syscall.ForkExec("/proc/self/fd/3", argv, &syscall.ProcAttr{
		Env:   env,
		Files: []uintptr{0, 1, 2, exe.Fd()},
		Sys:   &syscall.SysProcAttr{Setpgid: true, Foreground: foreground},
	})
}

func (g *execution) running(digests Record, root int, class string) (Record, error) {
	rootIncarnation, err := osfacts.ReadIncarnation(root)
	if err != nil {
		return Record{}, err
	}
	terminal, err := osfacts.ControllingTerminal(os.Getpid())
	if err != nil {
		return Record{}, err
	}
	record := digests
	record.Kind, record.Root, record.ProcessGroup, record.Terminal, record.ObjectClass = KindRunning, &rootIncarnation, root, terminal, class
	if class == ClassSampled {
		record.Interpreter = observedImage(root)
	}
	return record, nil
}

func observedImage(pid int) *Object {
	link := filepath.Join(procRoot, strconv.Itoa(pid), "exe")
	path, err := os.Readlink(link)
	if err != nil {
		return nil
	}
	info, err := os.Stat(link)
	if err != nil {
		return nil
	}
	stat := info.Sys().(*syscall.Stat_t)
	return &Object{Path: path, Device: uint64(stat.Dev), Inode: stat.Ino}
}

func (g *execution) supervise(root int, events <-chan os.Signal, grace time.Duration) (string, string, unix.WaitStatus, error) {
	var (
		cause, interrupt string
		deadline         time.Time
		status           unix.WaitStatus
		rootReaped       bool
	)
	terminate := func(why, operation string) error {
		if cause != "" {
			return nil
		}
		cause, interrupt, deadline = why, operation, time.Now().Add(grace)
		_, err := g.signalTree(unix.SIGTERM)
		return err
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		var err error
		select {
		case sig := <-events:
			switch sig {
			case unix.SIGTERM:
				err = terminate(CauseExternalTermination, "")
			case unix.SIGHUP:
				err = terminate(CauseHangup, "")
			}
		case <-ticker.C:
			if request, ok := g.interruptRequest(); ok {
				err = terminate(CauseInterruptRequest, request)
			}
		}
		if err != nil {
			return "", "", 0, err
		}
		for {
			var ws unix.WaitStatus
			pid, err := unix.Wait4(-1, &ws, unix.WNOHANG, nil)
			if errors.Is(err, unix.ECHILD) && rootReaped {
				return cause, interrupt, status, nil
			}
			if err != nil {
				return "", "", 0, fmt.Errorf("reap execution tree: %w", err)
			}
			if pid == 0 {
				break
			}
			if pid == root {
				status, rootReaped = ws, true
				if err := terminate(CauseHarnessExit, ""); err != nil {
					return "", "", 0, err
				}
			}
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			killed, err := g.kill(root)
			if !rootReaped {
				status = killed
			}
			return cause, interrupt, status, err
		}
	}
}

func (g *execution) interruptRequest() (string, bool) {
	request, err := ReadRecord(g.dir, KindInterruptRequest)
	if err != nil || request.LaunchOperationID != g.launch || request.Guard != g.guard || request.InterruptOperationID == "" {
		return "", false
	}
	return request.InterruptOperationID, true
}

// Stops the whole tree before killing it: a stopped process cannot fork, so once a
// full pass finds nothing running, the kill pass reaches every descendant.
func (g *execution) kill(root int) (unix.WaitStatus, error) {
	for {
		running, err := g.signalTree(unix.SIGSTOP)
		if err != nil {
			return 0, err
		}
		if running == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := g.signalTree(unix.SIGKILL); err != nil {
		return 0, err
	}
	var status unix.WaitStatus
	for {
		var ws unix.WaitStatus
		pid, err := unix.Wait4(-1, &ws, 0, nil)
		switch {
		case errors.Is(err, unix.ECHILD):
			return status, nil
		case errors.Is(err, unix.EINTR):
		case err != nil:
			return 0, fmt.Errorf("reap execution tree: %w", err)
		case pid == root:
			status = ws
		}
	}
}

// Signals every descendant through a pidfd, after re-reading that the pinned process
// still has the walked parent, or the guard after reparenting, and started after the
// guard, so a reused PID is never signalled. Returns how many were still running.
func (g *execution) signalTree(sig unix.Signal) (int, error) {
	self := os.Getpid()
	members, err := descendants(self)
	if err != nil {
		return 0, err
	}
	running := 0
	for _, member := range members {
		fd, err := unix.PidfdOpen(member.pid, 0)
		if err != nil {
			continue
		}
		stat, err := readStat(member.pid)
		if err == nil && (stat.ppid == member.parent || stat.ppid == self) && stat.startTicks >= g.guard.StartTicks {
			if !strings.ContainsRune("TtZX", rune(stat.state)) {
				running++
			}
			_ = unix.PidfdSendSignal(fd, sig, nil, 0)
		}
		_ = unix.Close(fd)
	}
	return running, nil
}

func exitStatus(status unix.WaitStatus) (int, int) {
	if status.Signaled() {
		return 128 + int(status.Signal()), int(status.Signal())
	}
	return status.ExitStatus(), 0
}

func (g *execution) refuse(reason string) (int, error) {
	err := g.write(Record{Kind: KindRefused, Reason: reason})
	return 0, errors.Join(fmt.Errorf("exec guard refused Launch %s: %s", g.launch, reason), err)
}

func (g *execution) write(record Record) error {
	record.LaunchOperationID, record.Guard = g.launch, g.guard
	return writeRecord(g.dir, record)
}

func (g *execution) writeSynced(record Record) error {
	if err := g.write(record); err != nil {
		return err
	}
	return errors.Join(syncPath(filepath.Join(g.dir, record.Kind)), syncPath(g.dir))
}

func syncPath(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
