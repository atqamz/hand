package faketool

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCommandPreservesProcessBoundaries(t *testing.T) {
	bin := Bin(t)
	log := t.TempDir() + "/invocations.log"
	Command{
		Name:   "argv-probe",
		Args:   true,
		Stderr: "warning with spaces and apostrophe\n",
		Exit:   7,
		Log:    log,
	}.Install(t, bin)

	cmd := exec.Command("argv-probe", "one two", "it's", "back`tick")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("run error = %v, want exit 7", err)
	}
	if stdout.String() != "3\none two\nit's\nback`tick\n" {
		t.Fatalf("stdout = %q, want exact argv", stdout.String())
	}
	if stderr.String() != "warning with spaces and apostrophe\n" {
		t.Fatalf("stderr = %q, want configured stderr", stderr.String())
	}

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "argv-probe one two it's back`tick\n" {
		t.Fatalf("log = %q, want one invocation with preserved arguments", data)
	}
}

func TestCommandCanShareABinAndRejectUnexpectedInvocations(t *testing.T) {
	bin := Bin(t)
	Command{Name: "first", Stdout: "one\n"}.Install(t, bin)
	Command{Name: "second", Stdout: "two\n"}.Install(t, bin)

	for name, want := range map[string]string{"first": "one\n", "second": "two\n"} {
		out, err := exec.Command(name).Output()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(out) != want {
			t.Fatalf("%s stdout = %q, want %q", name, out, want)
		}
	}

	command := exec.Command("first", "unexpected")
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Run(); err == nil {
		t.Fatal("unexpected invocation succeeded")
	}
	if !strings.Contains(stderr.String(), "unexpected first invocation") {
		t.Fatalf("stderr = %q, want an explicit unexpected-invocation failure", stderr.String())
	}
}
