package home

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeHome(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestIsHomeTrueWhenDataAndStateDirsExist(t *testing.T) {
	dir := t.TempDir()
	makeHome(t, dir)

	got, err := IsHome(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("got false, want true")
	}
}

func TestIsHomeFalseWhenEmpty(t *testing.T) {
	got, err := IsHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("got true, want false")
	}
}

func TestIsHomeFalseWhenOnlyDataDirExists(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := IsHome(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("got true, want false")
	}
}

func TestIsHomeFalseWhenDataIsAFileNotADir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := IsHome(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("got true, want false")
	}
}

func TestResolveReturnsCwdWhenItIsAHome(t *testing.T) {
	dir := t.TempDir()
	makeHome(t, dir)
	t.Chdir(dir)

	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("got %q, want %q", got, dir)
	}
}

func TestResolveWalksUpToAnAncestorHome(t *testing.T) {
	home := t.TempDir()
	makeHome(t, home)
	nested := filepath.Join(home, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got != home {
		t.Fatalf("got %q, want %q", got, home)
	}
}

func TestResolveFailsLoudlyWithNoHomeAnywhereInTheTree(t *testing.T) {
	t.Chdir(t.TempDir())

	_, err := Resolve()
	if err == nil {
		t.Fatal("got nil error, want failure")
	}
	want := "not inside a secondhand home; run `hand init` or set HAND_HOME"
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want it to wrap ErrNotFound", err)
	}
}

func TestResolvePrefersHandHomeOverCwd(t *testing.T) {
	cwdHome := t.TempDir()
	makeHome(t, cwdHome)
	t.Chdir(cwdHome)

	envHome := t.TempDir()
	makeHome(t, envHome)
	t.Setenv("HAND_HOME", envHome)

	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got != envHome {
		t.Fatalf("got %q, want %q", got, envHome)
	}
}

func TestResolveFailsLoudlyWhenHandHomeIsNotAHome(t *testing.T) {
	t.Chdir(t.TempDir())

	notAHome := t.TempDir()
	t.Setenv("HAND_HOME", notAHome)

	_, err := Resolve()
	if err == nil {
		t.Fatal("got nil error, want failure")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want it to wrap ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), notAHome) {
		t.Fatalf("got %q, want it to name HAND_HOME's value %q", err.Error(), notAHome)
	}
}
