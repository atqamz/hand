package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCodexDirSurvivesTheCmdShim(t *testing.T) {
	base := t.TempDir()
	out := filepath.Join(base, "args.txt")
	shim := filepath.Join(base, "codex.cmd")
	script := "@echo off\r\n:next\r\nif \"%~1\"==\"\" exit /b 0\r\n>>\"" + out + "\" echo %~1\r\nshift\r\ngoto next\r\n"
	if err := os.WriteFile(shim, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "my fleet")
	spec := Spec{"codex", "gpt-6-luna", "low"}
	for name, build := range map[string]func() ([]string, error){
		"supervisor": func() ([]string, error) { return SupervisorArgv(shim, spec, "", "go", dir, false) },
		"resume":     func() ([]string, error) { return SupervisorArgv(shim, spec, "abc", "", dir, true) },
		"worker":     func() ([]string, error) { return Argv(shim, spec, "go", dir) },
	} {
		_ = os.Remove(out)
		argv, err := build()
		if err != nil {
			t.Fatal(err)
		}
		if b, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", name, err, b)
		}
		got, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimRight(string(got), "\r\n"), "\r\n")
		if i := slices.Index(lines, "--cd"); i < 0 || i+1 == len(lines) || lines[i+1] != dir {
			t.Fatalf("%s: shim saw %q, want --cd followed by %q", name, lines, dir)
		}
	}
}
