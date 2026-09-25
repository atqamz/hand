//go:build linux

package osfacts

import (
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const nonDumpableChildEnv = "OSFACTS_TEST_NON_DUMPABLE_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(nonDumpableChildEnv) == "1" {
		runNonDumpableChild()
		return
	}
	os.Exit(m.Run())
}

// Flips PR_SET_DUMPABLE off, then blocks on stdin so a test can observe this
// process through /proc while it is still alive.
func runNonDumpableChild() {
	_ = unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)
	_, _ = io.Copy(io.Discard, os.Stdin)
}

// The comm field between the parens can itself contain spaces and ')', so every
// later field must be found from the line's *last* ')', not its first.
func TestStatFieldHandlesCommWithSpacesAndParens(t *testing.T) {
	stat := []byte("4567 (weird comm with ) spaces and parens) S 1 4567 4567 34816 4567 0 0 0 0 0 0 0 0 0 20 0 1 0 123456789")

	tty, err := statField(stat, 7)
	if err != nil || tty != "34816" {
		t.Fatalf("tty_nr = %q, %v, want 34816, nil", tty, err)
	}
	tpgid, err := statField(stat, 8)
	if err != nil || tpgid != "4567" {
		t.Fatalf("tpgid = %q, %v, want 4567, nil", tpgid, err)
	}
	ticks, err := parseStartTicks(stat)
	if err != nil || ticks != 123456789 {
		t.Fatalf("starttime = %d, %v, want 123456789, nil", ticks, err)
	}
	state, err := parseState(stat)
	if err != nil || state != 'S' {
		t.Fatalf("state = %q, %v, want S, nil", state, err)
	}
	threads, err := parseNumThreads(stat)
	if err != nil || threads != 1 {
		t.Fatalf("num_threads = %d, %v, want 1, nil", threads, err)
	}
}

// classifyState is the pure decision the guard-crash counterexamples need: a Z or X
// state proves death only when no other thread of the same process could still be
// running. The dangerous direction is a false Absent, so that case stays Unknown.
func TestClassifyState(t *testing.T) {
	cases := []struct {
		name    string
		state   byte
		threads int
		want    Observation
	}{
		{"running, single thread", 'S', 1, Alive},
		{"running, many threads", 'R', 8, Alive},
		{"zombie, single thread", 'Z', 1, Absent},
		{"dead, single thread", 'X', 1, Absent},
		{"zombie, main thread exited but others remain", 'Z', 4, Unknown},
		{"dead, main thread exited but others remain", 'X', 2, Unknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyState(c.state, c.threads); got != c.want {
				t.Fatalf("classifyState(%q, %d) = %s, want %s", c.state, c.threads, got, c.want)
			}
		})
	}
}

func TestObserveSelfIsAlive(t *testing.T) {
	self, err := ReadIncarnation(os.Getpid())
	if err != nil {
		t.Fatalf("ReadIncarnation(self): %v", err)
	}
	if got := Observe(self); got != Alive {
		t.Fatalf("Observe(self) = %s, want alive", got)
	}
}

func TestObservePIDReuseImpersonationIsAbsent(t *testing.T) {
	self, err := ReadIncarnation(os.Getpid())
	if err != nil {
		t.Fatalf("ReadIncarnation(self): %v", err)
	}
	impersonator := self
	impersonator.StartTicks++
	if got := Observe(impersonator); got != Absent {
		t.Fatalf("Observe(right pid, wrong start) = %s, want absent", got)
	}
}

func TestObserveReapedChildIsAbsent(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep binary on PATH")
	}
	cmd := exec.Command(sleep, "0.2")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	child, err := ReadIncarnation(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("ReadIncarnation(child): %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait for child: %v", err)
	}
	if got := Observe(child); got != Absent {
		t.Fatalf("Observe(reaped child) = %s, want absent", got)
	}
}

func TestObserveBootChangedWhenBootIDDiffers(t *testing.T) {
	self, err := ReadIncarnation(os.Getpid())
	if err != nil {
		t.Fatalf("ReadIncarnation(self): %v", err)
	}
	stale := self
	stale.BootID = "not-the-current-boot"
	if got := Observe(stale); got != BootChanged {
		t.Fatalf("Observe(stale boot id) = %s, want boot-changed", got)
	}
}

func TestZeroIncarnationIsUnknown(t *testing.T) {
	if got := Observe(Incarnation{}); got != Unknown {
		t.Fatalf("Observe(zero value) = %s, want unknown", got)
	}
}

func TestControllingTerminalAndForegroundGroupInAPty(t *testing.T) {
	t.Skip("no pty test helper exists on main; the guard PR #662 (unmerged, branch 346-execguard-linux) adds one for its own pty test, so this case is deferred until that lands or a shared helper is added")
}

func TestSelfPIDNamespaceIsStable(t *testing.T) {
	first, err := SelfPIDNamespace()
	if err != nil {
		t.Fatalf("SelfPIDNamespace: %v", err)
	}
	second, err := PIDNamespace(os.Getpid())
	if err != nil {
		t.Fatalf("PIDNamespace(self): %v", err)
	}
	if first != second || first == 0 {
		t.Fatalf("SelfPIDNamespace() = %d, PIDNamespace(self) = %d, want equal and nonzero", first, second)
	}
}

func TestRealUIDOfSelfMatchesOSGetuid(t *testing.T) {
	uid, err := RealUID(os.Getpid())
	if err != nil {
		t.Fatalf("RealUID(self): %v", err)
	}
	if int(uid) != os.Getuid() {
		t.Fatalf("RealUID(self) = %d, want %d", uid, os.Getuid())
	}
}

func TestScanCandidatesIncludesSelfAndHonorsStartTicksFilter(t *testing.T) {
	uid, err := RealUID(os.Getpid())
	if err != nil {
		t.Fatalf("RealUID(self): %v", err)
	}
	ns, err := SelfPIDNamespace()
	if err != nil {
		t.Fatalf("SelfPIDNamespace: %v", err)
	}
	ticks, err := readStartTicks(os.Getpid())
	if err != nil {
		t.Fatalf("readStartTicks(self): %v", err)
	}

	candidates, err := ScanCandidates(uid, ns, ticks)
	if err != nil {
		t.Fatalf("ScanCandidates: %v", err)
	}
	if !containsPID(candidates, os.Getpid()) {
		t.Fatalf("ScanCandidates(uid, ns, ticks) = %v, want self %d included", candidates, os.Getpid())
	}

	after, err := ScanCandidates(uid, ns, ticks+1)
	if err != nil {
		t.Fatalf("ScanCandidates: %v", err)
	}
	if containsPID(after, os.Getpid()) {
		t.Fatalf("ScanCandidates(uid, ns, ticks+1) = %v, want self %d excluded", after, os.Getpid())
	}
}

func TestIsCandidateFilterLogic(t *testing.T) {
	uid, err := RealUID(os.Getpid())
	if err != nil {
		t.Fatalf("RealUID(self): %v", err)
	}
	ns, err := SelfPIDNamespace()
	if err != nil {
		t.Fatalf("SelfPIDNamespace: %v", err)
	}
	ticks, err := readStartTicks(os.Getpid())
	if err != nil {
		t.Fatalf("readStartTicks(self): %v", err)
	}

	if !isCandidate(os.Getpid(), uid, ns, ticks) {
		t.Fatalf("isCandidate(self, matching uid/ns/ticks) = false, want true")
	}
	if isCandidate(os.Getpid(), uid+1, ns, ticks) {
		t.Fatalf("isCandidate(self, wrong uid) = true, want false")
	}
	if isCandidate(os.Getpid(), uid, ns+1, ticks) {
		t.Fatalf("isCandidate(self, wrong ns) = true, want false")
	}
	if isCandidate(os.Getpid(), uid, ns, ticks+1) {
		t.Fatalf("isCandidate(self, ticks below minimum) = true, want false")
	}
}

// P1: an EACCES on a same-uid process's ns link (for example from a non-dumpable
// target) must keep the pid as a candidate, not silently drop it, because the
// dangerous direction is a false absence check pass while the pid may hold B.
func TestScanCandidatesKeepsANonDumpableSameUIDProcess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: CAP_SYS_PTRACE bypasses the PR_SET_DUMPABLE access check this test needs")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), nonDumpableChildEnv+"=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start non-dumpable child: %v", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	}()
	pid := cmd.Process.Pid

	uid, err := RealUID(os.Getpid())
	if err != nil {
		t.Fatalf("RealUID(self): %v", err)
	}
	ns, err := SelfPIDNamespace()
	if err != nil {
		t.Fatalf("SelfPIDNamespace: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var evidence Evidence
	for time.Now().Before(deadline) {
		evidence = ReadEvidence(pid)
		if evidence.Unreadable {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !evidence.Unreadable {
		t.Fatalf("ReadEvidence(non-dumpable child) = %+v, want Unreadable once PR_SET_DUMPABLE takes effect", evidence)
	}
	if evidence.Gone {
		t.Fatalf("ReadEvidence(non-dumpable child) reported Gone, want a live Unreadable")
	}

	candidates, err := ScanCandidates(uid, ns, 0)
	if err != nil {
		t.Fatalf("ScanCandidates: %v", err)
	}
	if !containsPID(candidates, pid) {
		t.Fatalf("ScanCandidates = %v, want non-dumpable child %d included", candidates, pid)
	}
}

func TestReadEvidenceGoneForAReapedPID(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep binary on PATH")
	}
	cmd := exec.Command(sleep, "0.2")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait for child: %v", err)
	}

	evidence := ReadEvidence(pid)
	if !evidence.Gone {
		t.Fatalf("ReadEvidence(reaped pid) = %+v, want Gone", evidence)
	}
	if evidence.Unreadable {
		t.Fatalf("ReadEvidence(reaped pid) reported Unreadable, want Gone instead")
	}
}

func TestReadEvidenceOfSelf(t *testing.T) {
	evidence := ReadEvidence(os.Getpid())
	if evidence.Unreadable || evidence.Gone {
		t.Fatalf("ReadEvidence(self) = %+v, want neither Unreadable nor Gone", evidence)
	}
	if evidence.Cwd == "" {
		t.Fatalf("ReadEvidence(self).Cwd is empty")
	}
	if len(evidence.Environ) == 0 {
		t.Fatalf("ReadEvidence(self).Environ is empty")
	}
}

func containsPID(pids []int, want int) bool {
	for _, pid := range pids {
		if pid == want {
			return true
		}
	}
	return false
}
