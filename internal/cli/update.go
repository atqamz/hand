package cli

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/update"
)

func init() {
	commands["update"] = cmdUpdate
}

func cmdUpdate(r *runner, args []string) error {
	set := flags("update")
	channel := set.String("channel", "", "edge or stable; required for a source build")
	check := set.Bool("check", false, "download and compare, but change nothing")
	keep := set.Bool("keep-luvus", false, "leave every fleet's Luvus server and its pending switch alone")
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
		KeepLuvus: *keep,
		Root:      root,
		HandBase:  r.base("HAND_INSTALL_BASE", "https://github.com/atqamz/hand/releases"),
		LuvusBase: r.luvusBase(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		Env:       r.env.Environ(),
		Getenv:    r.env.Getenv,
		Now:       r.env.Now,
		Hold:      func() { signal.Notify(make(chan os.Signal, 1), os.Interrupt, syscall.SIGHUP, syscall.SIGTERM) },
		Cgroup:    string(cgroup),
		Stop:      stopOne,
		Watch: func(ctx context.Context, home string) error {
			w, err := fleetRunner(r, root, home)
			if err != nil {
				return err
			}
			_, err = w.ensureWatcher(ctx)
			return err
		},
		Board: func(ctx context.Context, home string) error {
			w, err := fleetRunner(r, root, home)
			if err != nil {
				return err
			}
			token, err := boardToken(home)
			if err != nil {
				return err
			}
			return w.ensureBoard(ctx, token)
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

func fleetRunner(r *runner, root, home string) (*runner, error) {
	w := &runner{env: r.env, home: home, root: root}
	st, err := w.store()
	if err != nil {
		return nil, err
	}
	return w, st.Close()
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
