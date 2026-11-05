package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/sidecar"
	"github.com/atqamz/hand/internal/update"
)

func TestDoctorIrohRowFollowsWhatIsInstalledAndWhatTheBoardSays(t *testing.T) {
	for _, c := range []struct {
		name, level, fix string
		id               string
		set              func(t *testing.T, fx *doctorFixture, root string)
	}{
		{"not installed and off", "info", "not installed; phone access over iroh needs hand-iroh", "", func(*testing.T, *doctorFixture, string) {}},
		{"not installed but switched on", "warn", "hand-iroh not found: it is not next to hand", "", func(t *testing.T, fx *doctorFixture, root string) { recordIrohState(t, root, "stopped") }},
		{"another program under the name", "warn", "not a hand-iroh binary", "", func(t *testing.T, fx *doctorFixture, root string) {
			installSidecar(t, fx, map[string]string{"version": "luvus 0.14.3", "raw": "1"})
		}},
		{"another version", "warn", "hand-iroh 9.9.9 does not match the " + sidecar.Version + " this hand needs; run `hand update`", "", func(t *testing.T, fx *doctorFixture, root string) {
			installSidecar(t, fx, map[string]string{"version": "9.9.9"})
		}},
		{"installed and off", "info", "installed, and off; turn it on by starting the board with `hand board --iroh`", "", func(t *testing.T, fx *doctorFixture, root string) { installSidecar(t, fx, nil) }},
		{"on, board not running", "info", "on, but no board is running", "", func(t *testing.T, fx *doctorFixture, root string) {
			installSidecar(t, fx, nil)
			recordIrohState(t, root, "ready "+irohID+" "+relayURL)
		}},
		{"running", "ok", `""`, irohID, func(t *testing.T, fx *doctorFixture, root string) {
			installSidecar(t, fx, nil)
			recordIrohState(t, root, "ready "+irohID+" "+relayURL)
			liveBoard(t, root)
		}},
		{"ready without a relay", "warn", "reached no relay within 10 seconds, so a phone elsewhere cannot dial it", irohID, func(t *testing.T, fx *doctorFixture, root string) {
			installSidecar(t, fx, nil)
			recordIrohState(t, root, "ready "+irohID+" none")
			liveBoard(t, root)
		}},
		{"failed", "warn", "gave up after 5 quick exits: exit status 1: boom; restart the board to try again", "", func(t *testing.T, fx *doctorFixture, root string) {
			installSidecar(t, fx, nil)
			recordIrohState(t, root, "failed gave up after 5 quick exits: exit status 1: boom")
			liveBoard(t, root)
		}},
		{"starting", "info", "starting; check again in a few seconds", "", func(t *testing.T, fx *doctorFixture, root string) {
			installSidecar(t, fx, nil)
			recordIrohState(t, root, "starting")
			liveBoard(t, root)
		}},
		{"a stale ready line of a board that is gone", "info", "on, but no board is running", "", func(t *testing.T, fx *doctorFixture, root string) {
			installSidecar(t, fx, nil)
			recordIrohState(t, root, "ready "+irohID+" "+relayURL)
			if err := os.WriteFile(filepath.Join(root, "board.pid"), []byte("999999 stale "+os.Args[0]+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fx := newDoctorFixture(t)
			holdWatchLock(t, fx.h.home)
			root := fx.h.vars["SECONDHAND_HOME"]
			c.set(t, fx, root)
			out := fx.h.ok("doctor")
			row := checkRows(t, out)["iroh"]
			if row[0] != c.level || !strings.Contains(row[1], c.fix) {
				t.Fatalf("iroh = %q, want %s with %q in:\n%s", row, c.level, c.fix, out)
			}
			if got := field(out, "iroh_id"); got != c.id {
				t.Fatalf("iroh_id = %q, want %q", got, c.id)
			}
			if installed := strings.HasPrefix(field(out, "iroh"), sidecar.Version+" ") || strings.HasPrefix(field(out, "iroh"), "9.9.9 "); installed != (field(out, "iroh") != "") {
				t.Fatalf("iroh field = %q", field(out, "iroh"))
			}
		})
	}
}

func TestDoctorIrohNeverFailsTheChecks(t *testing.T) {
	fx := newDoctorFixture(t)
	holdWatchLock(t, fx.h.home)
	installSidecar(t, fx, map[string]string{"version": "9.9.9"})
	if _, _, code := fx.h.run("doctor"); code != 0 {
		t.Fatalf("doctor exits %d for a sidecar of the wrong version", code)
	}
}

func TestDoctorNeverShowsAnIdForASidecarThatIsNotReady(t *testing.T) {
	fx := newDoctorFixture(t)
	holdWatchLock(t, fx.h.home)
	root := fx.h.vars["SECONDHAND_HOME"]
	installSidecar(t, fx, nil)
	recordIrohState(t, root, "failed boom "+irohID)
	liveBoard(t, root)
	if out := fx.h.ok("doctor"); field(out, "iroh_id") != "" {
		t.Fatalf("doctor shows an ID for a failed sidecar:\n%s", out)
	}
}

func installSidecar(t *testing.T, fx *doctorFixture, params map[string]string) {
	t.Helper()
	p := map[string]string{"log": filepath.Join(t.TempDir(), "log"), "version": sidecar.Version}
	for k, v := range params {
		p[k] = v
	}
	fakebin.Install(t, fx.tools, "hand-iroh", "sidecar", p)
}

func recordIrohState(t *testing.T, root, line string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "board.iroh"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func liveBoard(t *testing.T, root string) {
	t.Helper()
	forget, err := update.WritePID(filepath.Join(root, "board.pid"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(forget)
}
