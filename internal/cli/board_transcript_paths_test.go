package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
)

func TestTranscriptPathsFindOpencodeInMiseShims(t *testing.T) {
	t.Parallel()
	data := t.TempDir()
	name := "opencode"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	shim := filepath.Join(data, "mise", "shims", name)
	if err := os.MkdirAll(filepath.Dir(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fakebin.WriteExecutable(shim, []byte("#!/bin/sh\n")); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": t.TempDir(), "XDG_DATA_HOME": data, "PATH": t.TempDir()}
	r := &runner{env: Env{Getenv: func(k string) string { return env[k] }}, home: t.TempDir()}
	if got := r.transcriptPaths().Opencode; got != shim {
		t.Fatalf("Opencode = %q, want %q", got, shim)
	}
}
