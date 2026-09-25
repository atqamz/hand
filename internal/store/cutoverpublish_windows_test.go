package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// #348 revision 4 "Publication without an absent active DB": a held handle stops the replace and leaves the bridge.
func TestReplaceLegacyV18CutoverDurableReportsHeldTargetAsBusy(t *testing.T) {
	dir := t.TempDir()
	source, target := filepath.Join(dir, "canonical.tmp"), filepath.Join(dir, "hand.db")
	for path, body := range map[string]string{source: "canonical", target: "bridge"} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	held, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	if err := replaceLegacyV18CutoverDurable(source, target); !errors.Is(err, errLegacyV18CutoverReplaceBusy) {
		t.Fatalf("replace over a held target = %v, want busy", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "bridge" {
		t.Fatalf("held target after refused replace = %q, %v", data, err)
	}
}
