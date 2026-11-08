package desktop

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const maxDraft = 64 << 10

// DraftDir names the desktop's state folder for composer drafts.
func DraftDir(getenv func(string) string) string {
	if dir := strings.TrimSpace(getenv("XDG_STATE_HOME")); dir != "" {
		return filepath.Join(dir, "hand-desktop", "drafts")
	}
	if runtime.GOOS == "windows" {
		if dir := strings.TrimSpace(getenv("LOCALAPPDATA")); dir != "" {
			return filepath.Join(dir, "hand-desktop", "drafts")
		}
	}
	home := strings.TrimSpace(getenv("HOME"))
	if home == "" {
		home = strings.TrimSpace(getenv("USERPROFILE"))
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "state", "hand-desktop", "drafts")
}

type Drafts struct {
	Dir string
}

func (d Drafts) path(fleet string) (string, error) {
	if !fleetIDShape.MatchString(fleet) {
		return "", fmt.Errorf("%q is not a fleet id", fleet)
	}
	if d.Dir == "" {
		return "", errors.New("the desktop has no draft folder")
	}
	return filepath.Join(d.Dir, fleet+".txt"), nil
}

func (d Drafts) Load(fleet string) string {
	path, err := d.path(fleet)
	if err != nil {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxDraft+1))
	if err != nil || len(raw) > maxDraft {
		return ""
	}
	return string(raw)
}

func (d Drafts) Save(fleet, text string) error {
	path, err := d.path(fleet)
	if err != nil {
		return err
	}
	if len(text) > maxDraft {
		return fmt.Errorf("the draft is %d bytes; the most is %d", len(text), maxDraft)
	}
	if err := os.MkdirAll(d.Dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(d.Dir, ".draft-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	_, err = io.WriteString(tmp, text)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (d Drafts) Clear(fleet string) error {
	path, err := d.path(fleet)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
