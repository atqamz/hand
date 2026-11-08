package desktop

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	return d.remove(path)
}

// ClearIf removes the draft only while it still holds text, so a send cannot
// delete a newer draft another window saved meanwhile.
func (d Drafts) ClearIf(fleet, text string) error {
	path, err := d.path(fleet)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if string(raw) != text {
		return nil
	}
	return d.remove(path)
}

func (d Drafts) remove(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// draftWriter writes drafts on one goroutine, keeping only the latest text per
// fleet and running a clear after the writes it follows. Without a window it
// writes inline, as Boot reads.
type draftWriter struct {
	drafts Drafts
	report func(fleet string, err error)
	inline bool
	mu     sync.Mutex
	next   map[string]string
	failed map[string]draftFailure
	wake   chan struct{}
	clears chan draftClear
	flush  chan chan struct{}
}

// draftFailure is the text a save refused, kept so a fleet switch can show it.
type draftFailure struct {
	text string
	err  error
}

type draftClear struct {
	fleet string
	text  string
	only  bool
	reply chan error
}

func newDraftWriter(d Drafts, report func(fleet string, err error), inline bool) *draftWriter {
	w := &draftWriter{
		drafts: d,
		report: report,
		inline: inline,
		next:   map[string]string{},
		failed: map[string]draftFailure{},
		wake:   make(chan struct{}, 1),
		clears: make(chan draftClear),
		flush:  make(chan chan struct{}),
	}
	if !inline {
		go w.run()
	}
	return w
}

func (w *draftWriter) Save(fleet, text string) {
	if w.inline {
		err := w.drafts.Save(fleet, text)
		w.record(fleet, text, err)
		w.report(fleet, err)
		return
	}
	w.mu.Lock()
	w.next[fleet] = text
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *draftWriter) Clear(fleet string) error {
	if w.inline {
		return w.done(fleet, w.drafts.Clear(fleet))
	}
	return w.request(draftClear{fleet: fleet, reply: make(chan error, 1)}, true)
}

func (w *draftWriter) ClearIf(fleet, text string) error {
	if w.inline {
		return w.done(fleet, w.drafts.ClearIf(fleet, text))
	}
	return w.request(draftClear{fleet: fleet, text: text, only: true, reply: make(chan error, 1)}, false)
}

// unsaved reports the text a save refused, as a fleet switch reads it.
func (w *draftWriter) unsaved(fleet string) (draftFailure, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	u, ok := w.failed[fleet]
	return u, ok
}

func (w *draftWriter) Flush() {
	if w.inline {
		return
	}
	done := make(chan struct{})
	w.flush <- done
	<-done
}

func (w *draftWriter) request(op draftClear, drop bool) error {
	if drop {
		w.mu.Lock()
		delete(w.next, op.fleet)
		w.mu.Unlock()
	}
	w.clears <- op
	return <-op.reply
}

func (w *draftWriter) run() {
	for {
		if w.writeOne() {
			continue
		}
		select {
		case <-w.wake:
		case op := <-w.clears:
			for w.writeOne() {
			}
			op.reply <- w.clear(op)
		case done := <-w.flush:
			close(done)
		}
	}
}

func (w *draftWriter) writeOne() bool {
	w.mu.Lock()
	fleet, text, ok := "", "", false
	for f, t := range w.next {
		fleet, text, ok = f, t, true
		delete(w.next, f)
		break
	}
	w.mu.Unlock()
	if !ok {
		return false
	}
	err := w.drafts.Save(fleet, text)
	w.record(fleet, text, err)
	w.report(fleet, err)
	return true
}

func (w *draftWriter) clear(op draftClear) error {
	if op.only {
		return w.done(op.fleet, w.drafts.ClearIf(op.fleet, op.text))
	}
	return w.done(op.fleet, w.drafts.Clear(op.fleet))
}

func (w *draftWriter) done(fleet string, err error) error {
	if err == nil {
		w.mu.Lock()
		delete(w.failed, fleet)
		w.mu.Unlock()
	}
	return err
}

func (w *draftWriter) record(fleet, text string, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil {
		w.failed[fleet] = draftFailure{text: text, err: err}
		return
	}
	delete(w.failed, fleet)
}
