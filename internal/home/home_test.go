package home

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Builds a home carrying the marker IsHome checks, state/hand.db.
func makeHome(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state", "hand.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Builds what an unrelated project clone can plausibly have at its top level,
// which must not be mistaken for a fleet home.
func makeGenericDataAndState(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Builds a home initialized before state/hand.db existed: the data/projects.md plus
// state/ marker a pre-sqlite hand init wrote, and the one migrateLegacy needs
// recognized as a home before it can ever run.
func makeLegacyHome(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "projects.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestIsHomeTrueWhenHandDbExists(t *testing.T) {
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

func TestIsHomeFalseForAProjectCloneWithGenericDataAndStateDirs(t *testing.T) {
	dir := t.TempDir()
	makeGenericDataAndState(t, dir)

	got, err := IsHome(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("got true, want false without state/hand.db")
	}
}

func TestIsHomeTrueForALegacyHomeWithoutHandDb(t *testing.T) {
	dir := t.TempDir()
	makeLegacyHome(t, dir)

	got, err := IsHome(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("got false, want true")
	}
}

func TestIsHomeFalseWhenProjectsFileIsADirNotAFile(t *testing.T) {
	dir := t.TempDir()
	makeLegacyHome(t, dir)
	projectsPath := filepath.Join(dir, "data", "projects.md")
	if err := os.Remove(projectsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(projectsPath, 0o755); err != nil {
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

// A plain file named data must read as a clean no-match, not a stat error that
// aborts the whole ancestor walk and fails every command.
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

func TestIsHomeFalseWhenStateIsAFileNotADir(t *testing.T) {
	dir := t.TempDir()
	makeHome(t, dir)
	if err := os.RemoveAll(filepath.Join(dir, "state")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state"), []byte("x"), 0o644); err != nil {
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
	t.Setenv("HAND_HOME", "")
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

func TestResolveReturnsCwdWhenItIsALegacyHome(t *testing.T) {
	t.Setenv("HAND_HOME", "")
	dir := t.TempDir()
	makeLegacyHome(t, dir)
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
	t.Setenv("HAND_HOME", "")
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

// The walk-up's primary path runs through projects/<name>, so a clone with its
// own top-level data/ and state/ must not stop it short of the real home.
func TestResolveWalksPastAProjectCloneWithGenericDataAndStateDirs(t *testing.T) {
	t.Setenv("HAND_HOME", "")
	home := t.TempDir()
	makeHome(t, home)
	clone := filepath.Join(home, "projects", "myapp")
	makeGenericDataAndState(t, clone)
	t.Chdir(clone)

	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got != home {
		t.Fatalf("got %q, want %q", got, home)
	}
}

func TestResolveFailsLoudlyWithNoHomeAnywhereInTheTree(t *testing.T) {
	t.Setenv("HAND_HOME", "")
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

// A relative HAND_HOME names one fleet home, not a different one per working directory:
// commands join paths onto the resolved home and hand them to subprocesses running
// elsewhere (hand spawn's brief path, which the harness reads inside the worktree).
func TestResolveAbsolutizesARelativeHandHome(t *testing.T) {
	parent := t.TempDir()
	home := filepath.Join(parent, "fleet")
	makeHome(t, home)
	t.Chdir(parent)
	t.Setenv("HAND_HOME", "fleet")

	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got != home {
		t.Fatalf("got %q, want %q", got, home)
	}
}

// A misconfigured HAND_HOME reports its own sentinel, not ErrNotFound: the two
// have different remedies, and callers that quietly tolerate "no home here"
// (hand update's AGENTS.md refresh) must still surface this one.
func TestResolveFailsLoudlyWhenHandHomeIsNotAHome(t *testing.T) {
	cwdHome := t.TempDir()
	makeHome(t, cwdHome)
	t.Chdir(cwdHome)

	notAHome := t.TempDir()
	t.Setenv("HAND_HOME", notAHome)

	_, err := Resolve()
	if err == nil {
		t.Fatal("got nil error, want failure")
	}
	if !errors.Is(err, ErrHandHomeInvalid) {
		t.Fatalf("got %v, want it to wrap ErrHandHomeInvalid", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want it not to wrap ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), notAHome) {
		t.Fatalf("got %q, want it to name HAND_HOME's value %q", err.Error(), notAHome)
	}
	if strings.Contains(err.Error(), ErrNotFound.Error()) {
		t.Fatalf("got %q, want it not to prescribe setting the variable that is already wrong", err.Error())
	}
}
