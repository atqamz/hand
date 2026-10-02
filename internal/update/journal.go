package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/state"
)

type journal struct {
	Watch      []string `json:"watch,omitempty"`
	Supervisor []string `json:"supervisor,omitempty"`
	Board      []string `json:"board,omitempty"`
	Init       []string `json:"init,omitempty"`
}

func journalPath(root string) string { return filepath.Join(root, "update.json") }

func loadJournal(root string) (journal, bool, error) {
	var j journal
	b, err := os.ReadFile(journalPath(root))
	if errors.Is(err, fs.ErrNotExist) {
		return j, false, nil
	}
	if err != nil {
		return j, false, err
	}
	if err := json.Unmarshal(b, &j); err != nil {
		return j, false, fmt.Errorf("update: %s: %w", journalPath(root), err)
	}
	return j, true, nil
}

func (j journal) save(root string) error {
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	tmp := journalPath(root) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, journalPath(root))
}

func (j journal) describe() string {
	var steps []string
	for _, u := range j.Watch {
		steps = append(steps, "start "+u)
	}
	for _, h := range j.Supervisor {
		steps = append(steps, "resume the supervisor in "+h)
	}
	for _, u := range j.Board {
		steps = append(steps, "restart "+u)
	}
	for _, h := range j.Init {
		steps = append(steps, "run hand init in "+h)
	}
	return "A previous hand update stopped part way; `hand update` finishes it: " + strings.Join(steps, ", ")
}

func without(list []string, items ...string) []string {
	return slices.DeleteFunc(slices.Clone(list), func(s string) bool { return slices.Contains(items, s) })
}

func (j journal) empty() bool {
	return len(j.Watch)+len(j.Supervisor)+len(j.Board)+len(j.Init) == 0
}

func (r *Report) record(o Options, edit func(*journal)) error {
	next := journal{Watch: slices.Clone(r.journal.Watch), Supervisor: slices.Clone(r.journal.Supervisor), Board: slices.Clone(r.journal.Board), Init: slices.Clone(r.journal.Init)}
	edit(&next)
	if err := next.save(o.Root); err != nil {
		return err
	}
	r.journal = next
	return nil
}

func (r *Report) unrecord(o Options, edit func(*journal)) {
	if err := r.record(o, edit); err != nil {
		r.Help = append(r.Help, fmt.Sprintf("could not write %s (%v)", journalPath(o.Root), err))
	}
}

func (r *Report) finishJournal(ctx context.Context, o Options, hold func()) error {
	left, found, err := loadJournal(o.Root)
	switch {
	case err != nil && o.Check:
		r.Help = append(r.Help, fmt.Sprintf("could not read %s (%v); the next hand update sets it aside", journalPath(o.Root), err))
		return nil
	case err != nil:
		r.Help = append(r.Help, fmt.Sprintf("could not read %s (%v); set it aside as %s.bad", journalPath(o.Root), err, journalPath(o.Root)))
		return os.Rename(journalPath(o.Root), journalPath(o.Root)+".bad")
	case !found:
		return nil
	case left.empty() && o.Check:
		return nil
	case left.empty():
		return os.Remove(journalPath(o.Root))
	case o.Check:
		r.Help = append(r.Help, left.describe())
		return nil
	}
	hold()
	r.repair(ctx, o, left)
	r.Status = "repaired"
	return os.Remove(journalPath(o.Root))
}

func (r *Report) repair(ctx context.Context, o Options, j journal) {
	for _, u := range j.Watch {
		r.unit(ctx, o, u, "start")
	}
	for _, home := range j.Supervisor {
		if live, err := supervisorLive(ctx, home, o.Now); err == nil && live {
			continue
		}
		if err := child(ctx, o, home, "supervisor", "resume"); err != nil {
			r.fail(fmt.Sprintf("`hand supervisor resume` failed in %s (%v); run it there", home, err))
		}
	}
	for _, u := range j.Board {
		r.unit(ctx, o, u, "restart")
	}
	for _, home := range j.Init {
		if err := child(ctx, o, home, "init"); err != nil {
			r.fail(fmt.Sprintf("`hand init` failed in %s (%v); run it there", home, err))
		}
	}
}

func supervisorLive(ctx context.Context, home string, now func() time.Time) (bool, error) {
	st, err := state.Open(filepath.Join(home, "hand.db"), now)
	if err != nil {
		return false, err
	}
	defer st.Close()
	_, live, err := st.LiveSupervisor(ctx)
	return live, err
}
