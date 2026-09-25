//go:build test

package runtime

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	gitrepo "github.com/atqamz/hand/internal/git"
	"github.com/atqamz/hand/internal/store"
)

const legacyV18CutoverWaiterRoleEnv = "HAND_TEST_CUTOVER_WAITER_HOME"

// #348 revision 4 C4-2: the reproduced #611 waiter reads the DB, waits on its project lock across the
// freeze, then fast-forwards the clone. The same boot session refuses reboot-required; after a restart the move is drift.
func TestLegacyV18CutoverPreReadLockWaiterIsCaughtByWitnessAndDrift(t *testing.T) {
	if home := os.Getenv(legacyV18CutoverWaiterRoleEnv); home != "" {
		runLegacyV18CutoverPreReadLockWaiter(t, home)
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("the waiter commits through a POSIX git fixture")
	}
	home := t.TempDir()
	clone := filepath.Join(home, "projects", "alpha")
	gitLegacyV18CutoverFixture(t, clone, "init", "-q")
	gitLegacyV18CutoverFixture(t, clone, "commit", "-q", "--allow-empty", "-m", "before freeze")
	createLegacyV18CutoverProjectSource(t, home)
	legacyV18CutoverBootEnv(t, "11111111-2222-4333-8444-555555555555")

	child := exec.Command(os.Args[0], "-test.run=^TestLegacyV18CutoverPreReadLockWaiterIsCaughtByWitnessAndDrift$")
	child.Env = append(os.Environ(), legacyV18CutoverWaiterRoleEnv+"="+home)
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	lines := bufio.NewScanner(stdout)
	expectLine := func(want string) {
		t.Helper()
		if !lines.Scan() || lines.Text() != want {
			t.Fatalf("waiter line = %q, want %q; stderr=%s", lines.Text(), want, stderr.String())
		}
	}

	expectLine("read")
	initialHead, err := gitrepo.HeadCommit(clone)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := store.AcquireLegacyV18CutoverGuardFixture(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(stdin, "continue\n"); err != nil {
		t.Fatal(err)
	}
	expectLine("locking")
	frozenHead, err := gitrepo.HeadCommit(clone)
	if err != nil || frozenHead != initialHead {
		t.Fatalf("clone moved while the freeze held the project lock: %s, want %s (%v)", frozenHead, initialHead, err)
	}
	if err := guard.Freeze(context.Background(), home, legacyV18CutoverWaiterManifestInput(t, home, guard, frozenHead)); err != nil {
		t.Fatal(err)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
	expectLine("effect")
	if err := child.Wait(); err != nil {
		t.Fatalf("waiter: %v; stderr=%s", err, stderr.String())
	}
	if head, err := gitrepo.HeadCommit(clone); err != nil || head == frozenHead {
		t.Fatalf("waiter did not move the clone after the freeze: %s, %v", head, err)
	}

	bridge, err := os.ReadFile(store.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecoverCanonicalV19Cutover(home, LegacyV18CutoverDriftGate); err == nil || !strings.Contains(err.Error(), "recovery disposition=reboot-required") {
		t.Fatalf("completion in the freeze's boot session = %v, want reboot-required", err)
	}
	legacyV18CutoverBootEnv(t, "66666666-7777-4888-9999-aaaaaaaaaaaa")
	if _, err := store.RecoverCanonicalV19Cutover(home, LegacyV18CutoverDriftGate); err == nil || !strings.Contains(err.Error(), "recovery disposition=drift") || !strings.Contains(err.Error(), "project-head-changed") {
		t.Fatalf("completion after a restart = %v, want drift on the moved HEAD", err)
	}
	if after, err := os.ReadFile(store.Path(home)); err != nil || string(after) != string(bridge) {
		t.Fatalf("refused completions changed the frozen bridge: %v", err)
	}
}

func runLegacyV18CutoverPreReadLockWaiter(t *testing.T, home string) {
	db, err := store.OpenReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	fmt.Println("read")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	fmt.Println("locking")
	release, err := store.Lock(home, "project:alpha", false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	gitLegacyV18CutoverFixture(t, filepath.Join(home, "projects", "alpha"), "commit", "-q", "--allow-empty", "-m", "after freeze")
	fmt.Println("effect")
}

func createLegacyV18CutoverProjectSource(t *testing.T, home string) {
	t.Helper()
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+(&url.URL{Path: store.Path(home)}).EscapedPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	for _, statement := range []string{
		`INSERT INTO project(id,name,url,mode,position) VALUES('p_11111111111111111111111111111111','alpha','https://example.invalid/alpha.git','direct-pr',1)`,
		`PRAGMA journal_mode=DELETE`,
	} {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func legacyV18CutoverWaiterManifestInput(t *testing.T, home string, guard *store.LegacyV18CutoverGuard, revision string) store.LegacyV18CutoverManifestInput {
	t.Helper()
	plan, err := guard.ObservationPlan()
	if err != nil || len(plan.Projects) != 1 {
		t.Fatalf("observation plan = %#v, %v", plan, err)
	}
	project := plan.Projects[0]
	identity := func(path string) string {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		id, err := legacyV18CutoverPhysicalIdentity(path, info)
		if err != nil {
			t.Skipf("restart-stable identity unavailable here: %v", err)
		}
		return id
	}
	return store.LegacyV18CutoverManifestInput{
		FleetID:    plan.FleetID,
		ImportedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Projects: []store.LegacyV18CutoverManifestProjectInput{{
			SourceProjectID:      project.ProjectID,
			Locator:              "projects/alpha",
			RepositoryPhysicalID: identity(project.ClonePath),
			CommonDirPhysicalID:  identity(filepath.Join(project.ClonePath, ".git")),
			Revision:             revision,
			LegacyName:           project.Name,
			LegacyURL:            project.URL,
			LegacyMode:           project.Mode,
			LegacyUpstream:       project.Upstream,
		}},
	}
}

func legacyV18CutoverBootEnv(t *testing.T, token string) {
	t.Helper()
	t.Setenv("HAND_TEST_CUTOVER_FILESYSTEM", "local")
	t.Setenv("HAND_TEST_CUTOVER_BOOT_TOKEN", token)
	machine := "abcdefab-cdef-4abc-8def-abcdefabcdef"
	if runtime.GOOS == "linux" {
		machine = "0123456789abcdef0123456789abcdef"
	}
	t.Setenv("HAND_TEST_CUTOVER_MACHINE_ID", machine)
}

func gitLegacyV18CutoverFixture(t *testing.T, dir string, args ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", append([]string{"-c", "user.name=hand-test", "-c", "user.email=hand-test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
