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
	b.phones.mu.Lock()
	defer b.phones.mu.Unlock()
	out := make([]PhoneRecord, len(devices))
	for i, d := range devices {
		out[i] = PhoneRecord{d, b.phones.seen[d.ID], b.lapsed(d)}
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
	b.phones.mu.Lock()
	for _, d := range gone {
		delete(b.phones.seen, d.ID)
	}
	b.phones.mu.Unlock()
	return len(gone), err
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
	for _, l := range b.laptopFleets(ctx) {
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
		sb.WriteString(failed[0] + " could not be reached: its record of this phone, if it has one, is still active. ")
	} else {
		sb.WriteString(joined(failed) + " could not be reached: their records of this phone, if they have any, are still active. ")
		back = "they are back"
	}
	sb.WriteString("Press " + again + " again when " + back + ", and also remove the phone from your tailnet if it is lost.")
	return sb.String()
}

func (b *Board) connectRevokePhone(everywhere bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if s, _ := ctx.Value(sessionKey{}).(session); s.Device != nil {
			b.fail(w, http.StatusForbidden, "a paired phone can sign out only itself")
			return
		}
		label := r.PostFormValue("phone")
		if !state.ValidPhoneLabel(label) {
			b.fail(w, http.StatusBadRequest, "no phone was named")
			return
		}
		targets, note := b.laptopFleets(ctx), revokeNote{Action: "everywhere", Phone: label, Button: "Revoke everywhere"}
		if !everywhere {
			id := r.PostFormValue("fleet")
			i := slices.IndexFunc(targets, func(l railLink) bool { return l.ID == id })
			if i < 0 {
				b.fail(w, http.StatusForbidden, "this browser is not signed in to that fleet; reload the Phone page")
				return
			}
			targets, note = targets[i:i+1], revokeNote{Action: "in", Fleet: id, Phone: label, Button: "Revoke"}
		}
		detached := context.WithoutCancel(ctx)
		var done, failed []string
		for _, l := range targets {
			work, cancel := context.WithTimeout(detached, pairWait)
			n, err := b.revokeFleet(work, l.ID, label)
			cancel()
			switch {
			case err != nil:
				failed = append(failed, l.Name)
			case n > 0:
				done = append(done, l.Name)
			}
		}
		if len(failed) == 0 {
			http.Redirect(w, r, b.o.Base+"/connect", http.StatusSeeOther)
			return
		}
		note.Text = revokeText(done, failed, note.Button)
		b.connectPage(w, r, http.StatusOK, map[string]any{"Revoked": note})
	}
}
