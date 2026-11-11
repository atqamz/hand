package update

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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

func pinned(t *testing.T, f *fixture, name, version string) string {
	t.Helper()
	_, home := newFleet(t, f.root, name)
	if err := fleet.WriteVersion(home, fleet.VersionPin{Version: version, Schema: state.SchemaVersion}); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestRunRefusesATargetOlderThanAFleetsPin(t *testing.T) {
	for _, check := range []bool{false, true} {
		f := newFetch(t)
		home := pinned(t, f, "alpha", "0.10.0")
		f.o.Check = check
		_, err := Run(context.Background(), f.o)
		if err == nil || !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "0.10.0") || !strings.Contains(err.Error(), "stop the supervisor and board first") || !strings.Contains(err.Error(), "delete the fleet's hand.version") || !strings.Contains(err.Error(), "only if the older hand refuses the schema") || !strings.Contains(err.Error(), "`hand update --channel edge`") || !strings.Contains(err.Error(), home) {
			t.Fatalf("check=%v: err = %v", check, err)
		}
		f.unchanged(t)
		if got := f.log(t, changes...); len(got) != 0 {
			t.Fatalf("check=%v: calls = %q", check, got)
		}
	}
}

func TestRunNamesEveryFleetThatRefuses(t *testing.T) {
	f := newFetch(t)
	pinned(t, f, "alpha", "0.10.0")
	pinned(t, f, "beta", "0.8.0")
	pinned(t, f, "gamma", "0.11.0")
	_, err := Run(context.Background(), f.o)
	if err == nil || !strings.Contains(err.Error(), "alpha (hand 0.10.0") || !strings.Contains(err.Error(), "gamma (hand 0.11.0") || strings.Contains(err.Error(), "beta") {
		t.Fatalf("err = %v", err)
	}
	f.unchanged(t)
}

func TestRunNeverRefusesACurrentBuildForAPin(t *testing.T) {
	for _, check := range []bool{false, true} {
		f := newFetch(t)
		f.o.From = Build{Version: "0.9.0", Channel: "edge", Commit: "0123456789ab", Schema: state.SchemaVersion, Luvus: "0.14.4"}
		pinned(t, f, "alpha", "0.10.0")
		f.o.Check = check
		rep, err := Run(context.Background(), f.o)
		if err != nil || rep.Status != "up to date" && rep.Status != "checked" {
			t.Fatalf("check=%v: report = %+v, %v", check, rep, err)
		}
	}
}

func TestRunHintFollowsTheTargetsChannel(t *testing.T) {
	f := newFetch(t)
	pinned(t, f, "alpha", "0.10.0")
	_, err := Run(context.Background(), f.o)
	if err == nil || !strings.Contains(err.Error(), "No release of this channel is that new yet.") || strings.Contains(err.Error(), "Run `hand update --channel edge`") {
		t.Fatalf("edge target: err = %v", err)
	}
	g := newFetch(t)
	for name, body := range fakeHand(t, "0.9.0", "stable", "0123456789ab", "23", "0.14.4") {
		g.srv.set(name, body)
	}
	g.o.Channel = "stable"
	pinned(t, g, "alpha", "0.10.0")
	_, err = Run(context.Background(), g.o)
	if err == nil || !strings.Contains(err.Error(), "Run `hand update --channel edge` to get the newer hand.") {
		t.Fatalf("stable target: err = %v", err)
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

func TestRunSkipsInitInAFleetPinnedNewerAfterALuvusRepin(t *testing.T) {
	f := newCycle(t)
	f.current()
	f.pin = keepLuvus(t, f.root, "0.14.3")
	f.mu.Lock()
	f.version = "0.14.3"
	f.mu.Unlock()
	f.o.Start = func(context.Context, string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.version = "0.14.4"
		return nil
	}
	f.serverRuns(t)
	if err := fleet.WriteVersion(f.alpha, fleet.VersionPin{Version: "0.10.0", Schema: state.SchemaVersion}); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed || alphaLuvus(t, rep) != "switched" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	var init string
	for _, fr := range rep.Fleets {
		if fr.Name == "alpha" {
			init = fr.Init
		}
	}
	if init != "skipped: pinned to a newer hand" {
		t.Fatalf("alpha init = %q", init)
	}
	if !slices.ContainsFunc(rep.Help, func(h string) bool {
		return strings.Contains(h, f.alpha) && strings.Contains(h, "hand 0.10.0") && strings.Contains(h, "No release of this channel is that new yet.")
	}) {
		t.Fatalf("help = %q", rep.Help)
	}
}
