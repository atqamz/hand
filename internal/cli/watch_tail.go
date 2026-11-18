package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atqamz/hand/internal/fsx"
	"github.com/atqamz/hand/internal/redact"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

const (
	tailEntries = 6
	tailBytes   = 3072

	lastTurnsTemp = ".last-turns.md-"
)

var errNoTurns = errors.New("the session holds no operator or supervisor turn")

func lastTurnsPath(home string) string { return filepath.Join(home, "memory", "last-turns.md") }

func (w *watcher) tailOf(ctx context.Context, rd *transcript.Reader, sup state.Supervisor) (string, error) {
	entries, err := rd.Read(ctx, sup.Harness, sup.Session, w.transcriptDir(sup.Harness))
	if err != nil {
		return "", err
	}
	text := lastTurns(state.SupervisorRef(sup.ID), w.r.env.Now(), entries)
	if text == "" {
		return "", errNoTurns
	}
	return text, nil
}

func lastTurns(ref string, now time.Time, entries []transcript.Entry) string {
	var kept []transcript.Entry
	for _, e := range entries {
		if e.Role == "operator" || e.Role == "supervisor" {
			kept = append(kept, e)
		}
	}
	kept = kept[max(0, len(kept)-tailEntries):]
	var parts []string
	left := tailBytes
	for i := len(kept) - 1; i >= 0 && left > 0; i-- {
		text := strings.TrimSpace(strings.ReplaceAll(redact.Text(kept[i].Text), "\r\n", "\n"))
		if text == "" {
			continue
		}
		if len(text) > left {
			text, left = cutMiddle(text, left), 0
		} else {
			left -= len(text)
		}
		if text == "" {
			continue
		}
		head := "## " + kept[i].Role
		if kept[i].At != "" {
			head += ", " + kept[i].At
		}
		parts = append([]string{head + "\n\n" + text}, parts...)
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("# Last turns of %s\n\nHand copied these from the session %s ended at %s. Verbatim, newest last. Known secret shapes are removed and long text is cut; tool calls and thinking are not included. This is not a summary.\n\n%s\n", ref, ref, now.UTC().Format("2006-01-02T15:04:05Z"), strings.Join(parts, "\n\n"))
}

func cutMiddle(text string, left int) string {
	const marker = "\n[… %d bytes cut …]\n"
	keep := left - len(fmt.Sprintf(marker, len(text)))
	if keep <= 0 {
		return ""
	}
	head := keep / 3
	for head > 0 && !utf8.RuneStart(text[head]) {
		head--
	}
	tail := len(text) - (keep - keep/3)
	for tail < len(text) && !utf8.RuneStart(text[tail]) {
		tail++
	}
	return text[:head] + fmt.Sprintf(marker, tail-head) + text[tail:]
}

func writeLastTurns(home, text string) error {
	dir := filepath.Join(home, "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, lastTurnsTemp)
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil && !errors.Is(err, os.ErrPermission) {
		f.Close()
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return fsx.Rename(f.Name(), lastTurnsPath(home))
}

func (w *watcher) settleTail(tail string, why error) string {
	path := lastTurnsPath(w.r.home)
	if why == nil {
		if why = writeLastTurns(w.r.home, tail); why == nil {
			return path
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		why = fmt.Errorf("%w; the old file stays: %v", why, err)
	}
	w.say("supervisor rollover: last turns not carried: " + why.Error())
	return ""
}
