package fakebin_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
)

func TestMain(m *testing.M) {
	fakebin.Main(map[string]func([]string) int{
		"echo": func(args []string) int {
			fmt.Println(fakebin.Params()["word"], strings.Join(args, " "))
			return 3
		},
	})
	os.Exit(m.Run())
}

func TestFakebinSidecar(t *testing.T) {
	dir := t.TempDir()
	path := fakebin.Install(t, dir, "fake", "echo", map[string]string{"word": "hi"})
	want := filepath.Join(dir, "fake")
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	out, err := exec.Command(path, "a", "b").Output()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 || string(out) != "hi a b\n" {
		t.Fatalf("with a sidecar = %q, %v", out, err)
	}
	if err := os.Remove(path + ".fake"); err != nil {
		t.Fatal(err)
	}
	out, err = exec.Command(path, "-test.run=^$").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "PASS") {
		t.Fatalf("without a sidecar = %q, %v", out, err)
	}
}

func TestFakebinEmbedSurvivesACopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copy")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	if err := os.WriteFile(path, fakebin.Embed(t, "echo", map[string]string{"word": "there"}), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(path, "c").Output()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 || string(out) != "there c\n" {
		t.Fatalf("embedded = %q, %v", out, err)
	}
}
