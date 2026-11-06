package fleet

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
	case target == home:
		return "", nil
	case sameDir(target, home):
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

func Resolve(root, ref string) (Entry, error) {
	if state.FleetID.MatchString(ref) {
		entry, ok, err := resolveID(root, ref)
		if err != nil {
			return Entry{}, err
		}
		if ok {
			return entry, nil
		}
	}
	des, err := os.ReadDir(filepath.Join(root, "fleets"))
	if errors.Is(err, fs.ErrNotExist) {
		return Entry{}, fmt.Errorf("%w: no fleet %q is registered in %s", state.ErrInvalid, ref, root)
	}
	if err != nil {
		return Entry{}, err
	}
	var hits []Entry
	var ids []string
	for _, de := range des {
		id := de.Name()
		if !state.FleetID.MatchString(id) {
			continue
		}
		ids = append(ids, id)
		target, err := readLink(root, id)
		if err != nil {
			if ref == id {
				return Entry{}, err
			}
			continue
		}
		f, err := state.ReadFleet(target)
		if err != nil {
			if ref == id {
				return Entry{}, fmt.Errorf("%w: fleet %s at %s: %v", state.ErrInvalid, id, target, err)
			}
			continue
		}
		if f.ID != id {
			if ref == id {
				return Entry{}, fmt.Errorf("%w: fleet %s is registered at %s, which holds %s; run `hand init` there", state.ErrConflict, id, target, f.ID)
			}
			continue
		}
		if ref == id || ref == f.Name {
			hits = append(hits, Entry{ID: f.ID, Name: f.Name, Home: target, State: "ok"})
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		slices.Sort(ids)
		return Entry{}, fmt.Errorf("%w: no fleet %q is registered in %s (fleets: %s)", state.ErrInvalid, ref, root, strings.Join(ids, ", "))
	}
	slices.SortFunc(hits, func(a, b Entry) int { return strings.Compare(a.ID, b.ID) })
	names := make([]string, 0, len(hits))
	for _, h := range hits {
		names = append(names, h.ID)
	}
	return Entry{}, fmt.Errorf("%w: %q names more than one fleet: %s; use the fleet id", state.ErrConflict, ref, strings.Join(names, ", "))
}

func resolveID(root, id string) (Entry, bool, error) {
	target, err := readLink(root, id)
	if errors.Is(err, fs.ErrNotExist) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	f, err := state.ReadFleet(target)
	if err != nil {
		return Entry{}, false, fmt.Errorf("%w: fleet %s at %s: %v", state.ErrInvalid, id, target, err)
	}
	if f.ID != id {
		return Entry{}, false, fmt.Errorf("%w: fleet %s is registered at %s, which holds %s; run `hand init` there", state.ErrConflict, id, target, f.ID)
	}
	return Entry{ID: f.ID, Name: f.Name, Home: target, State: "ok"}, true, nil
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
			out = append(out, Entry{ID: de.Name(), State: "unreadable: " + err.Error()})
			continue
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
	b, err := os.ReadFile(link(root, id))
	target := strings.TrimSpace(string(b))
	if err == nil && target == "" {
		err = fmt.Errorf("%w: %s is empty; delete it and run `hand init` in the fleet", state.ErrInvalid, link(root, id))
	}
	if err != nil {
		if info, lstatErr := os.Lstat(link(root, id)); lstatErr == nil && info.Mode()&fs.ModeSymlink != 0 {
			err = fmt.Errorf("%w: %s is a symlink left by Hand before 0.10, which this hand does not read; delete it and run `hand init` in the fleet's folder", state.ErrInvalid, link(root, id))
		}
	}
	return target, err
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
