package fleet

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/atqamz/hand/internal/flock"
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
		return err
	}
	return syncDir(filepath.Dir(dst))
}
