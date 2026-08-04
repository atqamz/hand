package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/atqamz/secondhand/internal/agentsmd"
	"github.com/atqamz/secondhand/internal/axi"
	"github.com/atqamz/secondhand/internal/store"
	"github.com/spf13/cobra"
)

var harnessCandidates = []string{"claude", "codex", "pi", "grok", "opencode"}
var toolCandidates = []string{"treehouse", "herdr", "no-mistakes", "gh"}

const backlogSkeleton = `# Backlog

## Queue

## In Progress

## Done
`

const projectsSkeleton = `# Projects

`

const operatorSkeleton = `# Operator

Standing constraints and preferences. They outrank the agent's own judgment.

## Identity

## Authority

## Hard constraints

## Preferences
`

const learningsSkeleton = "# Learnings\n"

const doneArchiveSkeleton = "# Done archive\n"

const noteArchiveSkeleton = "# Note archive\n"

func newInitCmd() *cobra.Command {
	var setup bool

	cmd := &cobra.Command{
		Use:   "init [path]",
		Short: "Initialize secondhand runtime directories",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			home, err := resolveInitHome(cwd, args)
			if err != nil {
				return err
			}

			if err := initLayout(home); err != nil {
				return err
			}
			if err := initMarker(home); err != nil {
				return err
			}
			refreshed, err := agentsmd.Refresh(home)
			if err != nil {
				return err
			}

			var chosen setupChoice
			if setup {
				chosen, err = runInteractiveSetup(cmd, home)
				if err != nil {
					return err
				}
			}

			if err := warnHandHomeMismatch(cmd.ErrOrStderr(), home); err != nil {
				return err
			}

			agentsMD := "unchanged"
			if refreshed {
				agentsMD = "written"
			}

			var doc axi.Doc
			doc.Field("result", "initialized")
			doc.Field("home", home)
			doc.Field("agents_md", agentsMD)
			doc.Field("harness", orNone(chosen.harness))
			doc.Field("model", orNone(chosen.model))
			doc.Field("effort", orNone(chosen.effort))
			doc.Help("Run `hand project add <repo-url>` to register the first project",
				"Read AGENTS.md in this home for how a supervising agent is meant to drive it")
			return doc.Render(cmd.OutOrStdout())
		},
	}

	cmd.Flags().BoolVar(&setup, "setup", false, "run interactive first-time setup")
	return cmd
}

func initLayout(home string) error {
	if err := initDirs(home); err != nil {
		return err
	}
	return initSkeletonFiles(home)
}

func initDirs(home string) error {
	dirs := []string{"state", "data", "projects", "config"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	return nil
}

// initSkeletonFiles seeds every file it can and reports every one it could
// not, in this fixed order, because the seeds are independent of each other:
// stopping at the first failure named an arbitrary victim, so two runs against
// the same broken home disagreed about which file was at fault.
func initSkeletonFiles(home string) error {
	files := []struct {
		rel     string
		content string
	}{
		{"data/backlog.md", backlogSkeleton},
		{"data/projects.md", projectsSkeleton},
		{"data/operator.md", operatorSkeleton},
		{"data/learnings.md", learningsSkeleton},
		{"data/done-archive.md", doneArchiveSkeleton},
		{"data/note-archive.md", noteArchiveSkeleton},
	}
	var errs []error
	for _, f := range files {
		path := filepath.Join(home, f.rel)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("stat %s: %w", f.rel, err))
			continue
		}
		if err := os.WriteFile(path, []byte(f.content), 0o644); err != nil {
			errs = append(errs, fmt.Errorf("write %s: %w", f.rel, err))
		}
	}
	return errors.Join(errs...)
}

// initMarker creates state/hand.db up front so home.IsHome's marker exists as
// soon as init returns, rather than waiting for the first command that
// happens to touch machine state. store.Open is safe to call repeatedly.
func initMarker(home string) error {
	db, err := store.Open(home)
	if err != nil {
		return fmt.Errorf("create state/hand.db: %w", err)
	}
	return db.Close()
}

// The prompts stay plain lines: they are a dialog with whoever is at the
// terminal, and only the answers are a result worth rendering.
type setupChoice struct {
	harness string
	model   string
	effort  string
}

func runInteractiveSetup(cmd *cobra.Command, home string) (setupChoice, error) {
	out := cmd.OutOrStdout()

	var foundHarnesses []string
	for _, h := range harnessCandidates {
		if _, err := exec.LookPath(h); err == nil {
			foundHarnesses = append(foundHarnesses, h)
		}
	}

	var foundTools []string
	for _, t := range toolCandidates {
		if _, err := exec.LookPath(t); err == nil {
			foundTools = append(foundTools, t)
		}
	}

	if _, err := fmt.Fprintf(out, "found harnesses: %s\n", strings.Join(foundHarnesses, " ")); err != nil {
		return setupChoice{}, err
	}
	if _, err := fmt.Fprintf(out, "found tools: %s\n", strings.Join(foundTools, " ")); err != nil {
		return setupChoice{}, err
	}

	if len(foundHarnesses) == 0 {
		return setupChoice{}, fmt.Errorf("no supported harnesses found on PATH")
	}

	in := cmd.InOrStdin()
	if _, err := fmt.Fprintln(out, "select default worker harness:"); err != nil {
		return setupChoice{}, err
	}
	for i, h := range foundHarnesses {
		if _, err := fmt.Fprintf(out, "%d) %s\n", i+1, h); err != nil {
			return setupChoice{}, err
		}
	}
	harness, err := readSetupChoice(in, foundHarnesses, "harness")
	if err != nil {
		return setupChoice{}, err
	}

	model, err := readSetupValue(in, out, "default worker model")
	if err != nil {
		return setupChoice{}, err
	}
	effort, err := readSetupValue(in, out, "worker effort")
	if err != nil {
		return setupChoice{}, err
	}

	if err := os.WriteFile(filepath.Join(home, "config", "harness"), []byte(harness+"\n"), 0o644); err != nil {
		return setupChoice{}, fmt.Errorf("write config/harness: %w", err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "model"), []byte(model+"\n"), 0o644); err != nil {
		return setupChoice{}, fmt.Errorf("write config/model: %w", err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "effort"), []byte(effort+"\n"), 0o644); err != nil {
		return setupChoice{}, fmt.Errorf("write config/effort: %w", err)
	}

	return setupChoice{harness: harness, model: model, effort: effort}, nil
}

func resolveInitHome(cwd string, args []string) (string, error) {
	if len(args) > 1 {
		return "", &ExitError{Err: fmt.Errorf("init accepts at most one target path"), Code: 2}
	}
	if len(args) == 0 {
		return cwd, nil
	}
	home := args[0]
	if strings.TrimSpace(home) == "" {
		return "", &ExitError{Err: fmt.Errorf("init target path cannot be empty"), Code: 2}
	}
	if !filepath.IsAbs(home) {
		home = filepath.Join(cwd, home)
	}
	return filepath.Clean(home), nil
}

// warnHandHomeMismatch reports the one asymmetry in home handling: init
// creates the home its argument or working directory names, while every other
// command resolves HAND_HOME first, so an operator who exported HAND_HOME and
// initialized somewhere else would otherwise get a home nothing ever uses.
func warnHandHomeMismatch(w io.Writer, home string) error {
	handHome := os.Getenv("HAND_HOME")
	if handHome == "" {
		return nil
	}
	display := handHome
	if abs, err := filepath.Abs(handHome); err == nil {
		if abs == home {
			return nil
		}
		display = abs
	}
	_, err := fmt.Fprintf(w, "warning: HAND_HOME is set to %s, so every other hand command will use that home, not %s\n", display, home)
	return err
}

func readSetupChoice(in io.Reader, choices []string, label string) (string, error) {
	var input string
	if _, err := fmt.Fscan(in, &input); err != nil {
		return "", fmt.Errorf("read %s choice: %w", label, err)
	}
	choice, err := strconv.Atoi(input)
	if err != nil || choice < 1 || choice > len(choices) {
		return "", fmt.Errorf("invalid %s choice", label)
	}
	return choices[choice-1], nil
}

func readSetupValue(in io.Reader, out io.Writer, label string) (string, error) {
	if _, err := fmt.Fprintf(out, "%s: ", label); err != nil {
		return "", err
	}
	var value string
	if _, err := fmt.Fscan(in, &value); err != nil {
		return "", fmt.Errorf("read %s: %w", label, err)
	}
	return value, nil
}
