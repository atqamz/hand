package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
		Use:   "init",
		Short: "Initialize secondhand runtime directories",
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
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

			fmt.Fprintf(cmd.OutOrStdout(), "initialized secondhand home at %s\n", home)
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

	fmt.Fprintf(out, "found harnesses: %s\n", strings.Join(foundHarnesses, " "))
	fmt.Fprintf(out, "found tools: %s\n", strings.Join(foundTools, " "))

	if len(foundHarnesses) == 0 {
		return fmt.Errorf("no supported harnesses found on PATH")
	}

	harness := foundHarnesses[0]
	model := "sonnet"
	effort := "low"

	if err := os.WriteFile(filepath.Join(home, "config", "harness"), []byte(harness+"\n"), 0o644); err != nil {
		return fmt.Errorf("write config/harness: %w", err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "model"), []byte(model+"\n"), 0o644); err != nil {
		return fmt.Errorf("write config/model: %w", err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "effort"), []byte(effort+"\n"), 0o644); err != nil {
		return fmt.Errorf("write config/effort: %w", err)
	}

	fmt.Fprintf(out, "default worker harness: %s\n", harness)
	fmt.Fprintf(out, "default worker model: %s\n", model)
	fmt.Fprintf(out, "worker effort: %s\n", effort)
	fmt.Fprintln(out, "wrote config/harness config/model config/effort")

	return nil
}
