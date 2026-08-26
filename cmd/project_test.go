package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
	"github.com/spf13/cobra"
)

func TestValidateProjectName(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../escape", "nested/name", `nested\\name`, "foo:bar", "repo ", "repo=one"} {
		err := validateProjectName(name)
		if err == nil {
			t.Errorf("validateProjectName(%q) accepted unsafe name", name)
			continue
		}
		if code := exitCodeFor(t, err); code != 2 {
			t.Errorf("validateProjectName(%q) code = %d, want 2", name, code)
		}
	}
	for _, name := range []string{"repo", "repo-name", "repo_name"} {
		if err := validateProjectName(name); err != nil {
			t.Errorf("validateProjectName(%q) failed: %v", name, err)
		}
	}
}

func TestValidateProjectURL(t *testing.T) {
	for _, url := range []string{"https://github.com/org/repo", "git@github.com:org/repo.git", "ssh://git@example.com/repo", "git://example.com/repo"} {
		if err := validateProjectURL(url); err != nil {
			t.Errorf("validateProjectURL(%q) failed: %v", url, err)
		}
	}
	for _, url := range []string{".", "..", "local", "/tmp/repo", "file:///tmp/repo", `C:\\work\\repo`} {
		if err := validateProjectURL(url); err != nil {
			t.Errorf("validateProjectURL(%q) rejected local source: %v", url, err)
		}
	}
	for _, url := range []string{"", "http://github.com/org/repo", "ftp://example.com/repo", "sshx://example.com/repo"} {
		err := validateProjectURL(url)
		if err == nil {
			t.Errorf("validateProjectURL(%q) accepted invalid source", url)
			continue
		}
		if code := exitCodeFor(t, err); code != 2 {
			t.Errorf("validateProjectURL(%q) code = %d, want 2", url, code)
		}
	}
}

func TestProjectCommandRegistersCreate(t *testing.T) {
	cmd, _, err := newProjectCmd().Find([]string{"create"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd == nil {
		t.Fatal("project command does not register create")
	}
}

func TestProjectAddAdoptsLocalGitSourceIntoIndependentLocalProject(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "source with space-世界")
	initGitRepo(t, source)
	if err := os.Mkdir(filepath.Join(source, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	mkFleetDirs(t, home)
	t.Chdir(home)
	faketool.Treehouse{}.Install(t, faketool.Bin(t))

	cmd := newProjectAddCmd()
	cmd.SetArgs([]string{filepath.Join(source, "nested"), "--name", "adopted"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	managed := filepath.Join(home, "projects", "adopted")
	if _, err := os.Stat(managed); err != nil {
		t.Fatal(err)
	}
	if origin := gitConfigOrigin(t, managed); origin != "" {
		t.Fatalf("managed origin = %q, want no origin", origin)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	if got := runGitOutput(t, managed, "log", "-1", "--format=%s"); got != "initial commit\n" {
		t.Fatalf("managed baseline after source removal = %q", got)
	}
	projects, err := project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Name != "adopted" || projects[0].Mode != project.ModeLocalOnly || !project.IsFileLocator(projects[0].URL) {
		t.Fatalf("projects = %+v, want one local-only file-locator project", projects)
	}
}

func TestProjectAddRefusesExplicitRemoteDeliveryForLocalSource(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	initGitRepo(t, source)
	mkFleetDirs(t, home)
	t.Chdir(home)

	cmd := newProjectAddCmd()
	cmd.SetArgs([]string{source, "--mode", project.ModeDirectPR})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "only --mode local-only") {
		t.Fatalf("error = %v, want explicit local mode refusal", err)
	}
}

func TestProjectAddRejectsUnsafeLocalGitStates(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, string)
		want  string
	}{
		{name: "dirty", setup: func(t *testing.T, path string) {
			initGitRepo(t, path)
			if err := os.WriteFile(filepath.Join(path, "untracked.txt"), []byte("uncommitted"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, want: "uncommitted or untracked"},
		{name: "detached", setup: func(t *testing.T, path string) {
			initGitRepo(t, path)
			runGitIn(t, path, "checkout", "--detach", "HEAD")
		}, want: "checked-out branch"},
		{name: "unborn", setup: func(t *testing.T, path string) {
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
			runGitIn(t, path, "init", "-q", "-b", "main")
		}, want: "no committed HEAD"},
		{name: "non-git", setup: func(t *testing.T, path string) {
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}, want: "not a Git worktree"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			source := filepath.Join(t.TempDir(), test.name)
			test.setup(t, source)
			mkFleetDirs(t, home)
			t.Chdir(home)

			cmd := newProjectAddCmd()
			cmd.SetArgs([]string{source, "--name", test.name})
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestProjectCreateMakesDeterministicLocalBaseline(t *testing.T) {
	home := t.TempDir()
	mkFleetDirs(t, home)
	t.Chdir(home)
	faketool.Treehouse{}.Install(t, faketool.Bin(t))

	cmd := newProjectCreateCmd()
	cmd.SetArgs([]string{"created"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(home, "projects", "created")
	if got := runGitOutput(t, path, "symbolic-ref", "--short", "HEAD"); got != "main\n" {
		t.Fatalf("default branch = %q, want main", got)
	}
	if got := runGitOutput(t, path, "log", "-1", "--format=%s"); got != "chore: initialize project\n" {
		t.Fatalf("baseline message = %q", got)
	}
	if got := runGitOutput(t, path, "rev-list", "--count", "HEAD"); got != "1\n" {
		t.Fatalf("baseline commit count = %q, want one commit", got)
	}
	if origin := gitConfigOrigin(t, path); origin != "" {
		t.Fatalf("created origin = %q, want no origin", origin)
	}
	projects, err := project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Mode != project.ModeLocalOnly || !project.IsFileLocator(projects[0].URL) {
		t.Fatalf("projects = %+v, want one local-only file-locator project", projects)
	}
}

func TestProjectAddPreservesCustomDefaultBranchAfterOriginRemoval(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "custom")
	initGitRepo(t, source)
	runGitIn(t, source, "branch", "-m", "trunk")
	runGitIn(t, source, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/heads/trunk")
	mkFleetDirs(t, home)
	t.Chdir(home)
	faketool.Treehouse{}.Install(t, faketool.Bin(t))

	cmd := newProjectAddCmd()
	cmd.SetArgs([]string{source, "--name", "custom"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(home, "projects", "custom")
	if got := runGitOutput(t, managed, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); got != "origin/trunk\n" {
		t.Fatalf("default marker = %q, want origin/trunk", got)
	}
	if got := runGitOutput(t, managed, "rev-parse", "refs/remotes/origin/trunk"); got != runGitOutput(t, managed, "rev-parse", "refs/heads/trunk") {
		t.Fatalf("preserved origin/trunk ref = %q, want local trunk tip", got)
	}
}

func TestProjectAddUsesCheckedOutBranchWithoutRemoteDefaultMarker(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "custom")
	initGitRepo(t, source)
	runGitIn(t, source, "branch", "-m", "trunk")
	runGitIn(t, source, "remote", "add", "origin", "file:///does-not-exist")
	mkFleetDirs(t, home)
	t.Chdir(home)
	faketool.Treehouse{}.Install(t, faketool.Bin(t))

	cmd := newProjectAddCmd()
	cmd.SetArgs([]string{source, "--name", "custom"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(home, "projects", "custom")
	if got := runGitOutput(t, managed, "symbolic-ref", "--short", "HEAD"); got != "trunk\n" {
		t.Fatalf("checked-out branch = %q, want trunk", got)
	}
}

func TestValidateProjectMode(t *testing.T) {
	for _, mode := range []string{"no-mistakes", "direct-pr", "local-only"} {
		if err := validateProjectMode(mode); err != nil {
			t.Errorf("validateProjectMode(%q) failed: %v", mode, err)
		}
	}
	err := validateProjectMode("unexpected")
	if err == nil {
		t.Fatal("validateProjectMode accepted unexpected mode")
	}
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
}

func setupRegisteredURLProject(t *testing.T, oldURL string) (home, clonePath string) {
	t.Helper()
	home = t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	cloneSource, _ := setupSyncProject(t)
	clonePath = filepath.Join(home, "projects", "secondhand")
	if err := os.MkdirAll(filepath.Dir(clonePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(cloneSource, clonePath); err != nil {
		t.Fatal(err)
	}
	runGitIn(t, clonePath, "remote", "set-url", "origin", oldURL)
	if err := project.Add(home, project.Project{
		Name: "secondhand", URL: oldURL, Mode: project.ModeNoMistakes, Upstream: "atqamz/hand",
	}); err != nil {
		t.Fatal(err)
	}
	return home, clonePath
}

func TestProjectSetURLUpdatesRegistryAndOriginPreservingTask(t *testing.T) {
	oldURL := "https://github.com/atqamz/secondhand.git"
	newURL := "https://github.com/atqamz/hand.git"
	home, clonePath := setupRegisteredURLProject(t, oldURL)
	wantTask := state.Task{ID: "task-1", Project: "secondhand", Kind: state.KindShip, Brief: "data/task-1/brief.md"}
	if err := state.Write(home, wantTask); err != nil {
		t.Fatal(err)
	}

	cmd := newProjectSetURLCmd()
	var output strings.Builder
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"secondhand", newURL})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	projects, err := project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].ID == "" {
		t.Fatalf("projects = %+v, want one project with an identity", projects)
	}
	if projects[0] != (project.Project{
		ID: projects[0].ID, Name: "secondhand", URL: newURL, Mode: project.ModeNoMistakes, Upstream: "atqamz/hand",
	}) {
		t.Fatalf("projects = %+v, want repointed project with preserved fields", projects)
	}
	origin := gitConfigOrigin(t, clonePath)
	if origin != newURL {
		t.Fatalf("origin = %q, want %q", origin, newURL)
	}
	if got, err := state.Read(home, wantTask.ID); err != nil || got.ID != wantTask.ID || got.Project != wantTask.Project || got.Kind != wantTask.Kind || got.Brief != wantTask.Brief {
		t.Fatalf("task = %+v, %v, want unchanged logical task %+v", got, err, wantTask)
	}
	if got, err := os.Stat(clonePath); err != nil || !got.IsDir() {
		t.Fatalf("clone path = %s, %v, want same directory", clonePath, err)
	}
	for _, want := range []string{
		"result: url-set", "old_url: \"" + oldURL + "\"", "url: \"" + newURL + "\"",
		"old_origin: \"" + oldURL + "\"", "origin: \"" + newURL + "\"", "clone:",
		"hand project upstream secondhand \"\"",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output = %q, want %q", output.String(), want)
		}
	}
}

func setupRegisteredRenameProject(t *testing.T) (home, clonePath string) {
	t.Helper()
	home = t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	clonePath = filepath.Join(home, "projects", "old-name")
	if err := os.MkdirAll(clonePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clonePath, "marker"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{
		Name: "old-name", URL: "https://github.com/org/repo.git", Mode: project.ModeDirectPR, Upstream: "upstream/repo",
	}); err != nil {
		t.Fatal(err)
	}
	return home, clonePath
}

func TestProjectSetModePreservesActiveTaskAndClone(t *testing.T) {
	home, clonePath := setupRegisteredRenameProject(t)
	wantTask := state.Task{ID: "task-mode", Project: "old-name", Kind: state.KindShip, Brief: "data/task-mode/brief.md", Lifecycle: state.TaskOpen}
	if err := state.Write(home, wantTask); err != nil {
		t.Fatal(err)
	}

	cmd := newProjectSetModeCmd()
	var output strings.Builder
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"old-name", project.ModeNoMistakes})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	projects, err := project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	wantProject := project.Project{Name: "old-name", URL: "https://github.com/org/repo.git", Mode: project.ModeNoMistakes, Upstream: "upstream/repo"}
	if len(projects) != 1 || projects[0].ID == "" {
		t.Fatalf("projects = %+v, want one project with an identity", projects)
	}
	wantProject.ID = projects[0].ID
	if projects[0] != wantProject {
		t.Fatalf("projects = %+v, want %+v", projects, wantProject)
	}
	wantTask.ProjectID = projects[0].ID
	if got, err := state.Read(home, wantTask.ID); err != nil || got != wantTask {
		t.Fatalf("task = %+v, %v, want unchanged task %+v", got, err, wantTask)
	}
	if got, err := os.ReadFile(filepath.Join(clonePath, "marker")); err != nil || string(got) != "keep" {
		t.Fatalf("clone marker = %q, %v, want unchanged clone", got, err)
	}
	for _, want := range []string{"result: mode-set", "old_mode: direct-pr", "mode: no-mistakes", "clone:"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output = %q, want %q", output.String(), want)
		}
	}
}

func TestProjectRenameUpdatesLocalFileLocator(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	oldPath := filepath.Join(home, "projects", "old-name")
	if err := os.MkdirAll(oldPath, 0o755); err != nil {
		t.Fatal(err)
	}
	oldURL, err := project.CanonicalFileLocator(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "old-name", URL: oldURL, Mode: project.ModeLocalOnly}); err != nil {
		t.Fatal(err)
	}

	cmd := newProjectRenameCmd()
	cmd.SetArgs([]string{"old-name", "new-name"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	newURL, err := project.CanonicalFileLocator(filepath.Join(home, "projects", "new-name"))
	if err != nil {
		t.Fatal(err)
	}
	projects, err := project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].URL != newURL {
		t.Fatalf("projects = %+v, want local locator %q", projects, newURL)
	}
}

func TestProjectRenameMovesCloneAndPreservesTaskHistory(t *testing.T) {
	home, oldPath := setupRegisteredRenameProject(t)
	wantTask := state.Task{ID: "task-rename", Project: "old-name", Kind: state.KindShip, Brief: "data/task-rename/brief.md", Lifecycle: state.TaskTerminal}
	if err := state.Write(home, wantTask); err != nil {
		t.Fatal(err)
	}
	registered, err := project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(registered) != 1 || registered[0].ID == "" {
		t.Fatalf("projects = %+v, want one project with an identity", registered)
	}
	identityBeforeRename := registered[0].ID

	cmd := newProjectRenameCmd()
	var output strings.Builder
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"old-name", "new-name"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	newPath := filepath.Join(home, "projects", "new-name")
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old clone stat = %v, want old path absent", err)
	}
	if got, err := os.ReadFile(filepath.Join(newPath, "marker")); err != nil || string(got) != "keep" {
		t.Fatalf("new clone marker = %q, %v, want moved clone", got, err)
	}
	projects, err := project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	wantProject := project.Project{ID: identityBeforeRename, Name: "new-name", URL: "https://github.com/org/repo.git", Mode: project.ModeDirectPR, Upstream: "upstream/repo"}
	if len(projects) != 1 || projects[0] != wantProject {
		t.Fatalf("projects = %+v, want %+v", projects, wantProject)
	}
	gotTask, err := state.Read(home, wantTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotTask.Project != "new-name" || gotTask.ID != wantTask.ID || gotTask.Brief != wantTask.Brief {
		t.Fatalf("task = %+v, want same task with new project name", gotTask)
	}
	// The task never had its own copy of the name rewritten: it points at the identity the
	// rename could not change, and the live label is resolved through that (atqamz/hand#388).
	if gotTask.ProjectID != identityBeforeRename {
		t.Fatalf("task project identity = %q, want the pre-rename identity %q", gotTask.ProjectID, identityBeforeRename)
	}
	history, err := state.ReadHistory(home, wantTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.Project != "new-name" {
		t.Fatalf("history task = %+v, want renamed project", history.Task)
	}
	for _, want := range []string{"result: renamed", "old_name: old-name", "name: new-name", "old_clone:", "clone:"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output = %q, want %q", output.String(), want)
		}
	}
}

func TestProjectRenameRefusesActiveTasks(t *testing.T) {
	home, oldPath := setupRegisteredRenameProject(t)
	if err := state.Write(home, state.Task{ID: "task-rename-active", Project: "old-name", Kind: state.KindShip}); err != nil {
		t.Fatal(err)
	}

	cmd := newProjectRenameCmd()
	cmd.SetArgs([]string{"old-name", "new-name"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
	if !strings.Contains(err.Error(), "active tasks") {
		t.Fatalf("err = %v, want active tasks referencing it", err)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("old clone stat = %v, want clone unmoved", err)
	}
	if _, err := os.Stat(filepath.Join(home, "projects", "new-name")); !os.IsNotExist(err) {
		t.Fatalf("new clone stat = %v, want destination absent", err)
	}
	projects, err := project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Name != "old-name" {
		t.Fatalf("projects = %+v, want old registration", projects)
	}
}

func TestProjectRenameRollsBackCloneWhenRegistryUpdateFails(t *testing.T) {
	home, oldPath := setupRegisteredRenameProject(t)
	original := renameProjectRegistry
	t.Cleanup(func() { renameProjectRegistry = original })
	renameProjectRegistry = func(string, string, string) error { return errors.New("registry update failed") }

	cmd := newProjectRenameCmd()
	cmd.SetArgs([]string{"old-name", "new-name"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "registry update failed") {
		t.Fatalf("error = %v, want registry update failure", err)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("old clone stat = %v, want rollback to old path", err)
	}
	if _, err := os.Stat(filepath.Join(home, "projects", "new-name")); !os.IsNotExist(err) {
		t.Fatalf("new clone stat = %v, want moved clone rolled back", err)
	}
	projects, err := project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Name != "old-name" {
		t.Fatalf("projects = %+v, want old registration after rollback", projects)
	}
}

func TestProjectRenameRejectsRegisteredDestinationBeforeMovingClone(t *testing.T) {
	home, oldPath := setupRegisteredRenameProject(t)
	if err := project.Add(home, project.Project{Name: "new-name", URL: "https://github.com/org/new.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}

	cmd := newProjectRenameCmd()
	cmd.SetArgs([]string{"old-name", "new-name"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `project "new-name" already registered`) {
		t.Fatalf("error = %v, want registered destination refusal", err)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("old clone stat = %v, want clone unmoved", err)
	}
}

func TestProjectSetModeAndRenameRejectUnregisteredProject(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	for name, cmd := range map[string]*cobra.Command{
		"set-mode": newProjectSetModeCmd(),
		"rename":   newProjectRenameCmd(),
	} {
		cmd.SetArgs(func() []string {
			if name == "set-mode" {
				return []string{"missing", project.ModeDirectPR}
			}
			return []string{"missing", "new-name"}
		}())
		err := cmd.Execute()
		var exitErr *ExitError
		if !errors.As(err, &exitErr) || exitErr.Code != 3 || !strings.Contains(err.Error(), `project "missing" not registered`) {
			t.Fatalf("%s error = %v, want unregistered project code 3", name, err)
		}
	}
}

func gitConfigOrigin(t *testing.T, clonePath string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", clonePath, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

func executeProjectSetURL(t *testing.T, home, url string) error {
	t.Helper()
	t.Chdir(home)
	cmd := newProjectSetURLCmd()
	cmd.SetArgs([]string{"secondhand", url})
	return cmd.Execute()
}

func TestProjectSetURLRejectsInvalidURLBeforeMutation(t *testing.T) {
	oldURL := "https://github.com/atqamz/secondhand.git"
	home, clonePath := setupRegisteredURLProject(t, oldURL)

	err := executeProjectSetURL(t, home, "file:///tmp/renamed")
	assertExitCode2(t, err)
	projects, listErr := project.List(home)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if projects[0].URL != oldURL || gitConfigOrigin(t, clonePath) != oldURL {
		t.Fatalf("after invalid URL, project = %+v and origin = %q, want both old", projects[0], gitConfigOrigin(t, clonePath))
	}
}

func TestProjectSetURLRefusesLocalManagedProject(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	clonePath := filepath.Join(home, "projects", "local")
	initGitRepo(t, clonePath)
	if err := project.Add(home, project.Project{Name: "local", URL: "file:///tmp/local", Mode: project.ModeLocalOnly}); err != nil {
		t.Fatal(err)
	}

	cmd := newProjectSetURLCmd()
	cmd.SetArgs([]string{"local", "https://example.com/local"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "local-managed project") {
		t.Fatalf("error = %v, want local-managed refusal", err)
	}
	if got := gitConfigOrigin(t, clonePath); got != "" {
		t.Fatalf("origin = %q, want no origin after refusal", got)
	}
}

func TestProjectSetURLRefusesMissingProject(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	err := executeProjectSetURL(t, home, "https://github.com/atqamz/hand.git")
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), `project "secondhand" not registered`) {
		t.Fatalf("error = %v, want missing project", err)
	}
}

func TestProjectSetURLRefusesMissingCloneAndOrigin(t *testing.T) {
	oldURL := "https://github.com/atqamz/secondhand.git"
	newURL := "https://github.com/atqamz/hand.git"

	t.Run("missing clone", func(t *testing.T) {
		home, _ := setupRegisteredURLProject(t, oldURL)
		if err := os.RemoveAll(filepath.Join(home, "projects", "secondhand")); err != nil {
			t.Fatal(err)
		}
		if err := executeProjectSetURL(t, home, newURL); err == nil {
			t.Fatal("set-url succeeded without a clone")
		}
		projects, err := project.List(home)
		if err != nil {
			t.Fatal(err)
		}
		if projects[0].URL != oldURL {
			t.Fatalf("URL = %q, want %q", projects[0].URL, oldURL)
		}
	})

	t.Run("missing origin", func(t *testing.T) {
		home, clonePath := setupRegisteredURLProject(t, oldURL)
		runGitIn(t, clonePath, "remote", "remove", "origin")
		if err := executeProjectSetURL(t, home, newURL); err == nil {
			t.Fatal("set-url succeeded without an origin")
		}
		projects, err := project.List(home)
		if err != nil {
			t.Fatal(err)
		}
		if projects[0].URL != oldURL {
			t.Fatalf("URL = %q, want %q", projects[0].URL, oldURL)
		}
	})
}

func TestProjectSetURLConvergesAlreadyUpdatedSurfaces(t *testing.T) {
	oldURL := "https://github.com/atqamz/secondhand.git"
	newURL := "https://github.com/atqamz/hand.git"
	for _, test := range []struct {
		name          string
		updateOrigin  bool
		updateProject bool
	}{
		{name: "origin already new", updateOrigin: true},
		{name: "registry already new", updateProject: true},
		{name: "both already new", updateOrigin: true, updateProject: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, clonePath := setupRegisteredURLProject(t, oldURL)
			if test.updateOrigin {
				runGitIn(t, clonePath, "remote", "set-url", "origin", newURL)
			}
			if test.updateProject {
				if err := project.SetURL(home, "secondhand", newURL); err != nil {
					t.Fatal(err)
				}
			}
			if err := executeProjectSetURL(t, home, newURL); err != nil {
				t.Fatal(err)
			}
			projects, err := project.List(home)
			if err != nil {
				t.Fatal(err)
			}
			if projects[0].URL != newURL || gitConfigOrigin(t, clonePath) != newURL {
				t.Fatalf("project = %+v, origin = %q, want both new", projects[0], gitConfigOrigin(t, clonePath))
			}
		})
	}
}

func TestRepointProjectLeavesRegistryWhenOriginMutationFails(t *testing.T) {
	oldURL := "https://github.com/atqamz/secondhand.git"
	home, clonePath := setupRegisteredURLProject(t, oldURL)
	originalSetter := setProjectOrigin
	t.Cleanup(func() { setProjectOrigin = originalSetter })
	setProjectOrigin = func(string, string) error { return errors.New("origin mutation failed") }

	if err := executeProjectSetURL(t, home, "https://github.com/atqamz/hand.git"); err == nil || !strings.Contains(err.Error(), "origin mutation failed") {
		t.Fatalf("error = %v, want origin mutation failure", err)
	}
	projects, err := project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if projects[0].URL != oldURL || gitConfigOrigin(t, clonePath) != oldURL {
		t.Fatalf("after failed origin mutation, project = %+v and origin = %q, want old", projects[0], gitConfigOrigin(t, clonePath))
	}
}

func TestRepointProjectRestoresOriginWhenRegistryProjectionFails(t *testing.T) {
	oldURL := "https://github.com/atqamz/secondhand.git"
	newURL := "https://github.com/atqamz/hand.git"
	home, clonePath := setupRegisteredURLProject(t, oldURL)
	originalSetter := setProjectURL
	t.Cleanup(func() { setProjectURL = originalSetter })
	setProjectURL = func(string, string, string) error { return errors.New("registry mutation failed") }

	if err := executeProjectSetURL(t, home, newURL); err == nil || !strings.Contains(err.Error(), "registry mutation failed") {
		t.Fatalf("error = %v, want registry mutation failure", err)
	}
	projects, err := project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if projects[0].URL != oldURL || gitConfigOrigin(t, clonePath) != oldURL {
		t.Fatalf("after rollback, project = %+v and origin = %q, want old", projects[0], gitConfigOrigin(t, clonePath))
	}
}

func TestRepointProjectReportsOriginRestoreFailure(t *testing.T) {
	oldURL := "https://github.com/atqamz/secondhand.git"
	newURL := "https://github.com/atqamz/hand.git"
	home, _ := setupRegisteredURLProject(t, oldURL)
	originalProjectSetter := setProjectURL
	t.Cleanup(func() { setProjectURL = originalProjectSetter })
	setProjectURL = func(string, string, string) error { return errors.New("registry mutation failed") }
	originalSetter := setProjectOrigin
	t.Cleanup(func() { setProjectOrigin = originalSetter })
	call := 0
	setProjectOrigin = func(clonePath, url string) error {
		call++
		if call == 2 {
			return errors.New("origin restore failed")
		}
		return originalSetter(clonePath, url)
	}

	err := executeProjectSetURL(t, home, newURL)
	if err == nil || !strings.Contains(err.Error(), "registry mutation failed") || !strings.Contains(err.Error(), "origin restore failed") {
		t.Fatalf("error = %v, want projection and restore failures", err)
	}
}

func TestProjectAddRemovesIncompleteCloneOnGitFailure(t *testing.T) {
	home := t.TempDir()
	bin := faketool.Bin(t)
	faketool.Command{
		Name:   "git",
		Stderr: "clone failed\n",
		Exit:   1,
		FileAction: &faketool.FileAction{
			PathArg: 2, Relative: "partial", Content: "partial",
		},
	}.Install(t, bin)
	t.Chdir(home)
	mkFleetDirs(t, home)

	cmd := newProjectAddCmd()
	cmd.SetArgs([]string{"https://example.com/org/repo.git", "--mode", "local-only"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "git clone failed") {
		t.Fatalf("project add error = %v, want git clone failure", err)
	}
	if _, err := os.Stat(filepath.Join(home, "projects", "repo")); !os.IsNotExist(err) {
		t.Fatalf("incomplete clone still exists: %v", err)
	}
}

func TestProjectAddRefusesExistingCloneDestination(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, "projects", "repo")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dest, "keep-me")
	if err := os.WriteFile(marker, []byte("user data"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := faketool.Bin(t)
	log := filepath.Join(t.TempDir(), "git.log")
	faketool.Command{Name: "git", Stderr: "git clone should not run\n", Exit: 1, Log: log}.Install(t, bin)
	t.Chdir(home)
	mkFleetDirs(t, home)

	cmd := newProjectAddCmd()
	cmd.SetArgs([]string{"https://example.com/org/repo.git", "--mode", "local-only"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("project add error = %v, want existing destination error", err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "user data" {
		t.Fatalf("existing clone changed: %q, %v", got, err)
	}
	if data, err := os.ReadFile(log); err == nil && len(data) != 0 {
		t.Fatalf("git invocation log = %q, want no invocation", data)
	}
}

func TestProjectAddRefusesAlreadyRegistered(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := project.Add(home, project.Project{Name: "repo", URL: "https://example.com/org/repo.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}

	cmd := newProjectAddCmd()
	cmd.SetArgs([]string{"https://example.com/org/repo.git", "--mode", "local-only"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("err = %v, want already registered", err)
	}
}

func TestProjectRemoveRefusesActiveTasks(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := project.Add(home, project.Project{Name: "myproj", URL: "https://example.com/org/myproj.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip}); err != nil {
		t.Fatal(err)
	}

	cmd := newProjectRemoveCmd()
	cmd.SetArgs([]string{"myproj"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
	if !strings.Contains(err.Error(), "active tasks") {
		t.Fatalf("err = %v, want active tasks referencing it", err)
	}
}

func TestProjectRemoveRefusesUnregistered(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	mkFleetDirs(t, home)

	cmd := newProjectRemoveCmd()
	cmd.SetArgs([]string{"missing-proj"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
	if !strings.Contains(err.Error(), `project "missing-proj" not registered`) {
		t.Fatalf("err = %v, want not registered", err)
	}
}

func TestProjectUpstreamDeclaresAndClears(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := project.Add(home, project.Project{Name: "fork", URL: "https://github.com/atqamz/no-mistakes.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}

	cmd := newProjectUpstreamCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"fork", "https://github.com/kunchenguid/no-mistakes"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "result: upstream-set\nupstream: kunchenguid/no-mistakes\n") {
		t.Fatalf("out = %q, want the declared upstream confirmed", out.String())
	}

	projects, err := project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Upstream != "kunchenguid/no-mistakes" {
		t.Fatalf("got %+v, want the upstream normalized and stored", projects)
	}

	clear := newProjectUpstreamCmd()
	var cleared strings.Builder
	clear.SetOut(&cleared)
	clear.SetArgs([]string{"fork", ""})
	if err := clear.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cleared.String(), "result: upstream-cleared\nupstream: none\n") {
		t.Fatalf("out = %q, want the clear confirmed", cleared.String())
	}
	projects, err = project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if projects[0].Upstream != "" {
		t.Fatalf("upstream = %q, want it cleared", projects[0].Upstream)
	}
}

func TestProjectUpstreamRejectsUnresolvableRepo(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := project.Add(home, project.Project{Name: "fork", URL: "https://github.com/atqamz/no-mistakes.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}

	// "kunchen guid/no-mistakes" parses as a slug but cannot survive the registry
	// projection, which separates fields by whitespace: stored, it would come back
	// truncated and reject the whole line for every later project command.
	for _, repo := range []string{"no-mistakes", "kunchen guid/no-mistakes"} {
		cmd := newProjectUpstreamCmd()
		cmd.SetArgs([]string{"fork", repo})
		err := cmd.Execute()
		var exitErr *ExitError
		if !errors.As(err, &exitErr) || exitErr.Code != 2 {
			t.Fatalf("upstream %q: got %v, want ExitError code 2", repo, err)
		}

		projects, listErr := project.List(home)
		if listErr != nil {
			t.Fatalf("upstream %q: %v", repo, listErr)
		}
		if projects[0].Upstream != "" {
			t.Fatalf("upstream = %q, want nothing recorded from a refused ref %q", projects[0].Upstream, repo)
		}
	}
}

func TestProjectUpstreamRefusesUnregistered(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	mkFleetDirs(t, home)

	cmd := newProjectUpstreamCmd()
	cmd.SetArgs([]string{"missing-proj", "owner/repo"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
}

func TestProjectListShowsUpstreamAlongsideAGateMarker(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "gated", URL: "https://github.com/atqamz/no-mistakes.git", Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}
	if err := project.SetUpstream(home, "gated", "kunchenguid/no-mistakes"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "gated"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeNoMistakesPath(t, "repo not initialized (run 'no-mistakes init' first)"))
	t.Chdir(home)
	mkFleetDirs(t, home)

	cmd := newProjectListCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), ",kunchenguid/no-mistakes,not initialized\n") {
		t.Fatalf("project list output = %q, want the upstream and the gate issue in their own columns", out.String())
	}

	jsonCmd := newProjectListCmd()
	jsonCmd.SetArgs([]string{"--json"})
	var jsonOut strings.Builder
	jsonCmd.SetOut(&jsonOut)
	if err := jsonCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOut.String(), `"upstream": "kunchenguid/no-mistakes"`) {
		t.Fatalf("project list --json output = %q, want the upstream field", jsonOut.String())
	}
}

// A repo URL carries the two characters a TOON row would otherwise read as
// structure, so it has to come back quoted rather than splitting its own cell.
func TestProjectListQuotesAURLRatherThanLettingItSplitTheRow(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	mkFleetDirs(t, home)

	url := "https://github.com/atqamz/hand.git"
	if err := project.Add(home, project.Project{Name: "secondhand", URL: url, Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}

	cmd := newProjectListCmd()
	cmd.SetArgs([]string{"--fields", "name,url,mode"})
	var out strings.Builder
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	want := "projects[1]{name,url,mode}:\n  secondhand,\"" + url + "\",no-mistakes\n"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("project list output = %q, want %q", out.String(), want)
	}
}

func TestProjectListFieldsRejectsAnUnknownName(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	cmd := newProjectListCmd()
	cmd.SetArgs([]string{"--fields", "nope"})
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	if got := exitCodeFor(t, cmd.Execute()); got != 2 {
		t.Fatalf("exit code = %d, want 2 for an unknown field", got)
	}
}

func TestProjectListMarksGateNotInitialized(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "gated", URL: "https://example.com/gated.git", Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "gated"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeNoMistakesPath(t, "repo not initialized (run 'no-mistakes init' first)"))
	t.Chdir(home)
	mkFleetDirs(t, home)

	cmd := newProjectListCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "gate_issues: 1\n") || !strings.Contains(out.String(), ",not initialized\n") {
		t.Fatalf("project list output = %q, want a not-initialized gate marker and the aggregate counting it", out.String())
	}
}

func TestProjectListJSONIncludesGateIssue(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "gated", URL: "https://example.com/gated.git", Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "gated"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeNoMistakesPath(t, "repo not initialized (run 'no-mistakes init' first)"))
	t.Chdir(home)
	mkFleetDirs(t, home)

	cmd := newProjectListCmd()
	cmd.SetArgs([]string{"--json"})
	var out strings.Builder
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"gate_issue": "not initialized"`) {
		t.Fatalf("project list --json output = %q, want gate_issue: not initialized", out.String())
	}
}

func TestProjectListStatesNoGateIssueWhenGateReady(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "gated", URL: "https://example.com/gated.git", Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "gated"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeNoMistakesPath(t, "gate: ready\n\n  no active run"))
	t.Chdir(home)
	mkFleetDirs(t, home)

	cmd := newProjectListCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "gate_issues: 0\n") || !strings.Contains(out.String(), ",none\n") {
		t.Fatalf("project list output = %q, want a ready gate stated as no issue rather than as an absent marker", out.String())
	}
}

func TestReserveCloneDestinationIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "repo")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	results := make(chan error, 2)
	go func() { results <- reserveCloneDestination(path) }()
	go func() { results <- reserveCloneDestination(path) }()

	var successes int
	for range 2 {
		if err := <-results; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("reserveCloneDestination successes = %d, want 1", successes)
	}
}

func TestHasActiveTasksForProjectFailsOnMalformedState(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state", "broken.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := hasActiveTasksForProject(home, "repo"); err == nil {
		t.Fatal("expected malformed state to fail closed")
	}
}

func TestHasActiveTasksForProjectFailsOnIncompleteState(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state", "incomplete.json"), []byte(`{"id":"task"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := hasActiveTasksForProject(home, "repo"); err == nil {
		t.Fatal("expected incomplete state to fail closed")
	}
}

func TestResolveInitHome(t *testing.T) {
	cwd := "/workspace"
	if got, err := resolveInitHome(cwd, nil); err != nil || got != cwd {
		t.Fatalf("resolveInitHome(cwd, nil) = %q, %v", got, err)
	}
	if got, err := resolveInitHome(cwd, []string{"fleet"}); err != nil || got != filepath.Join(cwd, "fleet") {
		t.Fatalf("resolveInitHome relative = %q, %v", got, err)
	}
	if _, err := resolveInitHome(cwd, []string{"one", "two"}); err == nil {
		t.Fatal("expected more than one init path to fail")
	} else if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
	if _, err := resolveInitHome(cwd, []string{"  "}); err == nil {
		t.Fatal("expected blank init path to fail")
	} else if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
}

func TestWarnHandHomeMismatch(t *testing.T) {
	home := t.TempDir()

	t.Setenv("HAND_HOME", "")
	var quiet strings.Builder
	if err := warnHandHomeMismatch(&quiet, home); err != nil {
		t.Fatal(err)
	}
	if quiet.String() != "" {
		t.Fatalf("got %q, want no warning without HAND_HOME", quiet.String())
	}

	t.Setenv("HAND_HOME", home)
	quiet.Reset()
	if err := warnHandHomeMismatch(&quiet, home); err != nil {
		t.Fatal(err)
	}
	if quiet.String() != "" {
		t.Fatalf("got %q, want no warning when HAND_HOME is the initialized home", quiet.String())
	}

	elsewhere := t.TempDir()
	t.Setenv("HAND_HOME", elsewhere)
	var warned strings.Builder
	if err := warnHandHomeMismatch(&warned, home); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warned.String(), elsewhere) || !strings.Contains(warned.String(), home) {
		t.Fatalf("got %q, want a warning naming both %q and %q", warned.String(), elsewhere, home)
	}

	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("HAND_HOME", "fleet")
	absHandHome := filepath.Join(cwd, "fleet")
	var relative strings.Builder
	if err := warnHandHomeMismatch(&relative, home); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("warning: HAND_HOME is set to %s, so every other hand command will use that home, not %s\n", absHandHome, home)
	if relative.String() != want {
		t.Fatalf("got %q, want %q", relative.String(), want)
	}
}
