package secondhand

import (
	"path/filepath"
	"testing"
)

func TestHomeUsesExplicitInfrastructureRoot(t *testing.T) {
	configured := filepath.Join(t.TempDir(), "Secondhand 測試")
	t.Setenv("SECONDHAND_HOME", configured)
	operatorHome := filepath.Join(t.TempDir(), "operator")
	t.Setenv("HOME", operatorHome)
	t.Setenv("USERPROFILE", operatorHome)

	got, err := Home()
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(configured)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(want) {
		t.Fatalf("Home() = %q, want %q", got, filepath.Clean(want))
	}
}

func TestHomeUsesCanonicalUserRootWithoutOverride(t *testing.T) {
	configured := filepath.Join(t.TempDir(), "operator")
	t.Setenv("SECONDHAND_HOME", "")
	t.Setenv("HOME", configured)
	t.Setenv("USERPROFILE", configured)

	got, err := Home()
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(filepath.Join(configured, ".secondhand"))
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(want) {
		t.Fatalf("Home() = %q, want %q", got, filepath.Clean(want))
	}
}
