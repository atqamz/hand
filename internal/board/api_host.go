package board

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

var apiFeatures = []string{"events", "fleets", "pair"}

type APIHostEnvelope struct {
	Version     int    `json:"version"`
	GeneratedAt string `json:"generated_at"`
	Data        any    `json:"data"`
}

type APIFleetNeeds struct {
	Waiting    int    `json:"waiting"`
	Worst      string `json:"worst,omitempty"`
	WorstWord  string `json:"worst_word,omitempty"`
	WorstCount int    `json:"worst_count"`
	Summary    string `json:"summary,omitempty"`
}

type APIFleetSummary struct {
	Pill  string        `json:"pill"`
	Line  string        `json:"line"`
	Needs APIFleetNeeds `json:"needs"`
}

type APIFleetItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Unavailable bool   `json:"unavailable,omitempty"`
	*APIFleetSummary
}

type APIFleets struct {
	Fleets []APIFleetItem `json:"fleets"`
}

type APISidecar struct {
	State  string `json:"state"`
	ID     string `json:"id,omitempty"`
	Relay  string `json:"relay,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type APIMeta struct {
	Version  string      `json:"version"`
	Features []string    `json:"features"`
	Sidecar  *APISidecar `json:"sidecar,omitempty"`
}

type Summarizer interface {
	FleetSummary(ctx context.Context) (APIFleetSummary, error)
}

type Reader interface{ APIRead(r *http.Request) bool }

func (b *Board) FleetSummary(ctx context.Context) (APIFleetSummary, error) {
	s, err := b.apiStatusData(ctx)
	if err != nil {
		return APIFleetSummary{}, err
	}
	n := s.Needs
	return APIFleetSummary{Pill: s.Pill, Line: s.Line, Needs: APIFleetNeeds{
		Waiting: n.Waiting, Worst: n.Worst, WorstWord: n.WorstWord, WorstCount: n.WorstCount, Summary: n.Summary,
	}}, nil
}

func (b *Board) APIRead(r *http.Request) bool {
	s, fault := b.session(r)
	return fault == signedIn && (peerOf(r) == nil || s.Device != nil && strings.HasPrefix(s.Device.Login, "iroh:"))
}

func (h *Host) apiReadable(w http.ResponseWriter, r *http.Request) (map[string]bool, bool) {
	if !h.o.Loopback || proxied(r) {
		hostAPIFail(w, http.StatusForbidden, "forbidden", "the API answers only on a loopback board or to a paired phone")
		return nil, false
	}
	proven := h.tokenFleets("", r)
	delete(proven, "")
	for id := range h.phoneFleets(r, "") {
		proven[id] = true
	}
	held := len(proven) > 0
	if peerOf(r) != nil {
		for id := range proven {
			if !h.reads(id, r) {
				delete(proven, id)
			}
		}
	}
	if held && len(proven) == 0 {
		hostAPIFail(w, http.StatusForbidden, "forbidden", "the API answers on the peer listener only to a phone paired with its own key")
		return nil, false
	}
	if len(proven) == 0 {
		hostAPIFail(w, http.StatusUnauthorized, "unauthorized", "this request needs the board's session cookie")
		return nil, false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		hostAPIFail(w, http.StatusMethodNotAllowed, "method_not_allowed", "this path takes GET and HEAD")
		return nil, false
	}
	return proven, true
}

func (h *Host) reads(id string, r *http.Request) bool {
	f, err := h.handler(id)
	if err != nil {
		return false
	}
	defer f.busy.Done()
	rd, ok := f.h.(Reader)
	return ok && rd.APIRead(r)
}

func (h *Host) summary(ctx context.Context, l FleetLink) APIFleetItem {
	item := APIFleetItem{ID: l.ID, Name: l.Name, Unavailable: true}
	f, err := h.handler(l.ID)
	if err != nil {
		log.Printf("board: the fleet list could not reach fleet %s: %v", l.ID, err)
		return item
	}
	defer f.busy.Done()
	s, ok := f.h.(Summarizer)
	if !ok {
		return item
	}
	got, err := s.FleetSummary(ctx)
	if err != nil {
		log.Printf("board: the fleet list could not read fleet %s: %v", l.ID, err)
		return item
	}
	return APIFleetItem{ID: l.ID, Name: l.Name, APIFleetSummary: &got}
}

func (h *Host) apiFleets(w http.ResponseWriter, r *http.Request) {
	proven, ok := h.apiReadable(w, r)
	if !ok {
		return
	}
	var all []FleetLink
	if h.o.List != nil {
		var err error
		if all, err = h.o.List(); err != nil {
			hostAPIFail(w, http.StatusInternalServerError, "internal", Scrub(err.Error()))
			return
		}
	}
	out := APIFleets{Fleets: []APIFleetItem{}}
	for _, l := range within(all, proven) {
		out.Fleets = append(out.Fleets, h.summary(r.Context(), l))
	}
	h.apiSend(w, out)
}

func (h *Host) apiMeta(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.apiReadable(w, r); !ok {
		return
	}
	out := APIMeta{Version: h.o.Version, Features: apiFeatures}
	if h.o.Iroh != nil && peerOf(r) == nil {
		st := h.o.Iroh()
		out.Sidecar = &APISidecar{State: st.State, ID: st.ID, Relay: st.Relay, Reason: Scrub(st.Reason)}
	}
	h.apiSend(w, out)
}

func (h *Host) apiSend(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(APIHostEnvelope{Version: apiVersion, GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano), Data: data})
}
