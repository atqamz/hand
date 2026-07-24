package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

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

const dashboardSkeleton = `# Dashboard

## Active Tasks
| id | project | kind | state | age | pr |
|---|---|---|---|---|---|

## Pending Decisions

## Recent Events

## Recent Completions

## Projects
`

func newInitCmd() *cobra.Command {
	var setup bool

	cmd := &cobra.Command{
		Use:   "init [path]",
		Short: "Initialize secondhand runtime directories",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			home, err := resolveInitHome(cwd, args)
			if err != nil {
				return err
			}

			if err := initDirs(home); err != nil {
				return err
			}
			if err := initSkeletonFiles(home); err != nil {
				return err
			}

			if setup {
				if err := runInteractiveSetup(cmd, home); err != nil {
					return err
				}
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "initialized secondhand home at %s\n", home); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&setup, "setup", false, "run interactive first-time setup")
	return cmd
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

func initSkeletonFiles(home string) error {
	files := map[string]string{
		"data/backlog.md":   backlogSkeleton,
		"data/projects.md":  projectsSkeleton,
		"data/dashboard.md": dashboardSkeleton,
	}
	for rel, content := range files {
		path := filepath.Join(home, rel)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

func runInteractiveSetup(cmd *cobra.Command, home string) error {
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
		return err
	}
	if _, err := fmt.Fprintf(out, "found tools: %s\n", strings.Join(foundTools, " ")); err != nil {
		return err
	}

	if len(foundHarnesses) == 0 {
		return fmt.Errorf("no supported harnesses found on PATH")
	}

	in := cmd.InOrStdin()
	if _, err := fmt.Fprintln(out, "select default worker harness:"); err != nil {
		return err
	}
	for i, h := range foundHarnesses {
		if _, err := fmt.Fprintf(out, "%d) %s\n", i+1, h); err != nil {
			return err
		}
	}
	harness, err := readSetupChoice(in, foundHarnesses, "harness")
	if err != nil {
		return err
	}

	model, err := readSetupValue(in, out, "default worker model")
	if err != nil {
		return err
	}
	effort, err := readSetupValue(in, out, "worker effort")
	if err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(home, "config", "harness"), []byte(harness+"\n"), 0o644); err != nil {
		return fmt.Errorf("write config/harness: %w", err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "model"), []byte(model+"\n"), 0o644); err != nil {
		return fmt.Errorf("write config/model: %w", err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "effort"), []byte(effort+"\n"), 0o644); err != nil {
		return fmt.Errorf("write config/effort: %w", err)
	}

	if _, err := fmt.Fprintf(out, "default worker harness: %s\n", harness); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "default worker model: %s\n", model); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "worker effort: %s\n", effort); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "wrote config/harness config/model config/effort"); err != nil {
		return err
	}

	return nil
}

func resolveInitHome(cwd string, args []string) (string, error) {
	if len(args) > 1 {
		return "", fmt.Errorf("init accepts at most one target path")
	}
	if len(args) == 0 {
		return cwd, nil
	}
	home := args[0]
	if strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("init target path cannot be empty")
	}
	if !filepath.IsAbs(home) {
		home = filepath.Join(cwd, home)
	}
	return filepath.Clean(home), nil
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
