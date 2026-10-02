package cli

import (
	"errors"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/update"
)

var updateStop func(pid int, marker string) error

func init() {
	commands["update"] = cmdUpdate
}

func cmdUpdate(r *runner, args []string) error {
	set := flags("update")
	channel := set.String("channel", "", "edge or stable; required for a source build")
	check := set.Bool("check", false, "download and compare, but change nothing")
	if _, err := parse(set, args, 0); err != nil {
		return err
	}
	switch {
	case *channel != "" && *channel != "edge" && *channel != "stable":
		return usageError{"update: --channel must be edge or stable"}
	case *channel == "" && Channel == "source":
		return usageError{"update: this hand was built from source; pass --channel edge or --channel stable"}
	case *channel == "":
		*channel = Channel
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	target, err := update.Target(exe)
	if err != nil {
		return err
	}
	root, err := r.rootDir()
	if err != nil {
		return err
	}
	cgroup, _ := os.ReadFile("/proc/self/cgroup")
	rep, err := update.Run(r.ctx(), update.Options{
		Target:    target,
		From:      update.Build{Version: Version, Channel: Channel, Commit: commit(), Schema: state.SchemaVersion, Luvus: luvus.Tested},
		Channel:   *channel,
		Check:     *check,
		Root:      root,
		HandBase:  r.base("HAND_INSTALL_BASE", "https://github.com/atqamz/hand/releases"),
		LuvusBase: r.base("HAND_LUVUS_BASE", "https://github.com/RizRiyz/luvus/releases"),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		Env:       r.env.Environ(),
		Getenv:    r.env.Getenv,
		Now:       r.env.Now,
		Hold:      func() { signal.Notify(make(chan os.Signal, 1), os.Interrupt, syscall.SIGHUP, syscall.SIGTERM) },
		Cgroup:    string(cgroup),
		Stop:      updateStop,
	})
	if err != nil {
		if rep.Status == "repaired" {
			_ = r.print(update.Render(rep))
		}
		return err
	}
	if err := r.print(update.Render(rep)); err != nil {
		return err
	}
	if rep.Failed {
		return errors.New("update: some steps failed; run the help lines")
	}
	return nil
}

func (r *runner) base(name, fallback string) string {
	if v := r.env.Getenv(name); v != "" {
		return v
	}
	return fallback
}
