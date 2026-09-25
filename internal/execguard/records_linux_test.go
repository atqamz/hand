//go:build linux

package execguard

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/atqamz/hand/internal/osfacts"
)

func TestUnknownRecordVersionIsRefusedAndATornRecordIsAbsent(t *testing.T) {
	dir := t.TempDir()
	for _, test := range []struct {
		name    string
		content string
		want    error
	}{
		{"unknown version", `{"protocol":"hand-exec-guard:v9","kind":"ceased"}`, ErrUnknownProtocol},
		{"torn", `{"protocol":"hand-exec-guard:v1","kind":"cea`, fs.ErrNotExist},
		{"misfiled", `{"protocol":"hand-exec-guard:v1","kind":"running"}`, fs.ErrNotExist},
	} {
		if err := os.WriteFile(filepath.Join(dir, KindCeased), []byte(test.content), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := ReadRecord(dir, KindCeased)
		if !errors.Is(err, test.want) || (test.want == ErrUnknownProtocol) == errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: ReadRecord = %v, want %v (EG-17)", test.name, err, test.want)
		}
	}
}

func TestTerminalKindFollowsTheCessationCause(t *testing.T) {
	exit := func(code, signal int) *Exit { return &Exit{Code: code, Signal: signal} }
	for _, test := range []struct {
		name    string
		ceased  Record
		pending string
		want    string
	}{
		{"exact pending interrupt", Record{Cause: CauseInterruptRequest, InterruptOperationID: "op_i", Exit: exit(143, 15)}, "op_i", "interrupted"},
		{"forged request naming no pending interrupt", Record{Cause: CauseInterruptRequest, InterruptOperationID: "op_forged", Exit: exit(143, 15)}, "", "failed"},
		{"request naming another interrupt", Record{Cause: CauseInterruptRequest, InterruptOperationID: "op_forged", Exit: exit(143, 15)}, "op_i", "failed"},
		{"hangup", Record{Cause: CauseHangup, Exit: exit(143, 15)}, "op_i", "provider-gone"},
		{"clean harness exit", Record{Cause: CauseHarnessExit, Exit: exit(0, 0)}, "op_i", "completed"},
		{"nonzero harness exit", Record{Cause: CauseHarnessExit, Exit: exit(1, 0)}, "", "failed"},
		{"harness killed by a signal", Record{Cause: CauseHarnessExit, Exit: exit(137, 9)}, "", "failed"},
		{"external termination", Record{Cause: CauseExternalTermination, Exit: exit(143, 15)}, "", "failed"},
	} {
		if got := TerminalKind(test.ceased, test.pending); got != test.want {
			t.Errorf("%s: TerminalKind = %s, want %s (EG-9)", test.name, got, test.want)
		}
	}
}

func TestSweepReadsParentAndStartTimePastTheLastParen(t *testing.T) {
	self, err := osfacts.ReadIncarnation(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	stat, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Fatal(err)
	}
	open, end := bytes.IndexByte(stat, '('), bytes.LastIndexByte(stat, ')')
	hostile := append(append(append([]byte{}, stat[:open+1]...), "x) S 1 2 (y z) 9"...), stat[end:]...)
	parsed, err := parseStat(hostile)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.startTicks != self.StartTicks || parsed.ppid != os.Getppid() {
		t.Fatalf("parsed start %d parent %d behind a hostile command, want %d and %d", parsed.startTicks, parsed.ppid, self.StartTicks, os.Getppid())
	}
}
