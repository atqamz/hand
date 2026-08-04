package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/atqamz/secondhand/internal/atomicfile"
	"github.com/atqamz/secondhand/internal/axi"
	"github.com/atqamz/secondhand/internal/home"
	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
	"github.com/spf13/cobra"
)

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage the project registry",
	}
	cmd.AddCommand(newProjectAddCmd())
	cmd.AddCommand(newProjectListCmd())
	cmd.AddCommand(newProjectRemoveCmd())
	cmd.AddCommand(newProjectSyncCmd())
	cmd.AddCommand(newProjectUpstreamCmd())
	return cmd
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
		Use:   "add <repo-url>",
		Short: "Clone a git repository and register it",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			url := args[0]
			if err := validateProjectURL(url); err != nil {
				return err
			}
			if err := validateProjectMode(mode); err != nil {
				return err
			}
			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			if name == "" {
				name = project.DeriveName(url)
			}
			if err := validateProjectName(name); err != nil {
				return err
			}

			if _, exists, err := project.Find(home, name); err != nil {
				return err
			} else if exists {
				return &ExitError{Err: fmt.Errorf("project %q already registered", name), Code: 3}
			}

			clonePath := filepath.Join(home, "projects", name)
			if err := reserveCloneDestination(clonePath); err != nil {
				return err
			}
			if err := gitClone(url, clonePath); err != nil {
				if cleanupErr := os.RemoveAll(clonePath); cleanupErr != nil {
					return fmt.Errorf("%w; remove incomplete clone: %v", err, cleanupErr)
				}
				return err
			}

			if mode == project.ModeNoMistakes {
				if err := noMistakesInit(clonePath); err != nil {
					return cleanupCloneAfterFailure(clonePath, err)
				}
			}

			if err := treehouseInitIfNeeded(clonePath); err != nil {
				return cleanupCloneAfterFailure(clonePath, err)
			}

			if err := project.Add(home, project.Project{Name: name, URL: url, Mode: mode}); err != nil {
				return cleanupCloneAfterFailure(clonePath, err)
			}

			var doc axi.Doc
			doc.Field("name", name)
			doc.Field("result", "added")
			doc.Field("mode", mode)
			doc.Field("url", url)
			doc.Field("clone", clonePath)
			doc.Help("Run `hand spawn <id> " + name + "` to dispatch a worker into it")
			return doc.Render(cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&mode, "mode", project.ModeDirectPR, "delivery mode: no-mistakes, direct-pr, local-only")
	cmd.Flags().StringVar(&name, "name", "", "override the project name")
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
	for _, prefix := range []string{"https://", "git@", "ssh://", "git://"} {
		if strings.HasPrefix(url, prefix) {
			return nil
		}
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
	c := exec.Command("git", "clone", url, dest)
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone failed: %s", string(out))
	}
	return nil
}

func noMistakesInit(clonePath string) error {
	c := exec.Command("no-mistakes", "init")
	c.Dir = clonePath
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("no-mistakes init failed: %s", string(out))
	}
	return nil
}

func treehouseInitIfNeeded(clonePath string) error {
	if _, err := os.Stat(filepath.Join(clonePath, "treehouse.toml")); err == nil {
		return excludeLocally(clonePath, "treehouse.toml")
	}
	c := exec.Command("treehouse", "init")
	c.Dir = clonePath
	out, err := c.CombinedOutput()
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
	c := exec.Command("git", "-C", clonePath, "rev-parse", "--git-path", "info/exclude")
	var stderr strings.Builder
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		return fmt.Errorf("resolve info/exclude in %s: %w: %s", clonePath, err, strings.TrimSpace(stderr.String()))
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
		return []string{"Run `hand project add <repo-url>` to register the first project"}
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
	tasks, err := state.List(home)
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
		Short: "Fast-forward project clones to their remote default branch",
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
				outcome, syncErr := syncOneProject(home, p)
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
// wrong branch, diverged, no remote) - those come back as a skipped outcome, per SPECS.md's fail-open policy.
func syncOneProject(home string, p project.Project) (syncOutcome, error) {
	clonePath := filepath.Join(home, "projects", p.Name)

	if !hasOriginRemote(clonePath) {
		return skippedSync(p.Name, "no origin remote")
	}

	fetch := exec.Command("git", "fetch", "origin", "--prune")
	fetch.Dir = clonePath
	if out, err := fetch.CombinedOutput(); err != nil {
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
		return syncOutcome{Name: p.Name, Result: "up-to-date"}, nil
	}
	ahead, err := commitCount(clonePath, remoteRef+"..HEAD")
	if err != nil {
		return syncOutcome{}, fmt.Errorf("%s: %w", p.Name, err)
	}
	if ahead > 0 {
		return skippedSync(p.Name, "diverged from "+remoteRef)
	}

	merge := exec.Command("git", "merge", "--ff-only", remoteRef)
	merge.Dir = clonePath
	if out, err := merge.CombinedOutput(); err != nil {
		return skippedSync(p.Name, "fast-forward failed: "+strings.TrimSpace(string(out)))
	}
	return syncOutcome{
		Name:   p.Name,
		Result: "fast-forwarded",
		Detail: fmt.Sprintf("%s, was %d behind", remoteRef, behind),
	}, nil
}

func hasOriginRemote(clonePath string) bool {
	c := exec.Command("git", "remote", "get-url", "origin")
	c.Dir = clonePath
	return c.Run() == nil
}

func commitCount(clonePath, revRange string) (int, error) {
	c := exec.Command("git", "rev-list", "--count", revRange)
	c.Dir = clonePath
	out, err := c.Output()
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
	c := exec.Command("git", "for-each-ref", "--format=%(refname:short)|%(upstream:track)", "refs/heads/")
	c.Dir = clonePath
	out, err := c.Output()
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
		del := exec.Command("git", "branch", "-D", parts[0])
		del.Dir = clonePath
		_ = del.Run()
	}
}
