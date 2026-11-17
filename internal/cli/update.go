package cli

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/update"
)

func init() {
	commands["update"] = cmdUpdate
}

var updateHooks update.Options

func cmdUpdate(r *runner, args []string) error {
	set := flags("update")
	channel := set.String("channel", "", "edge or stable; required for a source build")
	check := set.Bool("check", false, "download and compare, but change nothing")
	keep := set.Bool("keep-luvus", false, "never switch a fleet's Luvus server; a version change stays pending")
	auto := set.Bool("auto", false, "set by the board's timer: do not try a Luvus switch again for a pin it failed on")
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
	target := updateHooks.Target
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
	rep, err := update.Run(r.ctx(), update.Options{
		Target:    target,
		From:      update.Build{Version: Version, Channel: Channel, Commit: commit(), Schema: state.SchemaVersion, Luvus: luvus.Tested},
		Channel:   *channel,
		Check:     *check,
		KeepLuvus: *keep,
		Auto:      *auto,
		Root:      root,
		HandBase:  r.base("HAND_INSTALL_BASE", "https://github.com/atqamz/hand/releases"),
		LuvusBase: r.luvusBase(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		Env:       r.env.Environ(),
		Getenv:    r.env.Getenv,
		Now:       r.env.Now,
		Hold:      func() { signal.Notify(make(chan os.Signal, 1), os.Interrupt, syscall.SIGHUP, syscall.SIGTERM) },
		Stop:      stopOne,
		Kill:      stopRoot,
		Version:   updateHooks.Version,
		Sleep:     updateHooks.Sleep,
		Client: func(e fleet.Entry) luvus.Client {
			return (&runner{env: r.env, home: e.Home, root: root}).client(fleet.Session(e.ID))
		},
		Start: func(ctx context.Context, e fleet.Entry) error {
			_, _, err := updateRunner(r, root, e).luvus(ctx)
			return err
		},
		Watch: func(ctx context.Context, e fleet.Entry) error {
			_, err := updateRunner(r, root, e).ensureWatcher(ctx)
			return err
		},
		Board: func(ctx context.Context, e fleet.Entry) error {
			token, err := boardToken(e.Home)
			if err != nil {
				return err
			}
			return updateRunner(r, root, e).ensureBoard(ctx, token)
		},
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

func updateRunner(r *runner, root string, e fleet.Entry) *runner {
	return &runner{env: r.env, home: e.Home, root: root, fleet: state.Fleet{ID: e.ID, Name: e.Name, Home: e.Home}}
}

func (r *runner) base(name, fallback string) string {
	if v := r.env.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func (r *runner) luvusBase() string {
	return r.base("HAND_LUVUS_BASE", "https://github.com/RizRiyz/luvus/releases")
}
