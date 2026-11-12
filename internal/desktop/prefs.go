package desktop

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

const maxPrefs = 1 << 10

func PrefsPath(drafts string) string {
	if drafts == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(filepath.Clean(drafts)), "prefs.json")
}

type prefs struct {
	Zoom float32 `json:"zoom"`
}

func (a *App) loadPrefs() {
	path := PrefsPath(a.DraftDir)
	if path == "" {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxPrefs+1))
	if err != nil || len(raw) > maxPrefs {
		return
	}
	var p prefs
	if json.Unmarshal(raw, &p) != nil || !(p.Zoom > 0) {
		return
	}
	a.Zoom = zoomFactor(zoomSteps[zoomRung(p.Zoom)])
}

func (a *App) savePrefs() {
	path := PrefsPath(a.DraftDir)
	if path == "" {
		return
	}
	raw, err := json.Marshal(prefs{Zoom: a.Zoom})
	if err != nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.CreateTemp(dir, ".prefs-*")
	if err != nil {
		return
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return
	}
	if err := f.Close(); err != nil {
		return
	}
	_ = os.Rename(name, path)
}
