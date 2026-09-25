//go:build linux

package execguard

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/atqamz/hand/internal/osfacts"
	"github.com/atqamz/hand/internal/store"
	"golang.org/x/sys/unix"
)

func TestRecordsNameTheIncarnationsHandReadsFromTheOS(t *testing.T) {
	l := newLaunch(t, "/bin/sh", "-c", "exec sleep 300")
	l.write(t)
	g := startGuard(t, l.locator)
	running := waitRecord(t, l.dir, KindRunning, g)

	guard, err := osfacts.ReadIncarnation(g.cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if running.Guard != guard || running.LaunchOperationID != l.handoff.LaunchOperationID {
		t.Fatalf("running names guard %+v for %s, want the OS re-read %+v for %s (EG-3)", running.Guard, running.LaunchOperationID, guard, l.handoff.LaunchOperationID)
	}
	root, err := osfacts.ReadIncarnation(running.Root.PID)
	if err != nil {
		t.Fatal(err)
	}
	if *running.Root != root || running.ProcessGroup != root.PID || running.ObjectClass != ClassExact {
		t.Fatalf("running root %+v group %d class %s, want %+v in its own group, exact (EG-3, EG-7)", *running.Root, running.ProcessGroup, running.ObjectClass, root)
	}
	impersonator := running.Guard
	impersonator.StartTicks--
	if got := osfacts.Observe(impersonator); got != osfacts.Absent {
		t.Fatalf("Observe(guard pid, other start) = %s, want absent (EG-3)", got)
	}
	verifier := l.handoff.Spec.Environment[CredentialEnv].ValueDigest
	for _, kind := range []string{KindClaimed, KindPinned, KindRunning} {
		record := mustRecord(t, l.dir, kind)
		if record.Guard != guard || record.LaunchOperationID != l.handoff.LaunchOperationID {
			t.Errorf("%s names %+v for %s, want this guard and Launch", kind, record.Guard, record.LaunchOperationID)
		}
		if kind != KindClaimed && (record.RequestDigest != l.handoff.RequestDigest || record.LaunchSpecDigest != l.handoff.LaunchSpecDigest || record.CredentialVerifier != verifier) {
			t.Errorf("%s digests = %q %q %q, want the committed request, launch-spec and V_B (EG-5)", kind, record.RequestDigest, record.LaunchSpecDigest, record.CredentialVerifier)
		}
	}
}

func TestOneHandoffStartsExactlyOneHarnessWhenTwoGuardsRace(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	l := newLaunch(t, "/bin/sh", "-c", `echo ran >> "$0"`, marker)
	l.write(t)
	first, second := newGuard(t, l.locator), newGuard(t, l.locator)
	first.start(t)
	second.start(t)
	codes := []int{first.wait(t), second.wait(t)}
	if (codes[0] == guardFailed) == (codes[1] == guardFailed) {
		t.Fatalf("guard exits = %v, want exactly one lost claim (EG-2)", codes)
	}
	if got := read(t, marker); got != "ran\n" {
		t.Fatalf("harness ran %q, want exactly once (EG-2)", got)
	}
}

func TestFenceAndClaimHaveExactlyOneWinner(t *testing.T) {
	for range 10 {
		marker := filepath.Join(t.TempDir(), "ran")
		l := newLaunch(t, "/bin/sh", "-c", `echo ran >> "$0"`, marker)
		l.write(t)
		g := startGuard(t, l.locator)
		fenceErr := Fence(l.dir)
		g.wait(t)
		claimed, ran := exists(filepath.Join(l.dir, KindClaimed)), exists(marker)
		if fenceErr != nil && !errors.Is(fenceErr, fs.ErrNotExist) {
			t.Fatal(fenceErr)
		}
		if (fenceErr == nil) == claimed || claimed != ran {
			t.Fatalf("fence won %t, guard claimed %t, harness ran %t: want exactly one winner (EG-2)", fenceErr == nil, claimed, ran)
		}
	}
}

func TestClaimIsRecordedAndTheHandoffRemovedBeforeTheHarnessStarts(t *testing.T) {
	l := newLaunch(t, "/bin/sh", "-c", `[ -s "$0/claimed" ] && [ ! -e "$0/handoff" ] && ! ls "$0" | grep -q '^claim\.'`)
	l.handoff.Spec.Arguments = append(l.handoff.Spec.Arguments, l.dir)
	l.seal()
	l.write(t)
	g := startGuard(t, l.locator)
	if code := g.succeed(t); code != 0 {
		t.Fatalf("harness saw exit %d: the claim was not durable and the handoff not gone before it started (EG-2)", code)
	}
}

func TestHandoffThatFailsItsCheckIsRefusedBeforeAnyHarness(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*launch)
		raw    func(*launch) []byte
	}{
		{name: "another boot", mutate: func(l *launch) { l.handoff.BootID = "00000000-0000-0000-0000-000000000000" }},
		{name: "another PID namespace", mutate: func(l *launch) { l.handoff.PIDNamespace++ }},
		{name: "another user", mutate: func(l *launch) { l.handoff.UID++ }},
		{name: "another Launch", mutate: func(l *launch) { l.handoff.LaunchOperationID = "op_launch_other" }},
		{name: "spec changed after commitment", mutate: func(l *launch) { l.handoff.Spec.Arguments = append(l.handoff.Spec.Arguments, "extra") }},
		{name: "credential verifier for another Fleet", mutate: func(l *launch) {
			entry := l.handoff.Spec.Environment[CredentialEnv]
			entry.ValueDigest = store.CanonicalV19ExecGuardCredentialVerifier("f_00000000000000000000000000000000", l.handoff.ExecutorBindingID, l.credential())
			l.handoff.Spec.Environment[CredentialEnv] = entry
			l.seal()
		}},
		{name: "resolved secret that is not the committed one", mutate: func(l *launch) { l.handoff.Values["TOKEN"] = "other-token" }},
		{name: "relative executable", mutate: func(l *launch) { l.handoff.Spec.Executable = "sh"; l.seal() }},
		{name: "cwd that cannot be entered", mutate: func(l *launch) { l.handoff.Spec.Cwd = filepath.Join(l.worktree, "missing"); l.seal() }},
		{name: "cwd that is not the WorktreeBinding", mutate: func(l *launch) { l.handoff.Spec.Cwd = l.dir; l.seal() }},
		{name: "unknown protocol version", raw: func(l *launch) []byte {
			return []byte(`{"protocol":"hand-exec-guard:v9","launch_operation_id":"` + l.handoff.LaunchOperationID + `"}`)
		}},
		{name: "torn handoff", raw: func(*launch) []byte { return []byte(`{"protocol":"hand-exec-guard:v1","spec":`) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "ran")
			l := newLaunch(t, "/bin/sh", "-c", `touch "$0"`, marker)
			if test.raw != nil {
				if err := os.WriteFile(l.locator, test.raw(l), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				test.mutate(l)
				l.write(t)
			}
			g := startGuard(t, l.locator)
			if code := g.wait(t); code != guardFailed {
				t.Fatalf("guard exit = %d, want a refusal", code)
			}
			refused := mustRecord(t, l.dir, KindRefused)
			if refused.Reason == "" || exists(marker) || exists(filepath.Join(l.dir, KindPinned)) || exists(filepath.Join(l.dir, KindRunning)) {
				t.Fatalf("refused %q, harness ran %t, pinned %t: want a refusal before any harness (EG-2)", refused.Reason, exists(marker), exists(filepath.Join(l.dir, KindPinned)))
			}
			assertSecretAbsent(t, l.credential(), l.dir, g.output)
		})
	}
}

func TestHarnessEnvironmentIsTheExactSpecWithNoInheritedSemanticKey(t *testing.T) {
	seen := filepath.Join(t.TempDir(), "environ")
	l := newLaunch(t, "/bin/sh", "-c", `cat /proc/$$/environ > "$0"`, seen)
	l.write(t)
	g := newGuard(t, l.locator)
	g.cmd.Env = append(g.cmd.Env, "HAND_HOME=/other/fleet", "HAND_ROLE=supervisor", "HAND_WORKER_CREDENTIAL=stale-credential",
		"HAND_ATTEMPT_ID=at_stale", "HERDR_PANE_ID=p_stale", "CLAUDECODE=1", "EXECGUARD_KEPT=kept")
	g.start(t)
	if code := g.succeed(t); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	environ := map[string]string{}
	for _, entry := range strings.Split(strings.TrimRight(read(t, seen), "\x00"), "\x00") {
		name, value, _ := strings.Cut(entry, "=")
		environ[name] = value
	}
	for name, value := range l.handoff.Values {
		if environ[name] != value {
			t.Errorf("harness %s = %q, want the exact spec value (EG-12)", name, environ[name])
		}
	}
	for _, name := range []string{"HAND_HOME", "HAND_ATTEMPT_ID", "HERDR_PANE_ID", "CLAUDECODE"} {
		if value, ok := environ[name]; ok {
			t.Errorf("harness inherited semantic %s=%q (EG-12)", name, value)
		}
	}
	if environ["EXECGUARD_KEPT"] != "kept" {
		t.Errorf("harness lost the non-semantic inherited EXECGUARD_KEPT")
	}
	if strings.Contains(strings.Join(g.cmd.Args, " "), l.credential()) {
		t.Error("the credential reached the guard command line (EG-1)")
	}
	assertSecretAbsent(t, l.credential(), l.dir, g.output)
}

func TestSameBasenameElsewhereOnPATHIsNeverRunAndAScriptIsSampled(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "ran")
	for _, dir := range []string{"trusted", "foreign"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
		script := "#!/bin/sh\necho " + dir + " \"$0\" > \"$1\"\nsleep 0.3\n"
		if err := os.WriteFile(filepath.Join(root, dir, "harness"), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	trusted := filepath.Join(root, "trusted", "harness")
	l := newLaunch(t, trusted, marker)
	l.write(t)
	g := newGuard(t, l.locator)
	g.cmd.Env = append(g.cmd.Env, "PATH="+filepath.Join(root, "foreign")+":"+os.Getenv("PATH"))
	g.start(t)
	g.succeed(t)
	if ran := read(t, marker); ran != "trusted "+trusted+"\n" {
		t.Fatalf("ran %q, want the exact absolute spec path as $0 (EG-7)", ran)
	}
	var want syscall.Stat_t
	if err := syscall.Stat(trusted, &want); err != nil {
		t.Fatal(err)
	}
	pinned, running := mustRecord(t, l.dir, KindPinned), mustRecord(t, l.dir, KindRunning)
	if pinned.Executable.Path != trusted || pinned.Executable.Inode != want.Ino || running.ObjectClass != ClassSampled {
		t.Fatalf("pinned %+v class %s, want %s inode %d sampled (EG-7)", *pinned.Executable, running.ObjectClass, trusted, want.Ino)
	}
	if running.Interpreter == nil || running.Interpreter.Inode == 0 {
		t.Fatalf("running records no interpreter image for a script (EG-7)")
	}
}

func TestNativeHarnessKeepsItsNameAndInheritsNoPinnedDescriptor(t *testing.T) {
	l := newLaunch(t, "/bin/sh", "-c", `[ "$(cat /proc/$$/comm)" = sh ] && [ ! -e /proc/$$/fd/3 ]`)
	l.write(t)
	g := startGuard(t, l.locator)
	if code := g.succeed(t); code != 0 || mustRecord(t, l.dir, KindRunning).ObjectClass != ClassExact {
		t.Fatalf("exit %d: an exact harness must run under its own name without the pinned descriptor", code)
	}
}

func TestPathReplacedOrRetargetedAfterPinIsRefusedBeforeStart(t *testing.T) {
	dir := t.TempDir()
	path, other := filepath.Join(dir, "harness"), filepath.Join(dir, "other")
	for _, file := range []string{path, other} {
		if err := os.WriteFile(file, []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		pinned  string
		replace func() error
	}{
		{"path replaced", path, func() error {
			replacement := filepath.Join(dir, "replacement")
			if err := os.WriteFile(replacement, []byte("#!/bin/sh\n"), 0o700); err != nil {
				return err
			}
			return os.Rename(replacement, path)
		}},
		{"symlink retargeted", link, func() error {
			if err := os.Remove(link); err != nil {
				return err
			}
			return os.Symlink(other, link)
		}},
	} {
		exe, pinned, _, err := pin(test.pinned)
		if err != nil {
			t.Fatal(err)
		}
		_ = exe.Close()
		if !stillNames(test.pinned, pinned) {
			t.Fatalf("%s: an unchanged path does not name its pinned object", test.name)
		}
		if err := test.replace(); err != nil {
			t.Fatal(err)
		}
		if stillNames(test.pinned, pinned) {
			t.Fatalf("%s: the start check still accepts the path (EG-7)", test.name)
		}
	}
}

func TestPinnedDescriptorRunsTheObjectItHashedWhenThePathIsReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "harness")
	copyExecutable(t, "/bin/sh", path)
	exe, pinned, class, err := pin(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = exe.Close() }()
	falseBinary, err := exec.LookPath("false")
	if err != nil {
		t.Skip("no false on PATH")
	}
	replacement := filepath.Join(dir, "replacement")
	copyExecutable(t, falseBinary, replacement)
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	pid, err := startHarness(exe, t.TempDir(), class, []string{path, "-c", "exit 0"}, os.Environ(), false)
	if err != nil {
		t.Fatal(err)
	}
	var status unix.WaitStatus
	if _, err := unix.Wait4(pid, &status, 0, nil); err != nil || status.ExitStatus() != 0 || class != ClassExact {
		t.Fatalf("class %s, harness wait %v, status %v: want the pinned sh, not the replacement false (EG-7)", class, err, status)
	}
	sum := sha256.Sum256([]byte(read(t, "/bin/sh")))
	if pinned.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("pinned digest %s, want the object hashed at pin time (EG-7)", pinned.SHA256)
	}
}

func TestHarnessStartsWithAnEmptySignalMask(t *testing.T) {
	grep, err := exec.LookPath("grep")
	if err != nil {
		t.Skip("no grep on PATH")
	}
	l := newLaunch(t, grep, "-q", "^SigBlk:[[:space:]]*0*$", "/proc/self/status")
	l.write(t)
	g := newGuard(t, l.locator)
	g.cmd.Env = append(g.cmd.Env, roleEnv+"=blocked-guard")
	g.start(t)
	if code := g.succeed(t); code != 0 {
		t.Fatalf("exit %d: the harness inherited the guard's blocked SIGUSR1", code)
	}
}

func copyExecutable(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o700); err != nil {
		t.Fatal(err)
	}
}

func assertSecretAbsent(t *testing.T, secret string, paths ...string) {
	t.Helper()
	for _, root := range paths {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			if strings.Contains(read(t, path), secret) {
				t.Errorf("the credential reached %s (EG-1)", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
