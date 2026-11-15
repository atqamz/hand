package desktop

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	maxPrefs        = 1 << 10
	firstH          = 760
	MinWindowHeight = 480
	prefsDelay      = 300 * time.Millisecond
)

func PrefsPath(drafts string) string {
	if drafts == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(filepath.Clean(drafts)), "prefs.json")
}

type prefs struct {
	Zoom   float32 `json:"zoom"`
	Chat   float32 `json:"chat,omitempty"`
	Width  int     `json:"width,omitempty"`
	Height int     `json:"height,omitempty"`
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
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	if p.Zoom > 0 {
		a.Zoom = zoomFactor(zoomSteps[zoomRung(p.Zoom)])
	}
	if p.Chat > 0 {
		a.Chat = max(p.Chat, minChat)
	}
	if p.Width > 0 && p.Height > 0 {
		a.WindowW, a.WindowH = p.Width, p.Height
	}
}

func (a *App) savePrefs() {
	path := PrefsPath(a.DraftDir)
	if path == "" {
		return
	}
	raw, err := json.Marshal(prefs{Zoom: a.Zoom, Chat: a.Chat, Width: a.WindowW, Height: a.WindowH})
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

func windowSize(storedW, storedH, areaW, areaH int) (int, int) {
	w, h := storedW, storedH
	if w <= 0 {
		w = firstW
	}
	if h <= 0 {
		h = firstH
	}
	if areaW > 0 {
		w = min(w, areaW)
	}
	if areaH > 0 {
		h = min(h, areaH)
	}
	return max(w, MinWindowWidth), max(h, MinWindowHeight)
}

func (a *App) WindowSize(areaW, areaH int) (int, int) {
	return windowSize(a.WindowW, a.WindowH, areaW, areaH)
}

func (a *App) SetWindowSize(w, h int) {
	if w <= 0 || h <= 0 || (a.WindowW == w && a.WindowH == h) {
		return
	}
	a.WindowW, a.WindowH = w, h
	a.settlePrefs()
}

func (a *App) settlePrefs() {
	if a.prefsTimer != nil {
		a.prefsTimer()
		a.prefsTimer = nil
	}
	if a.Post == nil {
		a.savePrefs()
		return
	}
	a.prefsGen++
	gen := a.prefsGen
	a.prefsTimer = a.clock().After(prefsDelay, func() {
		post := a.Post
		if post == nil {
			return
		}
		post(func() {
			if gen != a.prefsGen {
				return
			}
			a.prefsTimer = nil
			a.savePrefs()
		})
	})
}

func (a *App) flushPrefs() {
	if a.prefsTimer != nil {
		a.prefsTimer()
		a.prefsTimer = nil
	}
	a.prefsGen++
}
