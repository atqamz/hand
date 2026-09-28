package board

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/atqamz/hand/internal/state"
)

type FleetLink struct{ ID, Name string }

type HostOptions struct {
	Loopback bool
	Grace    time.Duration
	Resolve  func(id string) (home string, err error)
	Open     func(id, home string) (http.Handler, io.Closer, error)
	List     func() ([]FleetLink, error)
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
	h.mux.HandleFunc("/{id}/", h.fleet)
	return h
}

func (h *Host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("X-Frame-Options", "DENY")
	h.mux.ServeHTTP(w, r)
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

func (h *Host) list(w http.ResponseWriter, _ *http.Request) {
	data := map[string]any{"Title": "fleets", "Loopback": h.o.Loopback}
	if h.o.Loopback {
		fleets, err := h.o.List()
		if err != nil {
			hostFail(w, http.StatusInternalServerError, Scrub(err.Error()))
			return
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
		hostFail(w, http.StatusInternalServerError, Scrub(err.Error()))
	default:
		defer f.busy.Done()
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		defer context.AfterFunc(f.ctx, cancel)()
		http.StripPrefix("/"+id, f.h).ServeHTTP(w, r.WithContext(ctx))
	}
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
