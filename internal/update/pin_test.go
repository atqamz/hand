package update

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/state"
)

func TestPinNewer(t *testing.T) {
	for _, c := range []struct {
		name    string
		pin     fleet.VersionPin
		version string
		schema  int
		want    bool
	}{
		{"same", fleet.VersionPin{Version: "0.13.0", Schema: 22}, "0.13.0", 22, false},
		{"newer version", fleet.VersionPin{Version: "0.14.0", Schema: 22}, "0.13.0", 22, true},
		{"older version", fleet.VersionPin{Version: "0.12.0", Schema: 22}, "0.13.0", 22, false},
		{"newer schema, older version", fleet.VersionPin{Version: "0.12.0", Schema: 23}, "0.13.0", 22, true},
		{"older schema, newer version", fleet.VersionPin{Version: "0.14.0", Schema: 21}, "0.13.0", 22, true},
		{"older schema, same version", fleet.VersionPin{Version: "0.13.0", Schema: 21}, "0.13.0", 22, false},
		{"pre-release pin, same schema", fleet.VersionPin{Version: "99.0.0-rc1", Schema: 22}, "0.13.0", 22, false},
		{"pre-release pin, newer schema", fleet.VersionPin{Version: "99.0.0-rc1", Schema: 23}, "0.13.0", 22, true},
		{"unparseable self, same schema", fleet.VersionPin{Version: "0.13.0", Schema: 22}, "dev", 22, false},
		{"unparseable self, e2e", fleet.VersionPin{Version: "9.9.9", Schema: 22}, "0.0.0-e2e", 22, false},
		{"unparseable self, newer schema", fleet.VersionPin{Version: "0.13.0", Schema: 23}, "0.13.0-rc1", 22, true},
		{"both unparseable", fleet.VersionPin{Version: "dev", Schema: 22}, "0.0.0-e2e", 22, false},
		{"no schema in the pin", fleet.VersionPin{Version: "99.0.0"}, "0.13.0", 22, true},
		{"commit alone", fleet.VersionPin{Version: "0.13.0", Commit: "zzzzzzz", Schema: 22}, "0.13.0", 22, false},
	} {
		if got := PinNewer(c.pin, c.version, c.schema); got != c.want {
			t.Errorf("%s: PinNewer = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRunRefusesATargetOlderThanAFleetsPin(t *testing.T) {
	for _, check := range []bool{false, true} {
		f := newFetch(t)
		_, home := newFleet(t, f.root, "alpha")
		if err := fleet.WriteVersion(home, fleet.VersionPin{Version: "0.10.0", Schema: state.SchemaVersion}); err != nil {
			t.Fatal(err)
		}
		f.o.Check = check
		_, err := Run(context.Background(), f.o)
		if err == nil || !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "0.10.0") || !strings.Contains(err.Error(), "`hand update --channel edge`") {
			t.Fatalf("check=%v: err = %v", check, err)
		}
		f.unchanged(t)
		if got := f.log(t, changes...); len(got) != 0 {
			t.Fatalf("check=%v: calls = %q", check, got)
		}
	}
}

func TestRunIgnoresAnOlderOrUnreadablePin(t *testing.T) {
	f := newFetch(t)
	_, older := newFleet(t, f.root, "alpha")
	if err := fleet.WriteVersion(older, fleet.VersionPin{Version: "0.8.0", Schema: state.SchemaVersion}); err != nil {
		t.Fatal(err)
	}
	_, broken := newFleet(t, f.root, "beta")
	if err := writeFile(filepath.Join(broken, fleet.VersionFile), `{"version":"99.0.0","sch`); err != nil {
		t.Fatal(err)
	}
	f.o.Check = true
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
}

func writeFile(path, body string) error { return os.WriteFile(path, []byte(body), 0o600) }
