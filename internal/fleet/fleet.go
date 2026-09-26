package fleet

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/atqamz/hand/internal/state"
)

type Entry struct {
	ID, Name, Home, State string
}

func Root(getenv func(string) string) (string, error) {
	root := getenv("SECONDHAND_HOME")
	if root == "" {
		if getenv("HOME") == "" {
			return "", fmt.Errorf("%w: set HOME or SECONDHAND_HOME", state.ErrInvalid)
		}
		root = filepath.Join(getenv("HOME"), ".secondhand")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(root)
}

func Session(id string) string { return "secondhand-" + id }

func Worktrees(root, id string) string { return filepath.Join(root, "worktrees", id) }

func Check(root, id, home string) error {
	target, err := os.Readlink(link(root, id))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return point(root, id, home)
	case err != nil:
		return err
	case target == home:
		return nil
	}
	if err := refuseCopy(target, id); err != nil {
		return err
	}
	return fmt.Errorf("%w: fleet %s moved here from %s; run `hand init` to adopt this place", state.ErrConflict, id, target)
}

func Register(root, id, home string) (string, error) {
	target, err := os.Readlink(link(root, id))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", point(root, id, home)
	case err != nil:
		return "", err
	case target == home:
		return "", nil
	}
	if err := refuseCopy(target, id); err != nil {
		return "", err
	}
	return target, point(root, id, home)
}

func Home(root, id string) (string, error) {
	target, err := os.Readlink(link(root, id))
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%w: no fleet %s is registered in %s", state.ErrInvalid, id, root)
	}
	return target, err
}

func List(root string) ([]Entry, error) {
	des, err := os.ReadDir(filepath.Join(root, "fleets"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, de := range des {
		if !state.FleetID.MatchString(de.Name()) {
			continue
		}
		target, err := os.Readlink(link(root, de.Name()))
		if err != nil {
			return nil, err
		}
		e := Entry{ID: de.Name(), Home: target, State: "missing"}
		f, ok, err := at(target)
		switch {
		case err != nil:
			e.State = "unreadable: " + err.Error()
		case ok && f.ID == e.ID:
			e.Name, e.State = f.Name, "ok"
		case ok:
			e.State = "moved"
		}
		out = append(out, e)
	}
	return out, nil
}

func link(root, id string) string { return filepath.Join(root, "fleets", id) }

func point(root, id, home string) error {
	dir := filepath.Join(root, "fleets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+id+"."+rand.Text())
	if err := os.Symlink(home, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link(root, id)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func refuseCopy(other, id string) error {
	f, ok, err := at(other)
	if err != nil {
		return err
	}
	if ok && f.ID == id {
		return fmt.Errorf("%w: fleet %s is also at %s, so one of the two homes is a copy; delete one of them", state.ErrConflict, id, other)
	}
	return nil
}

func at(home string) (state.Fleet, bool, error) {
	db := filepath.Join(home, "hand.db")
	if _, err := os.Stat(db); errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return state.Fleet{}, false, nil
	} else if err != nil {
		return state.Fleet{}, false, err
	}
	st, err := state.Open(db, time.Now)
	if err != nil {
		return state.Fleet{}, false, err
	}
	defer st.Close()
	f, err := st.Fleet(context.Background())
	if errors.Is(err, state.ErrNotFound) {
		return state.Fleet{}, false, nil
	}
	return f, err == nil, err
}
