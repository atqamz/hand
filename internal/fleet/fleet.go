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

	"github.com/atqamz/hand/internal/flock"
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

func IndexDir(root string) string { return filepath.Join(root, "index") }

func IDs(root string) ([]string, error) { return registeredIDs(root) }

func Scratch(home string) string { return filepath.Join(home, "scratch") }

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
		if err := refuseRecordCopy(home, id); err != nil {
			return err
		}
		unlock, err := lockIndex(root)
		if err != nil {
			return err
		}
		defer unlock()
		return WriteHomes(home, home)
	case sameDir(target, home):
		return point(root, id, home, "")
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
		if err := refuseRecordCopy(home, id); err != nil {
			return "", err
		}
		unlock, err := lockIndex(root)
		if err != nil {
			return "", err
		}
		defer unlock()
		return "", WriteHomes(home, home)
	case sameDir(target, home):
		return "", point(root, id, home, "")
	}
	if err := refuseCopy(target, id); err != nil {
		return "", err
	}
	return target, point(root, id, home, target)
}

func Moved(root, id, home string) (string, error) {
	target, err := readLink(root, id)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", refuseRecordCopy(home, id)
	case err != nil:
		return "", err
	case target == home || sameDir(target, home):
		return "", refuseRecordCopy(home, id)
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
	ids, err := registeredIDs(root)
	if errors.Is(err, fs.ErrNotExist) {
		return Entry{}, fmt.Errorf("%w: no fleet %q is registered in %s", state.ErrInvalid, ref, root)
	}
	if err != nil {
		return Entry{}, err
	}
	if state.FleetID.MatchString(ref) {
		entry, ok, err := resolveID(root, ref)
		if err != nil {
			return Entry{}, err
		}
		if ok {
			return entry, nil
		}
		return Entry{}, fmt.Errorf("%w: no fleet %q is registered in %s (fleets: %s)", state.ErrInvalid, ref, root, strings.Join(ids, ", "))
	}
	var hits []Entry
	for _, id := range ids {
		target, err := readLink(root, id)
		if err != nil {
			continue
		}
		f, err := state.ReadFleet(target)
		if err != nil {
			continue
		}
		if f.ID != id {
			continue
		}
		if ref == f.Name {
			hits = append(hits, Entry{ID: f.ID, Name: f.Name, Home: target, State: "ok"})
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return Entry{}, fmt.Errorf("%w: no fleet %q is registered in %s (fleets: %s)", state.ErrInvalid, ref, root, strings.Join(ids, ", "))
	}
	slices.SortFunc(hits, func(a, b Entry) int { return strings.Compare(a.ID, b.ID) })
	names := make([]string, 0, len(hits))
	for _, h := range hits {
		names = append(names, h.ID)
	}
	return Entry{}, fmt.Errorf("%w: %q names more than one fleet: %s; use the fleet id", state.ErrConflict, ref, strings.Join(names, ", "))
}

func bookmarkIDs(root string) ([]string, error) {
	seen := map[string]bool{}
	for _, dir := range []string{IndexDir(root), filepath.Join(root, "fleets")} {
		if info, err := os.Stat(dir); err == nil && !info.IsDir() {
			return nil, fmt.Errorf("%w: %s is not a directory; move it aside, then run `hand fleet list` again", state.ErrInvalid, dir)
		}
		des, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, de := range des {
			if state.FleetID.MatchString(de.Name()) {
				seen[de.Name()] = true
			}
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

func registeredIDs(root string) ([]string, error) {
	unlock, err := lockIndex(root)
	if err != nil {
		return nil, err
	}
	defer unlock()
	migrate(root)
	return bookmarkIDs(root)
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
	unlock, err := lockIndex(root)
	if err != nil {
		return nil, err
	}
	migrate(root)
	ids, err := bookmarkIDs(root)
	unlock()
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, id := range ids {
		unlock, err := lockIndex(root)
		if err != nil {
			return nil, err
		}
		target, lerr := readLinkLocked(root, id)
		var rec HomesRecord
		var rerr error
		if lerr == nil {
			rec, rerr = ReadHomes(target)
		}
		unlock()
		if errors.Is(lerr, fs.ErrNotExist) {
			continue
		}
		if lerr != nil {
			out = append(out, Entry{ID: id, State: "unreadable: " + lerr.Error()})
			continue
		}
		e := Entry{ID: id, Home: target, State: "missing"}
		f, ok, err := at(target)
		switch {
		case err != nil:
			e.State = "unreadable: " + err.Error()
		case ok && f.ID == e.ID:
			e.Name, e.State = f.Name, "ok"
			if rerr != nil {
				e.State = "unreadable: " + rerr.Error()
			} else if rec.Current != "" && !sameDir(rec.Current, target) {
				e.State = "moved"
			}
		case ok:
			e.State = "moved"
		}
		out = append(out, e)
	}
	return out, nil
}

func bookmark(root, id string) string { return filepath.Join(IndexDir(root), id) }

func legacyBookmark(root, id string) string { return filepath.Join(root, "fleets", id) }

func lockIndex(root string) (func(), error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(root, "index.lock")
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if denied(err) {
		if lock, err = os.OpenFile(path, os.O_RDONLY, 0); errors.Is(err, fs.ErrNotExist) {
			return func() {}, nil
		}
	}
	if err != nil {
		return nil, err
	}
	if _, err := flock.Lock(lock, true); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return func() { _ = flock.Release(lock) }, nil
}

func denied(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EROFS)
}

func migrate(root string) {
	legacy := filepath.Join(root, "fleets")
	des, err := os.ReadDir(legacy)
	if err != nil {
		return
	}
	dir := IndexDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	for _, de := range des {
		if !state.FleetID.MatchString(de.Name()) {
			continue
		}
		dst := filepath.Join(dir, de.Name())
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		_ = os.Rename(filepath.Join(legacy, de.Name()), dst)
	}
	if rest, err := os.ReadDir(legacy); err == nil && len(rest) == 0 {
		_ = os.Remove(legacy)
	}
}

func readLink(root, id string) (string, error) {
	unlock, err := lockIndex(root)
	if err != nil {
		return "", err
	}
	defer unlock()
	return readLinkLocked(root, id)
}

func readLinkLocked(root, id string) (string, error) {
	migrate(root)
	path := bookmark(root, id)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if _, lstatErr := os.Lstat(path); errors.Is(lstatErr, fs.ErrNotExist) {
			path = legacyBookmark(root, id)
			b, err = os.ReadFile(path)
		}
	}
	target := strings.TrimSpace(string(b))
	if err == nil && target == "" {
		err = fmt.Errorf("%w: %s is empty; delete it and run `hand init` in the fleet", state.ErrInvalid, path)
	}
	if err != nil {
		if info, lstatErr := os.Lstat(path); lstatErr == nil && info.Mode()&fs.ModeSymlink != 0 {
			err = fmt.Errorf("%w: %s is a symlink left by Hand before 0.10, which this hand does not read; delete it and run `hand init` in the fleet's folder", state.ErrInvalid, path)
		}
	}
	return target, err
}

func stage(root, id, home string) (string, error) {
	dir := IndexDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp := filepath.Join(dir, "."+id+"."+rand.Text())
	if err := writeTemp(tmp, []byte(home+"\n")); err != nil {
		return "", err
	}
	return tmp, nil
}

func claim(root, id, home string) error {
	unlock, err := lockIndex(root)
	if err != nil {
		return err
	}
	defer unlock()
	migrate(root)
	if _, err := os.Stat(bookmark(root, id)); err == nil {
		return fs.ErrExist
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := refuseRecordCopy(home, id); err != nil {
		return err
	}
	tmp, err := stage(root, id, home)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err := renameSynced(tmp, bookmark(root, id)); err != nil {
		return err
	}
	return WriteHomes(home, home)
}

func point(root, id, home, old string) error {
	unlock, err := lockIndex(root)
	if err != nil {
		return err
	}
	defer unlock()
	migrate(root)
	if err := refuseRecordCopy(home, id); err != nil {
		return err
	}
	if rec, err := ReadHomes(home); err != nil {
		return err
	} else if rec.Current == "" && old != "" {
		if err := WriteHomes(home, old); err != nil {
			return err
		}
	}
	tmp, err := stage(root, id, home)
	if err != nil {
		return err
	}
	if err := renameSynced(tmp, bookmark(root, id)); err != nil {
		return err
	}
	return WriteHomes(home, home)
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

func refuseRecordCopy(home, id string) error {
	rec, err := ReadHomes(home)
	if err != nil {
		return err
	}
	if rec.Current == "" || sameDir(rec.Current, home) {
		return nil
	}
	f, ok, err := at(rec.Current)
	if err != nil {
		return err
	}
	if ok && f.ID == id {
		return fmt.Errorf("%w: fleet %s's record names %s as its current home, and that folder still holds it, so %s is a copy; delete one of them", state.ErrConflict, id, rec.Current, home)
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
