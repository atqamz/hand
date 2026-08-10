package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func notifyMessageTemplate(marker string) string {
	if runtime.GOOS == "windows" {
		return `<nul set /p "=%HAND_MESSAGE%" > "` + marker + `"`
	}
	return "printf '%s' \"$HAND_MESSAGE\" > '" + marker + "'"
}

func TestNotifyWithoutConfigFailsRatherThanClaimingDelivery(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	var out bytes.Buffer
	cmd := newNotifyCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"hello world"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want a failure since nothing was delivered")
	}
	if !strings.Contains(err.Error(), "hello world") {
		t.Fatalf("error = %v, want it to name the undelivered message", err)
	}
}

func TestNotifySubstitutesMessageAndExecutesTemplate(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "marker.txt")
	template := notifyMessageTemplate(marker)
	if err := os.WriteFile(filepath.Join(home, "config", "notify"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := newNotifyCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"hello world"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "result: notified\nmessage: hello world\n" {
		t.Fatalf("stdout = %q", out.String())
	}

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello world" {
		t.Fatalf("marker content = %q, want %q", got, "hello world")
	}
}

func TestNotifyFailureIsVisibleRatherThanAWarningBehindExitZero(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "notify"), []byte("exit 1"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newNotifyCmd()
	cmd.SetOut(io.Discard)
	cmd.SetArgs([]string{"hello world"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("Execute() error = nil, want the template's failure surfaced")
	}
}
