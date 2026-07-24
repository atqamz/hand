package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newNotifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notify <message>",
		Short: "Send an out-of-band notification",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			message := args[0]
			home, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}

			template, err := os.ReadFile(filepath.Join(home, "config", "notify"))
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("read notify config: %w", err)
			}
			if err == nil {
				shellCmd := strings.ReplaceAll(strings.TrimSpace(string(template)), "{{message}}", message)
				if out, runErr := exec.Command("sh", "-c", shellCmd).CombinedOutput(); runErr != nil {
					if _, printErr := fmt.Fprintf(cmd.ErrOrStderr(), "warning: notify command failed: %v: %s\n", runErr, strings.TrimSpace(string(out))); printErr != nil {
						return printErr
					}
				}
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "notified: %s\n", message); err != nil {
				return err
			}
			return nil
		},
	}
	return cmd
}
