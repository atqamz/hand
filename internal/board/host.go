package board

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/atqamz/hand/internal/state"
)

type FleetLink struct{ ID, Name string }

type fleetsKey struct{}

type railLink struct {
	FleetLink
	Current bool
}

const railOthers = 7

func (b *Board) fleets(ctx context.Context) []railLink { return b.fleetsFrom(ctx, fleetsKey{}) }

func (b *Board) laptopFleets(ctx context.Context) []railLink { return b.fleetsFrom(ctx, laptopKey{}) }

func (b *Board) fleetsFrom(ctx context.Context, key any) []railLink {
	f, err := b.st.Fleet(ctx)
	if err != nil {
		return nil
	}
	current := strings.TrimPrefix(b.o.Base, "/")
	var out []railLink
	found := false
	if list, ok := ctx.Value(key).(func() []FleetLink); ok {
		for _, l := range list() {
			out = append(out, railLink{l, l.ID == current})
			found = found || l.ID == current
		}
	}
	if !found {
		out = append([]railLink{{FleetLink{ID: current, Name: f.Name}, true}}, out...)
	}
	kept, others := out[:0], 0
	for _, l := range out {
		if l.Current || others < railOthers {
			kept = append(kept, l)
			if !l.Current {
				others++
			}
		}
	}
	return kept
}

type Prover interface{ PhoneSession(r *http.Request) bool }

type HostOptions struct {
	Loopback bool
	Grace    time.Duration
	Resolve  func(id string) (home string, err error)
	Open     func(id, home string) (http.Handler, io.Closer, error)
	List     func() ([]FleetLink, error)
	Valid    func(id, token string) bool
}

type Host struct {
	o      HostOptions
	mux    *http.ServeMux
	mu     sync.Mutex
	fleets map[string]hosted
}

type hosted struct {
	home   string
	h      http.Handler
	c      io.Closer
	busy   *sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

func NewHost(o HostOptions) *Host {
	if o.Grace <= 0 {
		o.Grace = 30 * time.Second
	}
	h := &Host{o: o, mux: http.NewServeMux(), fleets: map[string]hosted{}}
	h.mux.HandleFunc("GET /{$}", h.list)
	h.mux.HandleFunc("/static/", ServeStatic)
	h.mux.HandleFunc("GET /manifest.webmanifest", manifest)
	h.mux.HandleFunc("GET /sw.js", worker)
	h.mux.HandleFunc("GET /offline", offline)
	h.mux.HandleFunc("/{id}/", h.fleet)
	return h
}

func (h *Host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if viaFunnel(r) && !strings.HasPrefix(r.URL.Path, "/static/") {
		hostFail(w, http.StatusForbidden, funnelRefusal)
		return
	}
	h.mux.ServeHTTP(proxied{w, installable(r)}, r)
}

func (h *Host) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	var errs []error
	for id, f := range h.fleets {
		f.cancel()
		errs = append(errs, f.c.Close())
		delete(h.fleets, id)
	}
	return errors.Join(errs...)
}

func (h *Host) list(w http.ResponseWriter, r *http.Request) {
	loopback := h.o.Loopback && !forwarded(r)
	data := map[string]any{"Title": "fleets", "Loopback": loopback}
	var paired map[string]bool
	if !loopback && h.o.Loopback && h.o.List != nil {
		paired = h.phoneFleets(r, "")
	}
	if loopback || len(paired) > 0 {
		fleets, err := h.o.List()
		if err != nil {
			hostFail(w, http.StatusInternalServerError, Scrub(err.Error()))
			return
		}
		if !loopback {
			fleets = within(fleets, paired)
		}
		data["Fleets"] = fleets
	}
	hostPage(w, http.StatusOK, "fleets.html", data)
}

func (h *Host) fleet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !state.FleetID.MatchString(id) {
		hostFail(w, http.StatusNotFound, "fleet not found")
		return
	}
	f, err := h.handler(id)
	switch {
	case errors.Is(err, state.ErrNotFound):
		hostFail(w, http.StatusNotFound, "fleet not found")
	case err != nil:
		log.Printf("board: fleet %s did not open: %v", id, err)
		if h.o.Loopback && !forwarded(r) {
			hostFail(w, http.StatusInternalServerError, Scrub(err.Error()))
		} else {
			hostFail(w, http.StatusNotFound, "fleet not found")
		}
	default:
		defer f.busy.Done()
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		defer context.AfterFunc(f.ctx, cancel)()
		if h.o.Loopback && h.o.List != nil {
			ctx = context.WithValue(ctx, fleetsKey{}, h.signedIn(id, r))
			ctx = context.WithValue(ctx, laptopKey{}, h.laptopIn(id, r))
			ctx = context.WithValue(ctx, pairKey{}, h.pairIn)
			ctx = context.WithValue(ctx, listKey{}, listFunc(h.listIn(id, r)))
			ctx = context.WithValue(ctx, revokeKey{}, revokeFunc(h.revokeIn(id, r)))
		}
		http.StripPrefix("/"+id, f.h).ServeHTTP(w, r.WithContext(ctx))
	}
}

func (h *Host) phoneFleets(r *http.Request, current string) map[string]bool {
	prefix := phoneName + "_"
	if secure(r) {
		prefix = hostCookie + prefix
	}
	tried, proven := map[string]bool{current: true}, map[string]bool{}
	for _, c := range r.Cookies() {
		id, ok := strings.CutPrefix(c.Name, prefix)
		if !ok || tried[id] || !state.FleetID.MatchString(id) {
			continue
		}
		tried[id] = true
		if h.phoneProves(id, r) {
			proven[id] = true
		}
	}
	return proven
}

func (h *Host) phoneProves(id string, r *http.Request) bool {
	f, err := h.handler(id)
	if err != nil {
		return false
	}
	defer f.busy.Done()
	p, ok := f.h.(Prover)
	return ok && p.PhoneSession(r)
}

func (h *Host) tokenFleets(current string, r *http.Request) map[string]bool {
	proven := map[string]bool{current: true}
	for _, c := range r.Cookies() {
		id, ok := strings.CutPrefix(c.Name, cookieName+"_")
		if ok && state.FleetID.MatchString(id) && h.o.Valid != nil && h.o.Valid(id, c.Value) {
			proven[id] = true
		}
	}
	return proven
}

func (h *Host) tokenProves(current, id string, r *http.Request) bool {
	if id == current || !state.FleetID.MatchString(id) || h.o.Valid == nil {
		return false
	}
	for _, c := range r.CookiesNamed(cookieName + "_" + id) {
		if h.o.Valid(id, c.Value) {
			return true
		}
	}
	return false
}

func (h *Host) listIn(current string, r *http.Request) func(context.Context, string) ([]PhoneRecord, error) {
	return func(ctx context.Context, id string) ([]PhoneRecord, error) {
		if !h.tokenProves(current, id, r) {
			return nil, errNotProven
		}
		f, err := h.handler(id)
		if err != nil {
			log.Printf("board: the Phone page could not reach fleet %s: %v", id, err)
			return nil, err
		}
		defer f.busy.Done()
		l, ok := f.h.(Lister)
		if !ok {
			return nil, state.ErrNotFound
		}
		got, err := l.PhoneRecords(ctx)
		if err != nil {
			log.Printf("board: listing the phones of fleet %s failed: %v", id, err)
		}
		return got, err
	}
}

func (h *Host) revokeIn(current string, r *http.Request) func(context.Context, string, string) (int, error) {
	return func(ctx context.Context, id, label string) (int, error) {
		if !h.tokenProves(current, id, r) {
			return 0, errNotProven
		}
		f, err := h.handler(id)
		if err != nil {
			log.Printf("board: revoking a phone could not reach fleet %s: %v", id, err)
			return 0, err
		}
		defer f.busy.Done()
		rv, ok := f.h.(Revoker)
		if !ok {
			return 0, state.ErrNotFound
		}
		n, err := rv.RevokePhone(ctx, &Revocation{fleet: id, label: label})
		if err != nil {
			log.Printf("board: revoking a phone in fleet %s failed: %v", id, err)
		}
		return n, err
	}
}

func (h *Host) laptopIn(current string, r *http.Request) func() []FleetLink {
	return sync.OnceValue(func() []FleetLink {
		proven := h.tokenFleets(current, r)
		if len(proven) == 1 {
			return nil
		}
		all, err := h.o.List()
		if err != nil {
			return nil
		}
		return within(all, proven)
	})
}

func (h *Host) pairIn(ctx context.Context, rd *Redeemed, id string) (Paired, error) {
	if !state.FleetID.MatchString(id) {
		return Paired{}, state.ErrNotFound
	}
	f, err := h.handler(id)
	if err != nil {
		log.Printf("board: pairing could not reach fleet %s: %v", id, err)
		return Paired{}, err
	}
	defer f.busy.Done()
	p, ok := f.h.(Pairer)
	if !ok {
		return Paired{}, state.ErrNotFound
	}
	got, err := p.PairPhone(ctx, rd)
	if err != nil {
		log.Printf("board: pairing with fleet %s failed: %v", id, err)
	}
	return got, err
}

func (h *Host) signedIn(current string, r *http.Request) func() []FleetLink {
	return sync.OnceValue(func() []FleetLink {
		proven := h.tokenFleets(current, r)
		for id := range h.phoneFleets(r, current) {
			proven[id] = true
		}
		if len(proven) == 1 {
			return nil
		}
		all, err := h.o.List()
		if err != nil {
			return nil
		}
		return within(all, proven)
	})
}

func within(all []FleetLink, ids map[string]bool) []FleetLink {
	var out []FleetLink
	for _, l := range all {
		if ids[l.ID] {
			out = append(out, l)
		}
	}
	return out
}

func (h *Host) handler(id string) (hosted, error) {
	for range 3 {
		home, err := h.o.Resolve(id)
		if f, ok := h.cached(id, home, err); ok {
			return f, nil
		}
		if err != nil {
			return hosted{}, err
		}
		fh, c, err := h.o.Open(id, home)
		if err != nil {
			return hosted{}, err
		}
		if again, err := h.o.Resolve(id); err != nil || again != home {
			_ = c.Close()
			continue
		}
		h.mu.Lock()
		if f, ok := h.fleets[id]; ok && f.home == home {
			f.busy.Add(1)
			h.mu.Unlock()
			_ = c.Close()
			return f, nil
		}
		h.retire(id)
		f := hosted{home: home, h: fh, c: c, busy: &sync.WaitGroup{}}
		f.ctx, f.cancel = context.WithCancel(context.Background())
		f.busy.Add(1)
		h.fleets[id] = f
		h.mu.Unlock()
		return f, nil
	}
	return hosted{}, fmt.Errorf("%w: fleet %s kept moving while it was opened; try again", state.ErrConflict, id)
}

func (h *Host) cached(id, home string, err error) (hosted, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if f, ok := h.fleets[id]; ok && err == nil && f.home == home {
		f.busy.Add(1)
		return f, true
	}
	h.retire(id)
	return hosted{}, false
}

func (h *Host) retire(id string) {
	f, ok := h.fleets[id]
	if !ok {
		return
	}
	delete(h.fleets, id)
	go func() {
		idle := make(chan struct{})
		go func() {
			f.busy.Wait()
			close(idle)
		}()
		select {
		case <-idle:
		case <-time.After(h.o.Grace):
			f.cancel()
			<-idle
		}
		f.cancel()
		_ = f.c.Close()
	}()
}

func hostPage(w http.ResponseWriter, status int, name string, data map[string]any) {
	data["Base"] = ""
	data["Fleet"] = "hand"
	renderPage(w, status, name, data)
}

func hostFail(w http.ResponseWriter, status int, msg string) {
	hostPage(w, status, "error.html", map[string]any{"Title": strconv.Itoa(status), "Status": status, "Message": msg})
}
