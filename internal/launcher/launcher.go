package launcher

import (
	"bytes"
	"os"
	"path/filepath"
)

type State string

const (
	OK        State = "ok"
	Missing   State = "missing"
	Elsewhere State = "elsewhere"
	Foreign   State = "foreign"
)

func put(path string, want []byte) (bool, error) {
	if have, err := os.ReadFile(path); err == nil && bytes.Equal(have, want) {
		return false, nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	f, err := os.CreateTemp(dir, ".hand-desktop.*")
	if err != nil {
		return false, err
	}
	_, err = f.Write(want)
	if err == nil {
		err = f.Chmod(0o644)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return false, err
	}
	return true, nil
}
