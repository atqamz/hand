package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/atomicfile"
	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/integration"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
	"github.com/spf13/cobra"
)

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage the project registry",
	}
	cmd.AddCommand(newProjectAddCmd())
	cmd.AddCommand(newProjectCreateCmd())
	cmd.AddCommand(newProjectListCmd())
	cmd.AddCommand(newProjectRemoveCmd())
	cmd.AddCommand(newProjectSyncCmd())
	cmd.AddCommand(newProjectUpstreamCmd())
	cmd.AddCommand(newProjectSetURLCmd())
	cmd.AddCommand(newProjectSetModeCmd())
	cmd.AddCommand(newProjectRenameCmd())
	return cmd
}

type repointResult struct {
	Project   project.Project
	OldURL    string
	URL       string
	OldOrigin string
	Origin    string
	Clone     string
}

var setProjectOrigin = setOriginURL

var setProjectURL = project.SetURL

func newProjectSetURLCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-url <name> <repo-url>",
		Short: "Repoint a remote-backed project at a new repository URL",
		Long: "Repoint a registered project at a new repository URL. The project name and clone path remain unchanged;\n" +
			"the registry URL and clone origin are updated together, while tasks and history are preserved.\n" +
			"Local-managed projects do not have a live origin and must remain local-only.\n" +
			"The command refuses rather than deliberately leaving a registry-only update.",
		Args: usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, url := args[0], args[1]
			if err := validateRemoteProjectURL(url); err != nil {
				return err
			}
			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			release, err := state.Lock(home, "project:"+name)
			if err != nil {
				return fmt.Errorf("lock project %q: %w", name, err)
			}
			defer release()

			result, err := repointProject(home, name, url)
			if err != nil {
				return asPrecondition(err)
			}

			var doc axi.Doc
			doc.Field("name", result.Project.Name)
			doc.Field("result", "url-set")
			doc.Field("old_url", result.OldURL)
			doc.Field("url", result.URL)
			doc.Field("old_origin", result.OldOrigin)
			doc.Field("origin", result.Origin)
			doc.Field("clone", result.Clone)
			if slug, ok := project.ParseRepoRef(result.URL); ok && result.Project.Upstream != "" && strings.EqualFold(slug, result.Project.Upstream) {
				doc.Help(fmt.Sprintf("Upstream %s is now the project's own repo; clear it with `hand project upstream %s \"\"` if redundant", result.Project.Upstream, result.Project.Name))
			}
			return doc.Render(cmd.OutOrStdout())
		},
	}
	return cmd
}

func newProjectSetModeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-mode <name> <mode>",
		Short: "Change a registered project's delivery mode",
		Long:  "Change a registered project's delivery mode in place. The clone, tasks, and history remain unchanged, and the command does not require removing the project first.",
		Args:  usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, mode := args[0], args[1]
			if err := validateProjectMode(mode); err != nil {
				return err
			}
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			release, err := state.Lock(fleetHome, "project:"+name)
			if err != nil {
				return fmt.Errorf("lock project %q: %w", name, err)
			}
			defer release()

			p, exists, err := project.Find(fleetHome, name)
			if err != nil {
				return err
			}
			if !exists {
				return asPrecondition(fmt.Errorf("project %q %w", name, project.ErrNotFound))
			}
			if err := project.SetMode(fleetHome, name, mode); err != nil {
				return asPrecondition(err)
			}

			var doc axi.Doc
			doc.Field("name", p.Name)
			doc.Field("result", "mode-set")
			doc.Field("old_mode", p.Mode)
			doc.Field("mode", mode)
			doc.Field("clone", filepath.Join(fleetHome, "projects", p.Name))
			return doc.Render(cmd.OutOrStdout())
		},
	}
	return cmd
}

var moveProjectClone = os.Rename
var renameProjectRegistry = project.Rename

type renameResult struct {
	Project  project.Project
	OldName  string
	OldClone string
	Clone    string
}

func newProjectRenameCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rename <old-name> <new-name>",
		Short: "Rename a registered project",
		Long:  "Rename a registered project in place. The managed clone moves with the registration, while tasks and history remain attached to the same project.",
		Args:  usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldName, newName := args[0], args[1]
			if err := validateProjectName(oldName); err != nil {
				return err
			}
			if err := validateProjectName(newName); err != nil {
				return err
			}
			if oldName == newName {
				return &ExitError{Err: fmt.Errorf("old and new project names must differ"), Code: 2}
			}
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			release, err := state.Lock(fleetHome, "project:"+oldName)
			if err != nil {
				return fmt.Errorf("lock project %q: %w", oldName, err)
			}
			defer release()

			result, err := renameProject(fleetHome, oldName, newName)
			if err != nil {
				return asPrecondition(err)
			}

			var doc axi.Doc
			doc.Field("name", result.Project.Name)
			doc.Field("result", "renamed")
			doc.Field("old_name", result.OldName)
			doc.Field("old_clone", result.OldClone)
			doc.Field("clone", result.Clone)
			return doc.Render(cmd.OutOrStdout())
		},
	}
	return cmd
}

func renameProject(homeDir, oldName, newName string) (renameResult, error) {
	p, exists, err := project.Find(homeDir, oldName)
	if err != nil {
		return renameResult{}, err
	}
	if !exists {
		return renameResult{}, fmt.Errorf("project %q %w", oldName, project.ErrNotFound)
	}
	if _, exists, err := project.Find(homeDir, newName); err != nil {
		return renameResult{}, err
	} else if exists {
		return renameResult{}, fmt.Errorf("project %q already registered", newName)
	}
	if active, err := hasActiveTasksForProject(homeDir, oldName); err != nil {
		return renameResult{}, err
	} else if active {
		return renameResult{}, &ExitError{Err: fmt.Errorf("project %q has active tasks referencing it", oldName), Code: 3}
	}

	oldClone := filepath.Join(homeDir, "projects", oldName)
	newClone := filepath.Join(homeDir, "projects", newName)
	if info, err := os.Stat(oldClone); err != nil {
		return renameResult{}, fmt.Errorf("project %q clone %q: %w", oldName, oldClone, err)
	} else if !info.IsDir() {
		return renameResult{}, fmt.Errorf("project %q clone %q is not a directory", oldName, oldClone)
	}
	if _, err := os.Lstat(newClone); err == nil {
		return renameResult{}, fmt.Errorf("project destination %q already exists", newClone)
	} else if !os.IsNotExist(err) {
		return renameResult{}, fmt.Errorf("check project destination %q: %w", newClone, err)
	}

	if err := moveProjectClone(oldClone, newClone); err != nil {
		return renameResult{}, fmt.Errorf("move project clone: %w", err)
	}
	if err := renameProjectRegistry(homeDir, oldName, newName); err != nil {
		if rollbackErr := moveProjectClone(newClone, oldClone); rollbackErr != nil {
			return renameResult{}, errors.Join(err, fmt.Errorf("restore project clone: %w", rollbackErr))
		}
		return renameResult{}, err
	}

	p.Name = newName
	return renameResult{Project: p, OldName: oldName, OldClone: oldClone, Clone: newClone}, nil
}

func repointProject(homeDir, name, url string) (repointResult, error) {
	p, exists, err := project.Find(homeDir, name)
	if err != nil {
		return repointResult{}, err
	}
	if !exists {
		return repointResult{}, fmt.Errorf("project %q %w", name, project.ErrNotFound)
	}
	if project.IsFileLocator(p.URL) {
		return repointResult{}, fmt.Errorf("project %q is a local-managed project; set-url is supported only for remote-backed projects", name)
	}
	if err := validateRemoteProjectURL(url); err != nil {
		return repointResult{}, err
	}

	clonePath := filepath.Join(homeDir, "projects", p.Name)
	if info, err := os.Stat(clonePath); err != nil {
		return repointResult{}, fmt.Errorf("project %q clone %q: %w", p.Name, clonePath, err)
	} else if !info.IsDir() {
		return repointResult{}, fmt.Errorf("project %q clone %q is not a directory", p.Name, clonePath)
	}
	oldOrigin, err := storedOriginURL(clonePath)
	if err != nil {
		return repointResult{}, fmt.Errorf("project %q: %w", p.Name, err)
	}
	if err := setProjectOrigin(clonePath, url); err != nil {
		return repointResult{}, err
	}
	if err := setProjectURL(homeDir, p.Name, url); err != nil {
		restoreErr := setProjectOrigin(clonePath, oldOrigin)
		if restoreErr != nil {
			return repointResult{}, errors.Join(err, fmt.Errorf("restore origin for project %q: %w", p.Name, restoreErr))
		}
		return repointResult{}, err
	}
	return repointResult{
		Project:   p,
		OldURL:    p.URL,
		URL:       url,
		OldOrigin: oldOrigin,
		Origin:    url,
		Clone:     clonePath,
	}, nil
}

func storedOriginURL(clonePath string) (string, error) {
	out, err := runManagedCore(context.Background(), "git", clonePath, "config", "--get", "remote.origin.url")
	if err != nil {
		return "", fmt.Errorf("read stored origin: %w", err)
	}
	origin := strings.TrimSpace(string(out))
	if origin == "" {
		return "", fmt.Errorf("origin remote is empty")
	}
	return origin, nil
}

func setOriginURL(clonePath, url string) error {
	out, err := runManagedCore(context.Background(), "git", clonePath, "remote", "set-url", "origin", url)
	if err != nil {
		return fmt.Errorf("set origin in %s: %s", clonePath, strings.TrimSpace(string(out)))
	}
	return nil
}

func newProjectUpstreamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upstream <name> <repo>",
		Short: "Declare the upstream repo a fork project opens its PRs against",
		Long: "Declare the upstream repo a fork project opens its PRs against, as owner/repo or a\n" +
			"remote URL. hand pr then accepts a PR on either the project's own repo or that\n" +
			"upstream, and gate-opened-PR detection searches both. An empty repo clears the\n" +
			"declaration.",
		Args: usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, repo := args[0], args[1]
			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			upstream := ""
			if repo != "" {
				slug, ok := project.ParseRepoRef(repo)
				if !ok {
					return &ExitError{Err: fmt.Errorf("invalid upstream repo %q: must be owner/repo or a GitHub remote URL", repo), Code: 2}
				}
				upstream = slug
			}

			if err := project.SetUpstream(home, name, upstream); err != nil {
				return asPrecondition(err)
			}

			result := "upstream-set"
			if upstream == "" {
				result = "upstream-cleared"
			}
			var doc axi.Doc
			doc.Field("name", name)
			doc.Field("result", result)
			doc.Field("upstream", orNone(upstream))
			return doc.Render(cmd.OutOrStdout())
		},
	}
	return cmd
}

func newProjectAddCmd() *cobra.Command {
	var mode string
	var name string

	cmd := &cobra.Command{
		Use:   "add <source>",
		Short: "Clone or adopt a Git repository and register it",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			source, err := classifyProjectSource(args[0])
			if err != nil {
				return &ExitError{Err: err, Code: 2}
			}
			if err := validateProjectURL(args[0]); err != nil {
				return err
			}
			if source.remote {
				if err := validateProjectMode(mode); err != nil {
					return err
				}
			} else if cmd.Flags().Changed("mode") && mode != project.ModeLocalOnly {
				return &ExitError{Err: fmt.Errorf("local Git sources support only --mode local-only"), Code: 2}
			} else {
				mode = project.ModeLocalOnly
			}
			if err := validateProjectMode(mode); err != nil {
				return err
			}
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			if !source.remote {
				source, err = resolveLocalProjectSource(source)
				if err != nil {
					return &ExitError{Err: err, Code: 2}
				}
			}
			if name == "" && source.remote {
				name = project.DeriveName(source.input)
			}
			if name == "" {
				name = projectNameFromRoot(source.root)
			}
			if err := validateProjectName(name); err != nil {
				return err
			}

			release, err := state.Lock(fleetHome, "project:"+name)
			if err != nil {
				return fmt.Errorf("lock project %q: %w", name, err)
			}
			defer release()

			if !source.remote {
				managed, err := project.IsManagedPath(fleetHome, source.root)
				if err != nil {
					return err
				}
				if managed {
					return &ExitError{Err: fmt.Errorf("local project source %q is already a managed Hand project", source.input), Code: 3}
				}
			}

			if _, exists, err := project.Find(fleetHome, name); err != nil {
				return err
			} else if exists {
				return &ExitError{Err: fmt.Errorf("project %q already registered", name), Code: 3}
			}

			clonePath := filepath.Join(fleetHome, "projects", name)
			if err := reserveCloneDestination(clonePath); err != nil {
				return err
			}
			var cloneErr error
			if source.remote {
				cloneErr = gitClone(source.input, clonePath)
			} else {
				cloneErr = gitCloneLocal(source.root, clonePath)
			}
			if cloneErr != nil {
				return cleanupCloneAfterFailure(clonePath, cloneErr)
			}
			if !source.remote {
				if err := prepareAdoptedClone(source, clonePath); err != nil {
					return cleanupCloneAfterFailure(clonePath, err)
				}
			}

			if mode == project.ModeNoMistakes {
				if err := noMistakesInit(clonePath); err != nil {
					return cleanupCloneAfterFailure(clonePath, err)
				}
			}

			if err := treehouseInitIfNeeded(clonePath); err != nil {
				return cleanupCloneAfterFailure(clonePath, err)
			}

			locator := source.input
			if !source.remote {
				locator = source.locator
			}
			if err := project.Add(fleetHome, project.Project{Name: name, URL: locator, Mode: mode}); err != nil {
				if project.IsRegistrationRollbackError(err) {
					return fmt.Errorf("%w; managed repository retained at %s for registry repair", err, clonePath)
				}
				return cleanupCloneAfterFailure(clonePath, err)
			}

			var doc axi.Doc
			doc.Field("name", name)
			doc.Field("result", "added")
			doc.Field("mode", mode)
			doc.Field("url", locator)
			doc.Field("clone", clonePath)
			if !source.remote {
				doc.Field("source", source.input)
				doc.Field("default_branch", source.defaultBranch)
				doc.Field("baseline", source.baseline)
			}
			doc.Help("Run `hand spawn <id> " + name + "` to dispatch a worker into it")
			return doc.Render(cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&mode, "mode", project.ModeDirectPR, "delivery mode: no-mistakes, direct-pr, local-only")
	cmd.Flags().StringVar(&name, "name", "", "override the project name")
	return cmd
}

func newProjectCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new Git-backed project in the fleet",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := validateProjectName(name); err != nil {
				return err
			}
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			release, err := state.Lock(fleetHome, "project:"+name)
			if err != nil {
				return fmt.Errorf("lock project %q: %w", name, err)
			}
			defer release()
			if _, exists, err := project.Find(fleetHome, name); err != nil {
				return err
			} else if exists {
				return &ExitError{Err: fmt.Errorf("project %q already registered", name), Code: 3}
			}

			clonePath := filepath.Join(fleetHome, "projects", name)
			if err := reserveCloneDestination(clonePath); err != nil {
				return err
			}
			baseline, err := initCreatedProject(clonePath)
			if err != nil {
				return cleanupCloneAfterFailure(clonePath, err)
			}
			if err := treehouseInitIfNeeded(clonePath); err != nil {
				return cleanupCloneAfterFailure(clonePath, err)
			}
			locator, err := project.CanonicalFileLocator(clonePath)
			if err != nil {
				return cleanupCloneAfterFailure(clonePath, err)
			}
			if err := project.Add(fleetHome, project.Project{Name: name, URL: locator, Mode: project.ModeLocalOnly}); err != nil {
				if project.IsRegistrationRollbackError(err) {
					return fmt.Errorf("%w; managed repository retained at %s for registry repair", err, clonePath)
				}
				return cleanupCloneAfterFailure(clonePath, err)
			}

			var doc axi.Doc
			doc.Field("name", name)
			doc.Field("result", "created")
			doc.Field("mode", project.ModeLocalOnly)
			doc.Field("url", locator)
			doc.Field("clone", clonePath)
			doc.Field("baseline", baseline)
			doc.Help("Run `hand spawn <id> " + name + "` to dispatch a worker into it")
			return doc.Render(cmd.OutOrStdout())
		},
	}
	return cmd
}

func cleanupCloneAfterFailure(clonePath string, cause error) error {
	if err := os.RemoveAll(clonePath); err != nil {
		return errors.Join(cause, fmt.Errorf("remove incomplete clone: %w", err))
	}
	return cause
}

func reserveCloneDestination(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create projects directory: %w", err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		if os.IsExist(err) {
			return &ExitError{Err: fmt.Errorf("project destination %q already exists", path), Code: 3}
		}
		return fmt.Errorf("reserve project destination: %w", err)
	}
	return nil
}

func validateProjectName(name string) error {
	if !isRegistrySafeName(name) {
		return &ExitError{Err: fmt.Errorf("invalid project name %q: must be a registry-safe identifier", name), Code: 2}
	}
	return nil
}

func isRegistrySafeName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-' {
			return false
		}
		if i == 0 && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func validateProjectURL(url string) error {
	if _, err := classifyProjectSource(url); err != nil {
		return &ExitError{Err: fmt.Errorf("invalid project source %q: %w", url, err), Code: 2}
	}
	return nil
}

func validateRemoteProjectURL(url string) error {
	if isRemoteProjectSource(url) {
		return nil
	}
	return &ExitError{Err: fmt.Errorf("invalid project URL %q: must start with https://, git@, ssh://, or git://", url), Code: 2}
}

func validateProjectMode(mode string) error {
	switch mode {
	case project.ModeNoMistakes, project.ModeDirectPR, project.ModeLocalOnly:
		return nil
	default:
		return &ExitError{Err: fmt.Errorf("invalid project mode %q: must be no-mistakes, direct-pr, or local-only", mode), Code: 2}
	}
}

func gitClone(url, dest string) error {
	out, err := runManagedCore(context.Background(), "git", "", "clone", url, dest)
	if err != nil {
		return fmt.Errorf("git clone failed: %s", string(out))
	}
	return nil
}

func noMistakesInit(clonePath string) error {
	stdout, stderr, err := integration.Run(context.Background(), "delivery/no-mistakes", clonePath, "init")
	out := append(stdout, stderr...)
	if err != nil {
		return fmt.Errorf("no-mistakes init failed: %s", string(out))
	}
	return nil
}

func treehouseInitIfNeeded(clonePath string) error {
	if _, err := os.Stat(filepath.Join(clonePath, "treehouse.toml")); err == nil {
		return excludeLocally(clonePath, "treehouse.toml")
	}
	out, err := runManagedCore(context.Background(), "treehouse", clonePath, "init")
	if err != nil {
		return fmt.Errorf("treehouse init failed: %s", string(out))
	}
	return excludeLocally(clonePath, "treehouse.toml")
}

// Excluding the pool config is hand's job because writing it was: treehouse init leaves
// treehouse.toml untracked and ignores it nowhere (internal/faketool/FIDELITY.md), so a project whose
// repo does not list it reads as a dirty clone from then on and every later project sync skips it.
func excludeLocally(clonePath, pattern string) error {
	// info/exclude rather than .gitignore: it is per-clone and never committed, so hand cannot leave a
	// change of its own in the operator's repo.
	out, err := runManagedCore(context.Background(), "git", clonePath, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return fmt.Errorf("resolve info/exclude in %s: %w", clonePath, err)
	}
	path := strings.TrimSpace(string(out))
	if !filepath.IsAbs(path) {
		path = filepath.Join(clonePath, path)
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body := string(existing)
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return atomicfile.Write(path, "exclude-", []byte(body+pattern+"\n"), 0o644)
}

// One registry row plus the gate check the row is worth reading for, so the column reader never re-runs
// no-mistakes per field.
type projectView struct {
	project   project.Project
	gateIssue string
}

var projectFields = []axi.Column[projectView]{
	{Name: "name", Value: func(v projectView) string { return v.project.Name }},
	{Name: "mode", Value: func(v projectView) string { return v.project.Mode }},
	{Name: "url", Value: func(v projectView) string { return orNone(v.project.URL) }},
	{Name: "upstream", Value: func(v projectView) string { return orNone(v.project.Upstream) }},
	{Name: "gate", Value: func(v projectView) string { return orNone(v.gateIssue) }},
}

var projectDefaultFields = []string{"name", "mode", "url", "upstream", "gate"}

func newProjectListCmd() *cobra.Command {
	var asJSON bool
	var fields []string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List registered projects",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := rejectFieldsWithJSON(fields, asJSON); err != nil {
				return err
			}
			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			projects, err := project.List(home)
			if err != nil {
				return err
			}

			if asJSON {
				type projectJSON struct {
					Name      string `json:"name"`
					URL       string `json:"url"`
					Mode      string `json:"mode"`
					Upstream  string `json:"upstream,omitempty"`
					GateIssue string `json:"gate_issue,omitempty"`
				}
				out := make([]projectJSON, 0, len(projects))
				for _, p := range projects {
					out = append(out, projectJSON{Name: p.Name, URL: p.URL, Mode: p.Mode, Upstream: p.Upstream, GateIssue: gateIssue(home, p)})
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			cols, err := pickFields(projectFields, fields, projectDefaultFields)
			if err != nil {
				return err
			}
			views := make([]projectView, 0, len(projects))
			ungated := 0
			for _, p := range projects {
				v := projectView{project: p, gateIssue: gateIssue(home, p)}
				if v.gateIssue != "" {
					ungated++
				}
				views = append(views, v)
			}

			var doc axi.Doc
			doc.Int("count", len(views))
			doc.Int("gate_issues", ungated)
			axi.Table(&doc, "projects", views, cols)
			doc.Help(projectListHelp(len(views), ungated)...)
			return doc.Render(cmd.OutOrStdout())
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "output as JSON instead of TOON")
	cmd.Flags().StringSliceVar(&fields, "fields", nil, fieldsFlagUsage(projectFields, projectDefaultFields))
	return cmd
}

func projectListHelp(count, ungated int) []string {
	if count == 0 {
		return []string{"Run `hand project add <source>` or `hand project create <name>` to register the first project"}
	}
	help := []string{"Run `hand spawn <id> <project>` to dispatch a worker into one of these"}
	if ungated > 0 {
		help = append(help, "A project with a gate value cannot honour its no-mistakes mode until that is fixed; `hand doctor` and the project's own clone are where to look")
	}
	return help
}

// Reports why a no-mistakes project's recorded mode cannot currently be honoured, so
// hand project list is the surface an operator catches a stale or missing gate registration on,
// instead of a worker discovering it mid-dispatch with nothing obliging it to say so.
func gateIssue(home string, p project.Project) string {
	if p.Mode != project.ModeNoMistakes {
		return ""
	}
	clonePath := filepath.Join(home, "projects", p.Name)
	gateState, err := project.GateStatus(clonePath)
	if err != nil {
		return "unreachable"
	}
	if gateState == project.GateNotInitialized {
		return "not initialized"
	}
	return ""
}

func newProjectRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Unregister a project",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			if active, err := hasActiveTasksForProject(home, name); err != nil {
				return err
			} else if active {
				return &ExitError{Err: fmt.Errorf("project %q has active tasks referencing it", name), Code: 3}
			}

			if err := project.Remove(home, name); err != nil {
				return asPrecondition(err)
			}

			var doc axi.Doc
			doc.Field("name", name)
			doc.Field("result", "removed")
			doc.Field("clone", filepath.Join(home, "projects", name))
			doc.Help("The clone is retained; delete it by hand if the registration was the only thing holding it")
			return doc.Render(cmd.OutOrStdout())
		},
	}
	return cmd
}

func hasActiveTasksForProject(home, name string) (bool, error) {
	tasks, err := state.ListOpen(home)
	if err != nil {
		return false, err
	}
	for _, t := range tasks {
		if t.Project == name {
			return true, nil
		}
	}
	return false, nil
}

func newProjectSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync [name]",
		Short: "Fast-forward remote-backed project clones to their default branch",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			var targets []project.Project
			if len(args) == 1 {
				p, exists, err := project.Find(home, args[0])
				if err != nil {
					return err
				}
				if !exists {
					return &ExitError{Err: fmt.Errorf("project %q not registered", args[0]), Code: 3}
				}
				targets = []project.Project{p}
			} else {
				targets, err = project.List(home)
				if err != nil {
					return err
				}
			}

			var outcomes []syncOutcome
			failed := 0
			for _, p := range targets {
				releaseProject, err := state.Lock(home, "project:"+p.Name)
				if err != nil {
					return fmt.Errorf("lock project %q: %w", p.Name, err)
				}
				outcome, syncErr := syncOneProjectContext(cmd.Context(), home, p)
				releaseProject()

				if syncErr != nil {
					if len(targets) == 1 {
						return syncErr
					}
					failed++
					if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", syncErr); err != nil {
						return err
					}
					continue
				}
				outcomes = append(outcomes, outcome)
			}

			advanced := 0
			for _, o := range outcomes {
				if o.Result == "fast-forwarded" {
					advanced++
				}
			}

			var doc axi.Doc
			doc.Int("count", len(targets))
			doc.Int("advanced", advanced)
			doc.Int("failed", failed)
			axi.Table(&doc, "projects", outcomes, syncFields)
			if failed > 0 {
				doc.Help("A project that failed to sync carries no row here; its error is on stderr")
			}
			return doc.Render(cmd.OutOrStdout())
		},
	}
	return cmd
}

type syncOutcome struct {
	Name   string
	Result string
	Detail string
}

var syncFields = []axi.Column[syncOutcome]{
	{Name: "name", Value: func(o syncOutcome) string { return o.Name }},
	{Name: "result", Value: func(o syncOutcome) string { return o.Result }},
	{Name: "detail", Value: func(o syncOutcome) string { return orNone(o.Detail) }},
}

func skippedSync(name, detail string) (syncOutcome, error) {
	return syncOutcome{Name: name, Result: "skipped", Detail: detail}, nil
}

// Fetches and, when eligible, fast-forwards a single project clone. Never errors on a benign skip (dirty,
// wrong branch, diverged, no remote); those return a skipped outcome.
func syncOneProject(home string, p project.Project) (syncOutcome, error) {
	return syncOneProjectContext(context.Background(), home, p)
}

func syncOneProjectContext(ctx context.Context, home string, p project.Project) (syncOutcome, error) {
	clonePath := filepath.Join(home, "projects", p.Name)
	if project.IsFileLocator(p.URL) {
		return skippedSync(p.Name, "local-managed project; no live origin remote")
	}

	origin, err := storedOriginURL(clonePath)
	if err != nil {
		return skippedSync(p.Name, "no origin remote")
	}
	repointDetail, err := repairProjectRename(ctx, home, p, origin)
	if err != nil {
		return syncOutcome{}, err
	}

	if out, err := runManagedCore(ctx, "git", clonePath, "fetch", "origin", "--prune"); err != nil {
		return syncOutcome{}, fmt.Errorf("%s: git fetch failed: %s", p.Name, strings.TrimSpace(string(out)))
	}

	pruneGoneBranches(clonePath)

	defaultBr, err := defaultBranch(clonePath)
	if err != nil {
		return syncOutcome{}, fmt.Errorf("%s: resolve default branch: %w", p.Name, err)
	}
	currentBr, err := currentBranch(clonePath)
	if err != nil {
		return syncOutcome{}, fmt.Errorf("%s: current branch: %w", p.Name, err)
	}
	if currentBr != defaultBr {
		return skippedSync(p.Name, fmt.Sprintf("on branch %s, not %s", currentBr, defaultBr))
	}
	// A clone registered before hand started excluding the pool config still has it
	// untracked, and registration is the one place that would have excluded it, so
	// without repairing it here such a clone reads dirty and skips every sync forever.
	if err := excludeLocally(clonePath, "treehouse.toml"); err != nil {
		return syncOutcome{}, fmt.Errorf("%s: %w", p.Name, err)
	}
	dirty, err := hasUncommittedChanges(clonePath)
	if err != nil {
		return syncOutcome{}, fmt.Errorf("%s: %w", p.Name, err)
	}
	if dirty {
		return skippedSync(p.Name, "dirty working tree")
	}

	remoteRef := "origin/" + defaultBr
	behind, err := commitCount(clonePath, "HEAD.."+remoteRef)
	if err != nil {
		return syncOutcome{}, fmt.Errorf("%s: %w", p.Name, err)
	}
	if behind == 0 {
		return syncOutcome{Name: p.Name, Result: "up-to-date", Detail: repointDetail}, nil
	}
	ahead, err := commitCount(clonePath, remoteRef+"..HEAD")
	if err != nil {
		return syncOutcome{}, fmt.Errorf("%s: %w", p.Name, err)
	}
	if ahead > 0 {
		return skippedSync(p.Name, "diverged from "+remoteRef)
	}

	if out, err := runManagedCore(ctx, "git", clonePath, "merge", "--ff-only", remoteRef); err != nil {
		return skippedSync(p.Name, "fast-forward failed: "+strings.TrimSpace(string(out)))
	}
	detail := fmt.Sprintf("%s, was %d behind", remoteRef, behind)
	if repointDetail != "" {
		detail = repointDetail + "; " + detail
	}
	return syncOutcome{
		Name:   p.Name,
		Result: "fast-forwarded",
		Detail: detail,
	}, nil
}

func repairProjectRename(ctx context.Context, home string, p project.Project, origin string) (string, error) {
	oldSlug, ok := ghutil.RepoSlugFromRemote(origin)
	if !ok {
		return "", nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	canonical, err := ghutil.ResolveCanonicalRepo(lookupCtx, oldSlug)
	if err != nil {
		return "", nil
	}
	if strings.EqualFold(oldSlug, canonical.NameWithOwner) {
		return "", nil
	}
	newOrigin, ok := ghutil.RewriteGitHubRemote(origin, canonical.NameWithOwner)
	if !ok {
		return "", fmt.Errorf("%s: cannot rewrite GitHub origin %q to canonical repo %q", p.Name, origin, canonical.NameWithOwner)
	}
	if _, err := repointProject(home, p.Name, newOrigin); err != nil {
		return "", fmt.Errorf("%s: repoint renamed repository: %w", p.Name, err)
	}
	return fmt.Sprintf("repointed %s -> %s", oldSlug, canonical.NameWithOwner), nil
}

func commitCount(clonePath, revRange string) (int, error) {
	out, err := runManagedCore(context.Background(), "git", clonePath, "rev-list", "--count", revRange)
	if err != nil {
		return 0, fmt.Errorf("git rev-list --count %s failed: %w", revRange, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parse rev-list count: %w", err)
	}
	return n, nil
}

// Best-effort deletes local branches whose upstream tracking branch is gone. Branches still checked out
// in a worktree refuse deletion; that failure is ignored since pruning must never block a sync.
func pruneGoneBranches(clonePath string) {
	out, err := runManagedCore(context.Background(), "git", clonePath, "for-each-ref", "--format=%(refname:short)|%(upstream:track)", "refs/heads/")
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 || !strings.Contains(parts[1], "[gone]") {
			continue
		}
		_, _ = runManagedCore(context.Background(), "git", clonePath, "branch", "-D", parts[0])
	}
}
