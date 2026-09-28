package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

const attachUsage = "usage: hand attach [supervisor | aN]"

func init() {
	commands["attach"] = cmdAttach
}

func cmdAttach(r *runner, args []string) error {
	fs := flags("attach")
	if err := fs.Parse(args); err != nil {
		return usageError{fmt.Sprintf("attach: %v; %s", err, attachUsage)}
	}
	if fs.NArg() > 1 {
		return usageError{attachUsage}
	}
	target := fs.Arg(0)
	var attemptID int64
	switch {
	case target == "", target == "supervisor":
	case strings.HasPrefix(target, "a"):
		id, err := parseID("a", target)
		if err != nil {
			return err
		}
		attemptID = id
	default:
		return usageError{"attach opens the supervisor or an attempt; " + attachUsage}
	}
	if !r.terminal() {
		return usageError{"attach needs a terminal; run it from an interactive shell"}
	}
	bin, err := harness.LookPath("luvus", r.env.Getenv("PATH"))
	if err != nil {
		return err
	}
	var argv []string
	err = r.withSupervisor(func(ctx context.Context, st *state.Store, _ luvus.Client, _ luvus.Capabilities) error {
		session := fleet.Session(r.fleet.ID)
		switch {
		case target == "":
			argv = []string{bin, "session", "attach", session}
		case target == "supervisor":
			sup, err := runningSupervisor(ctx, st)
			if err != nil {
				return err
			}
			argv = []string{bin, "--session", session, "attach", sup.PaneID}
		default:
			a, err := runningAttempt(ctx, st, attemptID)
			if err != nil {
				return fmt.Errorf("%w; see `hand attempt show %s`", err, state.AttemptRef(attemptID))
			}
			argv = []string{bin, "--session", session, "attach", a.PaneID}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if r.env.Exec != nil {
		return r.env.Exec(argv[0], argv, r.env.Environ())
	}
	return syscall.Exec(argv[0], argv, r.env.Environ())
}

func (r *runner) terminal() bool {
	if r.env.Terminal != nil {
		return r.env.Terminal()
	}
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
