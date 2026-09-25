//go:build linux

package osfacts

import (
	"os"
	"os/exec"
	"testing"
)

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

func TestObservePIDReuseImpersonationIsCeased(t *testing.T) {
	self, err := ReadIncarnation(os.Getpid())
	if err != nil {
		t.Fatalf("ReadIncarnation(self): %v", err)
	}
	impersonator := self
	impersonator.StartTicks++
	if got := Observe(impersonator); got != Ceased {
		t.Fatalf("Observe(right pid, wrong start) = %s, want ceased", got)
	}
}

func TestObserveReapedChildIsCeased(t *testing.T) {
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
	if got := Observe(child); got != Ceased {
		t.Fatalf("Observe(reaped child) = %s, want ceased", got)
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

func TestPIDNamespaceOfSelfIsStable(t *testing.T) {
	first, err := PIDNamespace(os.Getpid())
	if err != nil {
		t.Fatalf("PIDNamespace(self): %v", err)
	}
	second, err := PIDNamespace(os.Getpid())
	if err != nil {
		t.Fatalf("PIDNamespace(self) again: %v", err)
	}
	if first != second || first == 0 {
		t.Fatalf("PIDNamespace(self) = %d then %d, want a stable nonzero inode", first, second)
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
	ns, err := PIDNamespace(os.Getpid())
	if err != nil {
		t.Fatalf("PIDNamespace(self): %v", err)
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
	ns, err := PIDNamespace(os.Getpid())
	if err != nil {
		t.Fatalf("PIDNamespace(self): %v", err)
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

func TestReadEvidenceUnreadableForAGonePID(t *testing.T) {
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
	if !evidence.Unreadable {
		t.Fatalf("ReadEvidence(gone pid) = %+v, want Unreadable", evidence)
	}
}

func TestReadEvidenceOfSelf(t *testing.T) {
	evidence := ReadEvidence(os.Getpid())
	if evidence.Unreadable {
		t.Fatalf("ReadEvidence(self) reported Unreadable")
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
