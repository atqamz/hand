package secondhand

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/atqamz/hand/internal/testtag"
)

func TestMain(m *testing.M) {
	testtag.Main(m.Run)
}

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

// This suite only ever runs under the test build tag (TestMain above refuses otherwise), so this
// proves the enforced half of the contract: without SECONDHAND_HOME, Home() must refuse rather than
// silently resolve the operator's real infrastructure root. See atqamz/hand#413.
func TestHomeRefusesWithoutOverrideUnderTestBuild(t *testing.T) {
	operatorHome := filepath.Join(t.TempDir(), "operator")
	t.Setenv("SECONDHAND_HOME", "")
	t.Setenv("HOME", operatorHome)
	t.Setenv("USERPROFILE", operatorHome)

	_, err := Home()
	if !errors.Is(err, ErrHomeNotOverridden) {
		t.Fatalf("Home() error = %v, want ErrHomeNotOverridden", err)
	}
}
