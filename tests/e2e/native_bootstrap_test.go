//go:build nativebootstrap && !e2e && !test

package e2e

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/store"
	"github.com/atqamz/hand/internal/toolchain"
)

// This opt-in native test builds an untagged CLI and downloads its exact locked
// runtime. It qualifies only Fleet/Project/Task creation, never worker execution.
func TestNativeCanonicalBootstrap(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "hand")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-tags=", "-o", binary, ".")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build untagged Hand: %v\n%s", err, out)
	}
	bytes, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("production binary SHA-256: %x", sha256.Sum256(bytes))
	if info, err := exec.Command("go", "version", "-m", binary).CombinedOutput(); err != nil {
		t.Fatal(err)
	} else {
		t.Logf("production build identity:\n%s", info)
	}
	for _, key := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "SECONDHAND_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		path := filepath.Join(root, strings.ToLower(key))
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, path)
	}
	for _, key := range []string{"HAND_HOME", "HAND_ROLE", "HAND_HARNESS", "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_CONFIG", "GIT_CONFIG_COUNT"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "absent-git-config"))
	run := func(dir, executable string, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, executable, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		t.Logf("%s %v\n%s", filepath.Base(executable), args, out)
		if err != nil {
			t.Fatalf("native command failed: %v", err)
		}
		return string(out)
	}
	fleet := filepath.Join(root, "fleet")
	initialized := run(root, binary, "init", "--canonical", fleet)
	if again := run(root, binary, "init", "--canonical", fleet); again != initialized {
		t.Fatal("canonical init changed Fleet identity after process restart")
	}
	managed := run(root, binary, "runtime", "ensure")
	gitPath := nativeField(t, managed, "git")
	managedRuntime, err := toolchain.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(fleet, "projects", "sample")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.name=Native fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-qm", "fixture"}} {
		spec, err := managedRuntime.Process(gitPath, toolchain.GitArgsWithTemplate(managedRuntime.GitTemplateDir, args)...)
		if err != nil {
			t.Fatal(err)
		}
		spec.Dir = repo
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		out, err := spec.Output(ctx)
		cancel()
		if err != nil {
			t.Fatalf("locked Git fixture setup: %v\n%s", err, out)
		}
	}
	registered := run(fleet, binary, "project", "register", "sample")
	if again := run(fleet, binary, "project", "register", "sample"); again != registered {
		t.Fatal("registration replay changed exact Project/WorkspaceBinding identity")
	}
	projectID := nativeField(t, registered, "project_id")
	run(fleet, binary, "task", "create", "t_native", "--project-id", projectID, "--goal", "native production goal")
	if id, err := store.FleetIDReadOnly(fleet); err != nil || id != nativeField(t, initialized, "fleet_id") {
		t.Fatalf("canonical schema/identity validation: %s, %v", id, err)
	}
	db, err := sql.Open("sqlite", "file:"+(&url.URL{Path: store.Path(fleet)}).EscapedPath()+"?mode=ro&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var goal, owner string
	if err := db.QueryRow(`SELECT goal,project_id FROM task WHERE id='t_native' AND lifecycle='active'`).Scan(&goal, &owner); err != nil || goal != "native production goal" || owner != projectID {
		t.Fatalf("native Task did not retain exact meaning/owner: %q %q %v", goal, owner, err)
	}
	var effects int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM external_operation)+(SELECT COUNT(*) FROM attempt)+(SELECT COUNT(*) FROM policy_revision)`).Scan(&effects); err != nil || effects != 0 {
		t.Fatalf("bootstrap invented policy/Attempt/effects: %d, %v", effects, err)
	}
}

func nativeField(t *testing.T, doc, key string) string {
	t.Helper()
	for _, line := range strings.Split(doc, "\n") {
		if value, found := strings.CutPrefix(line, key+": "); found {
			if strings.HasPrefix(value, `"`) {
				decoded, err := strconv.Unquote(value)
				if err != nil {
					t.Fatal(err)
				}
				return decoded
			}
			return value
		}
	}
	t.Fatalf("missing %s in %s", key, doc)
	return ""
}
