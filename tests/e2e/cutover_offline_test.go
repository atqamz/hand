//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/store"
)

// #348 revision 4 "Offline sequence": freeze, refuse in the same boot session, complete after a restart.
func TestCutoverOfflineFreezesThenCompletesAfterRestart(t *testing.T) {
	home := offlineCutoverHome(t)
	fleetID, err := store.FleetIDReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	cutoverBootEnv(t, false)
	got := runHand(t, home, "cutover", "offline", home)
	if got.code != 0 || !strings.Contains(got.stdout, "disposition: frozen") {
		t.Fatalf("freeze run = %+v", got)
	}
	status := runHand(t, home, "status")
	if status.code == 0 || !strings.Contains(status.stderr, "pending v19 cutover") {
		t.Fatalf("legacy command on a frozen home = %+v, want the cutover-pending refusal", status)
	}
	before := snapshotTree(t, home)
	got = runHand(t, home, "cutover", "offline", home)
	if got.code == 0 || !strings.Contains(got.stderr, "recovery disposition=reboot-required") {
		t.Fatalf("completion in the freeze's boot session = %+v", got)
	}
	assertTreeUnchanged(t, home, before)

	cutoverBootEnv(t, true)
	got = runHand(t, home, "cutover", "offline", home)
	if got.code != 0 || !strings.Contains(got.stdout, "disposition: canonical-authority") {
		t.Fatalf("completion after a restart = %+v", got)
	}
	if got, err := store.FleetIDReadOnly(home); err != nil || got != fleetID {
		t.Fatalf("published Fleet identity = %q, %v; want %q", got, err, fleetID)
	}
}

// #348 revision 4 "Abort": drift after the restart refuses, and abort restores the exact pre-freeze DB.
func TestCutoverOfflineDriftAbortsToLegacy(t *testing.T) {
	home := offlineCutoverHome(t)
	original, err := os.ReadFile(store.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	cutoverBootEnv(t, false)
	if got := runHand(t, home, "cutover", "offline", home); got.code != 0 {
		t.Fatalf("freeze run = %+v", got)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "added-after-freeze"), 0o755); err != nil {
		t.Fatal(err)
	}
	cutoverBootEnv(t, true)
	before := snapshotTree(t, home)
	got := runHand(t, home, "cutover", "offline", home)
	if got.code == 0 || !strings.Contains(got.stderr, "recovery disposition=drift") || !strings.Contains(got.stderr, "project-orphan-path") {
		t.Fatalf("completion after drift = %+v", got)
	}
	assertTreeUnchanged(t, home, before)

	got = runHand(t, home, "cutover", "offline", "--abort", home)
	if got.code != 0 || !strings.Contains(got.stdout, "disposition: aborted") {
		t.Fatalf("abort = %+v", got)
	}
	if restored, err := os.ReadFile(store.Path(home)); err != nil || string(restored) != string(original) {
		t.Fatalf("aborted home does not hold the pre-freeze bytes: %v", err)
	}
	if got := runHand(t, home, "cutover", "inspect", home); got.code != 0 || !strings.Contains(got.stdout, "disposition: legacy-source") {
		t.Fatalf("inspect after abort = %+v", got)
	}
}

// #348 revision 4 C4-13: an unobservable subject is retryable and completes once it can be observed.
func TestCutoverOfflineRetriesObservationUnknown(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions that bind the test user")
	}
	home := offlineCutoverHome(t)
	projects := filepath.Join(home, "projects")
	if err := os.Mkdir(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	cutoverBootEnv(t, false)
	if got := runHand(t, home, "cutover", "offline", home); got.code != 0 {
		t.Fatalf("freeze run = %+v", got)
	}
	cutoverBootEnv(t, true)
	if err := os.Chmod(projects, 0); err != nil {
		t.Fatal(err)
	}
	got := runHand(t, home, "cutover", "offline", home)
	if err := os.Chmod(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	if got.code == 0 || !strings.Contains(got.stderr, "recovery disposition=observation-unknown") {
		t.Fatalf("completion with an unreadable projects directory = %+v", got)
	}
	got = runHand(t, home, "cutover", "offline", home)
	if got.code != 0 || !strings.Contains(got.stdout, "disposition: canonical-authority") {
		t.Fatalf("retried completion = %+v", got)
	}
}

// A legacy home whose Herdr reports no session: nothing of this Fleet runs in Herdr, before or after the restart.
func offlineCutoverHome(t *testing.T) string {
	t.Helper()
	writeFakeDispatch(t, binDir(t), "herdr", "", "$1 $2", `  "session list") echo '{"sessions":[]}' ;;`)
	home := t.TempDir()
	createCutoverLegacyFixture(t, home)
	return home
}
