package board

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"

	"github.com/atqamz/hand/internal/state"
)

type FleetLink struct{ ID, Name string }

type HostOptions struct {
	Loopback bool
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
	home string
	h    http.Handler
	c    io.Closer
}

func NewHost(o HostOptions) *Host {
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
	fh, err := h.handler(id)
	switch {
	case errors.Is(err, state.ErrNotFound):
		hostFail(w, http.StatusNotFound, "fleet not found")
	case err != nil:
		hostFail(w, http.StatusInternalServerError, Scrub(err.Error()))
	default:
		http.StripPrefix("/"+id, fh).ServeHTTP(w, r)
	}
}

func (h *Host) handler(id string) (http.Handler, error) {
	home, err := h.o.Resolve(id)
	h.mu.Lock()
	defer h.mu.Unlock()
	f, cached := h.fleets[id]
	if err == nil && cached && f.home == home {
		return f.h, nil
	}
	if cached {
		_ = f.c.Close()
		delete(h.fleets, id)
	}
	if err != nil {
		return nil, err
	}
	fh, c, err := h.o.Open(id, home)
	if err != nil {
		return nil, err
	}
	h.fleets[id] = hosted{home: home, h: fh, c: c}
	return fh, nil
}

func hostPage(w http.ResponseWriter, status int, name string, data map[string]any) {
	data["Base"] = ""
	data["Fleet"] = "hand"
	renderPage(w, status, name, data)
}

func hostFail(w http.ResponseWriter, status int, msg string) {
	hostPage(w, status, "error.html", map[string]any{"Title": strconv.Itoa(status), "Status": status, "Message": msg})
}
