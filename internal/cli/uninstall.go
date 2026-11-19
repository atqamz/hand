package cli

import (
	"os"
	"runtime"

	"github.com/atqamz/hand/internal/uninstall"
	"github.com/atqamz/hand/internal/update"
)

func init() {
	commands["uninstall"] = cmdUninstall
}

var uninstallHooks struct{ Target string }

func cmdUninstall(r *runner, args []string) error {
	set := flags("uninstall")
	dry := set.Bool("dry-run", false, "print what would be removed and kept, and change nothing")
	if _, err := parse(set, args, 0); err != nil {
		return err
	}
	target := uninstallHooks.Target
	if target == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if target, err = update.Target(exe); err != nil {
			return err
		}
	}
	root, err := r.rootDir()
	if err != nil {
		return err
	}
	rep, err := uninstall.Run(uninstall.Options{Target: target, Root: root, OS: runtime.GOOS, Getenv: r.env.Getenv, DryRun: *dry})
	if err != nil {
		if len(rep.Removed)+len(rep.Kept) > 0 {
			_ = r.print(uninstall.Render(rep))
		}
		return err
	}
	return r.print(uninstall.Render(rep))
}
