package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/atqamz/secondhand/internal/project"
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
	return cmd
}

func newProjectAddCmd() *cobra.Command {
	var mode string
	var name string

	cmd := &cobra.Command{
		Use:   "add <repo-url>",
		Short: "Clone a git repository and register it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			url := args[0]
			home, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}

			if name == "" {
				name = project.DeriveName(url)
			}

			if _, exists, err := project.Find(home, name); err != nil {
				return err
			} else if exists {
				return fmt.Errorf("project %q already registered", name)
			}

			clonePath := filepath.Join(home, "projects", name)
			if err := gitClone(url, clonePath); err != nil {
				return err
			}

			if mode == project.ModeNoMistakes {
				if err := noMistakesInit(clonePath); err != nil {
					os.RemoveAll(clonePath)
					return err
				}
			}

			if err := treehouseInitIfNeeded(clonePath); err != nil {
				os.RemoveAll(clonePath)
				return err
			}

			if err := project.Add(home, project.Project{Name: name, URL: url, Mode: mode}); err != nil {
				os.RemoveAll(clonePath)
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "added project %s (%s) mode=%s\n", name, url, mode)
			return nil
		},
	}

	cmd.Flags().StringVar(&mode, "mode", project.ModeDirectPR, "delivery mode: no-mistakes, direct-pr, local-only")
	cmd.Flags().StringVar(&name, "name", "", "override the project name")
	return cmd
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
		return nil
	}
	c := exec.Command("treehouse", "init")
	c.Dir = clonePath
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("treehouse init failed: %s", string(out))
	}
	return nil
}

func newProjectListCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List registered projects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}

			projects, err := project.List(home)
			if err != nil {
				return err
			}

			if asJSON {
				type projectJSON struct {
					Name string `json:"name"`
					URL  string `json:"url"`
					Mode string `json:"mode"`
				}
				out := make([]projectJSON, 0, len(projects))
				for _, p := range projects {
					out = append(out, projectJSON{Name: p.Name, URL: p.URL, Mode: p.Mode})
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			w := cmd.OutOrStdout()
			for _, p := range projects {
				fmt.Fprintf(w, "%-12s%-40s%s\n", p.Name, p.URL, p.Mode)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	return cmd
}

func newProjectRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Unregister a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			home, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}

			if active, err := hasActiveTasksForProject(home, name); err != nil {
				return err
			} else if active {
				return fmt.Errorf("project %q has active tasks referencing it", name)
			}

			if err := project.Remove(home, name); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "removed project %s (clone retained at projects/%s)\n", name, name)
			return nil
		},
	}
	return cmd
}

func hasActiveTasksForProject(home, name string) (bool, error) {
	entries, err := os.ReadDir(filepath.Join(home, "state"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read state directory: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(home, "state", e.Name()))
		if err != nil {
			return false, fmt.Errorf("read state file %s: %w", e.Name(), err)
		}
		var task struct {
			Project string `json:"project"`
		}
		if err := json.Unmarshal(data, &task); err != nil {
			continue
		}
		if task.Project == name {
			return true, nil
		}
	}
	return false, nil
}
