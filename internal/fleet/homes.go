package fleet

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/state"
)

const (
	HomeFile = "fleet.home"
	homeLock = ".fleet.home.lock"
)

type HomesRecord struct {
	Homes   []string `json:"homes"`
	Current string   `json:"current"`
}

func ReadHomes(home string) (HomesRecord, error) {
	path := filepath.Join(home, HomeFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return HomesRecord{}, nil
	}
	if err != nil {
		return HomesRecord{}, err
	}
	var rec HomesRecord
	if err := json.Unmarshal(b, &rec); err != nil || rec.Current == "" {
		return HomesRecord{}, nil
	}
	return rec, nil
}

func WriteHomes(home, current string) error {
	if current == "" {
		current = home
	}
	unlock, err := lockHomes(home)
	if err != nil {
		return err
	}
	defer unlock()
	rec, err := ReadHomes(home)
	if err != nil {
		return err
	}
	if rec.Current != "" && rec.Current != current && sameDir(rec.Current, current) {
		current = rec.Current
	}
	if rec.Current == current && slices.Contains(rec.Homes, current) {
		return nil
	}
	homes := make([]string, 0, len(rec.Homes)+2)
	add := func(dir string) {
		if dir != "" && !slices.Contains(homes, dir) {
			homes = append(homes, dir)
		}
	}
	for _, dir := range rec.Homes {
		add(dir)
	}
	add(rec.Current)
	add(current)
	rec.Homes, rec.Current = homes, current
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp := filepath.Join(home, "."+HomeFile+"."+rand.Text())
	if err := writeTemp(tmp, append(b, '\n')); err != nil {
		return err
	}
	return renameSynced(tmp, filepath.Join(home, HomeFile))
}

func lockHomes(home string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(home, homeLock), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := flock.Lock(f, true); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = flock.Release(f) }, nil
}

func writeTemp(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
	}
	return werr
}

func renameSynced(tmp, dst string) error {
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncDir(filepath.Dir(dst))
}

func RefuseCopies(id string, homes []string) error {
	for _, home := range homes {
		db := filepath.Join(home, "hand.db")
		info, err := os.Stat(db)
		switch {
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrPermission), errors.Is(err, syscall.ENOTDIR):
			continue
		case err != nil:
			return fmt.Errorf("cannot look for a copy at %s: %v; if it is not this fleet's, remove it from %s", home, err, HomeFile)
		case !info.Mode().IsRegular():
			continue
		}
		other, err := copyID(db)
		switch {
		case err != nil:
			return fmt.Errorf("cannot read %s to tell whether it is a copy of fleet %s: %v; move or delete that file, or remove %s from %s", db, id, err, home, HomeFile)
		case other == id:
			return fmt.Errorf("fleet %s is also at %s, so one of the two homes is a copy; delete one of them", id, home)
		}
	}
	return nil
}

func copyID(db string) (string, error) {
	st, err := state.OpenRead(db)
	if err != nil {
		return "", err
	}
	defer st.Close()
	f, err := st.Fleet(context.Background())
	switch {
	case errors.Is(err, state.ErrNotFound), err != nil && strings.Contains(err.Error(), "no such table"):
		return "", nil
	}
	return f.ID, err
}
