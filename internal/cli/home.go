package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/atqamz/hand/internal/memory"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

func init() {
	commands["init"] = cmdInit
}

func (r *runner) dbPath() string { return filepath.Join(r.home, "hand.db") }

func (r *runner) store() (*state.Store, error) {
	if r.home == "" {
		return nil, usageError{"no home: pass --home DIR or set HAND_HOME"}
	}
	if _, err := os.Stat(r.dbPath()); errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: no hand home at %s; run `hand init`", state.ErrNotFound, r.home)
	} else if err != nil {
		return nil, err
	}
	return state.Open(r.dbPath(), r.env.Now)
}

func cmdInit(r *runner, args []string) error {
	if _, err := parse(flags("init"), args, 0); err != nil {
		return err
	}
	if r.home == "" {
		return usageError{"no home: pass --home DIR or set HAND_HOME"}
	}
	_, statErr := os.Stat(r.dbPath())
	created := errors.Is(statErr, fs.ErrNotExist)
	if err := memory.Init(r.home); err != nil {
		return err
	}
	st, err := state.Open(r.dbPath(), r.env.Now)
	if err != nil {
		return err
	}
	if err := st.Close(); err != nil {
		return err
	}
	var d toon.Doc
	d.Field("home", r.home)
	d.Bool("created", created)
	d.Help("Write your durable preferences in "+filepath.Join(r.home, "memory", memory.OperatorFile),
		"Register a repository: `hand project add NAME ABSOLUTE_REPO_PATH`")
	return r.print(&d)
}
