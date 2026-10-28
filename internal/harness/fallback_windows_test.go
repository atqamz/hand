package harness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindRunsTheNpmBatchFileForItsPrefix(t *testing.T) {
	p := newPlaces(t)
	delete(p.env, "NPM_CONFIG_PREFIX")
	if err := os.WriteFile(filepath.Join(p.path, "npm.cmd"), []byte("@echo "+p.prefix+"\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := exeFile(t, p.npm(), "copilot")
	if got, err := p.find("copilot"); err != nil || got != want {
		t.Fatalf("found %s, %v; want %s", got, err, want)
	}
}

func TestFindPrefersAGhExeToACopilotBatchFileOnPath(t *testing.T) {
	p := newPlaces(t)
	if err := os.WriteFile(filepath.Join(p.path, "copilot.cmd"), []byte("@exit 0\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := exeFile(t, p.gh(), "copilot")
	if got, err := p.find("copilot"); err != nil || got != want {
		t.Fatalf("found %s, %v; want %s", got, err, want)
	}
}
