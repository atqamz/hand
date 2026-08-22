package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestRuntimeStatusIsMachineContextAndActionable(t *testing.T) {
	t.Setenv("SECONDHAND_HOME", t.TempDir())
	command := newRuntimeCmd()
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs([]string{"status"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ready: false", "hand runtime ensure"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("runtime status = %q, want %q", stdout.String(), want)
		}
	}
}
