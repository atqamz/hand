//go:build e2e

package e2e

import (
	"database/sql"
	"net/url"
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

// #348 revision 4 C4-14: a Project imported through the production freeze path works in canonical v19.
func TestCutoverOfflineImportedProjectIsUsableAfterPublication(t *testing.T) {
	home := offlineCutoverHome(t)
	initGitRepo(t, filepath.Join(home, "projects", "alpha"))
	execFleetFixtureSQL(t, home, `INSERT INTO project(id,name,url,mode,position) VALUES('p_11111111111111111111111111111111','alpha','https://example.invalid/alpha.git','direct-pr',1)`)
	cutoverBootEnv(t, false)
	if got := runHand(t, home, "cutover", "offline", home); got.code != 0 || !strings.Contains(got.stdout, "disposition: frozen") {
		t.Fatalf("freeze run = %+v", got)
	}
	cutoverBootEnv(t, true)
	if got := runHand(t, home, "cutover", "offline", home); got.code != 0 || !strings.Contains(got.stdout, "disposition: canonical-authority") {
		t.Fatalf("completion = %+v", got)
	}
	ok := func(args ...string) invocation {
		t.Helper()
		got := runHand(t, home, args...)
		if got.code != 0 {
			t.Fatalf("%v on the published home: %+v", args, got)
		}
		return got
	}
	registered := ok("project", "register", "alpha")
	projectID := canonicalOutputField(t, registered, "project_id")
	workspaceID := canonicalOutputField(t, registered, "workspace_binding_id")
	if again := ok("project", "register", "alpha"); canonicalOutputField(t, again, "workspace_binding_id") != workspaceID {
		t.Fatalf("re-registration minted a new binding: %+v", again)
	}
	var imported, importedPolicy string
	db, err := sql.Open("sqlite", "file:"+(&url.URL{Path: filepath.ToSlash(store.Path(home))}).EscapedPath()+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	err = db.QueryRow(`SELECT workspace_binding_id, policy_revision_id FROM legacy_import_project WHERE project_id=?`, projectID).Scan(&imported, &importedPolicy)
	_ = db.Close()
	if err != nil || imported != workspaceID {
		t.Fatalf("registration returned binding %s, imported %s (%v)", workspaceID, imported, err)
	}
	ok("task", "create", "task-1", "--project-id", projectID, "--goal", "use the imported Project")
	ok("plan", "create", "plan-1", "--task-id", "task-1", "--workspace-binding-id", workspaceID,
		"--policy-revision-id", importedPolicy, "--intent", "execute", "--judgment", "bounded",
		"--basis", "imported repository", "--brief", "verify the imported binding")
}

// A legacy home whose Herdr and Treehouse report nothing: no Fleet resource runs, before or after the restart.
func offlineCutoverHome(t *testing.T) string {
	t.Helper()
	dir := binDir(t)
	writeFakeDispatch(t, dir, "herdr", "", "$1 $2", `  "session list") echo '{"sessions":[]}' ;;`)
	writeFakeDispatch(t, dir, "treehouse", "", "$1", `  status) echo '[]' ;;`)
	home := t.TempDir()
	createCutoverLegacyFixture(t, home)
	return home
}
