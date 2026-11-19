package board

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/state"
)

type PhoneRecord struct {
	state.Device
	Seen    time.Time
	Expired bool
}

type Lister interface {
	PhoneRecords(ctx context.Context) ([]PhoneRecord, error)
}

type Revoker interface {
	RevokePhone(ctx context.Context, rv *Revocation) (int, error)
}

type Revocation struct{ fleet, label string }

type (
	listFunc   func(ctx context.Context, fleet string) ([]PhoneRecord, error)
	revokeFunc func(ctx context.Context, fleet, label string) (int, error)
)

type (
	listKey   struct{}
	revokeKey struct{}
)

var (
	errNotRevocable = errors.New("this revocation does not name this fleet")
	errNotProven    = errors.New("this browser is not signed in to that fleet")
)

type phoneAt struct {
	ID, Name         string
	Current, Expired bool
}

type phoneEntry struct {
	PhoneRecord
	Fleets []phoneAt
}

type revokeNote struct {
	Text, Action, Fleet, Phone, Button string
}

func (b *Board) PhoneRecords(ctx context.Context) ([]PhoneRecord, error) {
	devices, err := b.st.Devices(ctx)
	if err != nil {
		return nil, err
	}
	b.companions.mu.Lock()
	defer b.companions.mu.Unlock()
	out := make([]PhoneRecord, len(devices))
	for i, d := range devices {
		out[i] = PhoneRecord{d, b.companions.seen[d.ID], b.lapsed(d)}
	}
	return out, nil
}

func (b *Board) RevokePhone(ctx context.Context, rv *Revocation) (int, error) {
	if rv == nil || rv.fleet == "" || rv.fleet != b.fleetID() {
		return 0, errNotRevocable
	}
	return b.revokeLabel(ctx, rv.label)
}

func (b *Board) revokeLabel(ctx context.Context, label string) (int, error) {
	gone, err := b.st.RevokePhone(ctx, label)
	b.companions.mu.Lock()
	for _, d := range gone {
		delete(b.companions.seen, d.ID)
	}
	b.companions.mu.Unlock()
	if err == nil {
		b.closePeers(gone...)
	}
	return len(gone), err
}

func (b *Board) revokeDevice(ctx context.Context, id int64) (*state.Device, error) {
	d, err := b.st.RevokeDevice(ctx, id)
	missing := errors.Is(err, state.ErrNotFound)
	if err != nil && !missing {
		return nil, err
	}
	b.companions.mu.Lock()
	delete(b.companions.seen, id)
	b.companions.mu.Unlock()
	if missing {
		return nil, nil
	}
	return &d, nil
}

type revokeFailure struct {
	railLink
	err error
}

func (b *Board) revokeAcross(ctx context.Context, targets []railLink, label string) (done []railLink, failed []revokeFailure) {
	detached := context.WithoutCancel(ctx)
	for _, l := range targets {
		work, cancel := context.WithTimeout(detached, pairWait)
		n, err := b.revokeFleet(work, l.ID, label)
		cancel()
		switch {
		case err != nil:
			failed = append(failed, revokeFailure{l, err})
		case n > 0:
			done = append(done, l)
		}
	}
	return done, failed
}

func (b *Board) revokeFleet(ctx context.Context, id, label string) (int, error) {
	if id == b.fleetID() {
		return b.revokeLabel(ctx, label)
	}
	if revoke, ok := ctx.Value(revokeKey{}).(revokeFunc); ok {
		return revoke(ctx, id, label)
	}
	return 0, errors.New("this board cannot reach another fleet")
}

func (b *Board) phoneEntries(ctx context.Context, own []PhoneRecord) ([]phoneEntry, []string) {
	list, _ := ctx.Value(listKey{}).(listFunc)
	var out []*phoneEntry
	byLabel := map[string]*phoneEntry{}
	var unread []string
	add := func(l railLink, r PhoneRecord) {
		if r.Phone == "" {
			if l.Current {
				out = append(out, &phoneEntry{PhoneRecord: r})
			}
			return
		}
		at := phoneAt{ID: l.ID, Name: l.Name, Current: l.Current, Expired: r.Expired}
		if e := byLabel[r.Phone]; e != nil {
			e.Fleets = append(e.Fleets, at)
			e.Seen = laterOf(e.Seen, r.Seen)
			if r.PairedAt.Before(e.PairedAt) {
				e.PairedAt = r.PairedAt
			}
			return
		}
		e := &phoneEntry{PhoneRecord: r, Fleets: []phoneAt{at}}
		byLabel[r.Phone] = e
		out = append(out, e)
	}
	for _, l := range b.hostFleets(ctx) {
		records := own
		if !l.Current {
			var err error
			if list != nil {
				records, err = list(ctx, l.ID)
			}
			if list == nil || err != nil {
				unread = append(unread, l.Name)
				continue
			}
		}
		for _, r := range records {
			add(l, r)
		}
	}
	entries := make([]phoneEntry, len(out))
	for i, e := range out {
		if len(e.Fleets) == 1 && e.Fleets[0].Current {
			e.Fleets = nil
		}
		entries[i] = *e
	}
	slices.SortStableFunc(entries, func(x, y phoneEntry) int { return x.PairedAt.Compare(y.PairedAt) })
	return entries, unread
}

func linkNames(ls []railLink) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Name
	}
	return out
}

func failedNames(fs []revokeFailure) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Name
	}
	return out
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func revokeText(done, failed []string, again string) string {
	var sb strings.Builder
	if len(done) > 0 {
		sb.WriteString("Revoked in " + joined(done) + ". ")
	} else {
		sb.WriteString("Nothing was revoked. ")
	}
	back := "it is back"
	if len(failed) == 1 {
		sb.WriteString(failed[0] + " could not be reached: its record of this Companion, if it has one, is still active. ")
	} else {
		sb.WriteString(joined(failed) + " could not be reached: their records of this Companion, if they have any, are still active. ")
		back = "they are back"
	}
	sb.WriteString("Press " + again + " again when " + back + ".")
	return sb.String()
}

func (b *Board) connectRevokePhone(everywhere bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if s, _ := ctx.Value(sessionKey{}).(session); s.Device != nil {
			b.fail(w, http.StatusForbidden, "a paired Companion can sign out only itself")
			return
		}
		label := r.PostFormValue("phone")
		if !state.ValidPhoneLabel(label) {
			b.fail(w, http.StatusBadRequest, "no Companion was named")
			return
		}
		targets, note := b.hostFleets(ctx), revokeNote{Action: "everywhere", Phone: label, Button: "Revoke everywhere"}
		if !everywhere {
			id := r.PostFormValue("fleet")
			i := slices.IndexFunc(targets, func(l railLink) bool { return l.ID == id })
			if i < 0 {
				b.fail(w, http.StatusForbidden, "this browser is not signed in to that fleet; reload the Companion page")
				return
			}
			targets, note = targets[i:i+1], revokeNote{Action: "in", Fleet: id, Phone: label, Button: "Revoke"}
		}
		done, failed := b.revokeAcross(ctx, targets, label)
		if len(failed) == 0 {
			http.Redirect(w, r, b.o.Base+"/connect", http.StatusSeeOther)
			return
		}
		note.Text = revokeText(linkNames(done), failedNames(failed), note.Button)
		b.connectPage(w, r, http.StatusOK, map[string]any{"Revoked": note})
	}
}
