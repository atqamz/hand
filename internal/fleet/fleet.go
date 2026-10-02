package fleet

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

func LuvusUnit(id string) string { return "secondhand-luvus-" + id }

func WatchUnit(id string) string { return "secondhand-watch-" + id }

func Worktrees(root, id string) string { return filepath.Join(root, "worktrees", id) }

func Check(root, id, home string) error {
	target, err := readLink(root, id)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := claim(root, id, home); errors.Is(err, fs.ErrExist) {
			return Check(root, id, home)
		} else if err != nil {
			return err
		}
		return nil
	case err != nil:
		return err
	case target == home:
		return nil
	case sameDir(target, home):
		return point(root, id, home)
	}
	if err := refuseCopy(target, id); err != nil {
		return err
	}
	return fmt.Errorf("%w: fleet %s moved here from %s; run `hand init` to adopt this place", state.ErrConflict, id, target)
}

func Register(root, id, home string) (string, error) {
	target, err := readLink(root, id)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := claim(root, id, home); errors.Is(err, fs.ErrExist) {
			return Register(root, id, home)
		} else if err != nil {
			return "", err
		}
		return "", nil
	case err != nil:
		return "", err
	case target == home && !isSymlink(root, id):
		return "", nil
	case target == home || sameDir(target, home):
		return "", point(root, id, home)
	}
	if err := refuseCopy(target, id); err != nil {
		return "", err
	}
	return target, point(root, id, home)
}

func Moved(root, id, home string) (string, error) {
	target, err := readLink(root, id)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", nil
	case err != nil:
		return "", err
	case target == home || sameDir(target, home):
		return "", nil
	}
	if err := refuseCopy(target, id); err != nil {
		return "", err
	}
	return target, nil
}

func Home(root, id string) (string, error) {
	target, err := readLink(root, id)
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
		target, err := readLink(root, de.Name())
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

func readLink(root, id string) (string, error) {
	if target, err := os.Readlink(link(root, id)); err == nil {
		return target, nil
	}
	b, err := os.ReadFile(link(root, id))
	target := strings.TrimSpace(string(b))
	if err == nil && target == "" {
		err = fmt.Errorf("%w: %s is empty; delete it and run `hand init` in the fleet", state.ErrInvalid, link(root, id))
	}
	return target, err
}

func isSymlink(root, id string) bool {
	info, err := os.Lstat(link(root, id))
	return err == nil && info.Mode()&fs.ModeSymlink != 0
}

func stage(root, id, home string) (string, error) {
	dir := filepath.Join(root, "fleets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp := filepath.Join(dir, "."+id+"."+rand.Text())
	if err := os.WriteFile(tmp, []byte(home+"\n"), 0o600); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

func claim(root, id, home string) error {
	tmp, err := stage(root, id, home)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	return os.Link(tmp, link(root, id))
}

func point(root, id, home string) error {
	tmp, err := stage(root, id, home)
	if err != nil {
		return err
	}
	err = os.Rename(tmp, link(root, id))
	if err != nil && isSymlink(root, id) && os.Remove(link(root, id)) == nil {
		err = os.Rename(tmp, link(root, id))
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

func sameDir(a, b string) bool {
	x, err := os.Stat(a)
	if err != nil {
		return false
	}
	y, err := os.Stat(b)
	return err == nil && os.SameFile(x, y)
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
