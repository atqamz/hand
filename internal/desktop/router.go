package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/atqamz/hand/internal/desktop/remote"
	"github.com/atqamz/hand/internal/sidecar"
)

const RemoteAttentionRefresh = 15 * time.Second

type RemoteStore interface {
	Hosts() ([]remote.Host, error)
	Cookie(host, fleet string) (string, error)
}

type RemoteClients interface {
	Start(h remote.Host, newKey bool) error
	Status(host string) sidecar.ClientStatus
}

type member struct {
	model  ReadModel
	remote bool
	held   int
	at     time.Time
	sigs   []FleetSignal
}

type Router struct {
	local   ReadModel
	members []*member
	byFleet map[string]*member
	fleets  []Fleet
	now     func() time.Time
	mu      sync.Mutex
	rmu     sync.Mutex
	refused map[string]string
	hostErr error
}

func NewRouter(local ReadModel, hosts ...ReadModel) *Router {
	r := &Router{local: local, byFleet: map[string]*member{}, now: time.Now, refused: map[string]string{}}
	r.members = append(r.members, &member{model: local})
	for _, h := range hosts {
		r.members = append(r.members, &member{model: h, remote: true})
	}
	for _, m := range r.members {
		for _, f := range m.model.Fleets() {
			if _, held := r.byFleet[f.ID]; held {
				continue
			}
			r.byFleet[f.ID] = m
			m.held++
			r.fleets = append(r.fleets, f)
		}
	}
	return r
}

func Connect(getenv func(string) string, store RemoteStore, clients RemoteClients, hc *http.Client) func() (ReadModel, error) {
	return func() (ReadModel, error) {
		local, err := openLocal(getenv)
		if err != nil {
			return nil, err
		}
		var hosts []remote.Host
		var herr error
		if store != nil && clients != nil {
			hosts, herr = store.Hosts()
		}
		if len(hosts) == 0 {
			if local.Down != nil {
				if herr != nil {
					return nil, fmt.Errorf("%w; the remote hosts are hidden: %v", local.Down, herr)
				}
				return nil, local.Down
			}
			if herr != nil {
				r := NewRouter(local)
				r.hostErr = herr
				return r, nil
			}
			return local, nil
		}
		models := make([]ReadModel, len(hosts))
		for i, h := range hosts {
			_ = clients.Start(h, false)
			models[i] = remoteClient(h, store, clients, hc)
		}
		return NewRouter(local, models...), nil
	}
}

func remoteClient(h remote.Host, store RemoteStore, clients RemoteClients, hc *http.Client) *HTTPClient {
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true, MaxConnsPerHost: 6}}
	}
	fleets := make([]Fleet, len(h.Fleets))
	for i, f := range h.Fleets {
		fleets[i] = Fleet{ID: f.ID, Name: f.Name, Host: h.ID}
	}
	return &HTTPClient{
		Paired: true,
		Known:  fleets,
		HTTP:   hc,
		BaseOf: func() string {
			if st := clients.Status(h.ID); st.Addr != "" {
				return "http://" + st.Addr
			}
			return ""
		},
		Token: func(fleet string) (string, error) {
			v, err := store.Cookie(h.ID, fleet)
			if err == nil && v == "" {
				err = fmt.Errorf("no cookie for fleet %s; pair it again", fleet)
			}
			return v, err
		},
		Lost: func() error { return lostLink(clients.Status(h.ID)) },
		Check: func() error {
			st := clients.Status(h.ID)
			switch {
			case h.ClientID != "" && st.ID != "" && st.ID != h.ClientID:
				return &APIError{Code: CodeKeyChanged, Remote: true, Message: "this device's key is not the one the host paired; pair it again"}
			case st.Addr == "":
				return fmt.Errorf("the host %s is not connected (%s)", remote.Short(h.ID), st.State)
			}
			return nil
		},
	}
}

func lostLink(st sidecar.ClientStatus) error {
	switch st.Link {
	case sidecar.BoardDown:
		return &APIError{Code: "board_down", Remote: true, Message: "Board not running on the host. Start it there."}
	case sidecar.Busy:
		return &APIError{Code: "host_busy", Remote: true, Message: "Host busy. Trying again."}
	case sidecar.Unreachable:
		return &APIError{Code: "unreachable", Remote: true, Message: "Can't reach host. Check that it is on, that Hand's Board is running on it, and that this computer has internet."}
	}
	return nil
}

func refusalCode(err error) string {
	var api *APIError
	if errors.As(err, &api) && api.Remote {
		switch api.Code {
		case CodeRevoked, CodeNotRecognised, CodeKeyChanged:
			return api.Code
		}
	}
	return ""
}

func (r *Router) note(fleets []string, err error) {
	code := refusalCode(err)
	r.rmu.Lock()
	defer r.rmu.Unlock()
	for _, f := range fleets {
		switch {
		case code != "":
			r.refused[f] = code
		case err == nil:
			delete(r.refused, f)
		}
	}
}

func (r *Router) Refusal(fleet string) string {
	r.rmu.Lock()
	defer r.rmu.Unlock()
	return r.refused[fleet]
}

func (r *Router) HostsErr() error { return r.hostErr }

func (r *Router) Fleets() []Fleet { return append([]Fleet(nil), r.fleets...) }

func (r *Router) of(fleet string) ReadModel {
	if m, ok := r.byFleet[fleet]; ok {
		return m.model
	}
	return r.local
}

func (r *Router) Attention(ctx context.Context) ([]FleetSignal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.members) == 1 {
		return r.local.Attention(ctx)
	}
	now := r.now()
	var wg sync.WaitGroup
	for _, m := range r.members {
		if m.held == 0 || m.remote && !m.at.IsZero() && now.Sub(m.at) < RemoteAttentionRefresh {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sigs, err := m.model.Attention(ctx)
			if m.remote {
				ok := map[string]bool{}
				for _, s := range sigs {
					ok[s.Fleet] = s.OK
				}
				var ids []string
				for _, f := range m.model.Fleets() {
					if r.byFleet[f.ID] == m && (err != nil || ok[f.ID]) {
						ids = append(ids, f.ID)
					}
				}
				r.note(ids, err)
			}
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				sigs = nil
				for _, f := range m.model.Fleets() {
					sigs = append(sigs, FleetSignal{Fleet: f.ID})
				}
			}
			m.sigs, m.at = sigs, now
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []FleetSignal
	for _, m := range r.members {
		for _, s := range m.sigs {
			if r.byFleet[s.Fleet] == m {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

func route[T any](r *Router, fleet string, f func(ReadModel) (T, error)) (T, error) {
	v, err := f(r.of(fleet))
	if m := r.byFleet[fleet]; m != nil && m.remote {
		r.note([]string{fleet}, err)
	}
	return v, err
}

func (r *Router) Name(ctx context.Context, fleet string) (string, error) {
	return route(r, fleet, func(m ReadModel) (string, error) { return m.Name(ctx, fleet) })
}

func (r *Router) Status(ctx context.Context, fleet string) (Status, error) {
	return route(r, fleet, func(m ReadModel) (Status, error) { return m.Status(ctx, fleet) })
}

func (r *Router) Timeline(ctx context.Context, fleet string, before int) (Timeline, error) {
	return route(r, fleet, func(m ReadModel) (Timeline, error) { return m.Timeline(ctx, fleet, before) })
}

func (r *Router) Tasks(ctx context.Context, fleet string, all bool) (Tasks, error) {
	return route(r, fleet, func(m ReadModel) (Tasks, error) { return m.Tasks(ctx, fleet, all) })
}

func (r *Router) TaskSearch(ctx context.Context, fleet string, q TaskSearchQuery) (TaskSearch, error) {
	return route(r, fleet, func(m ReadModel) (TaskSearch, error) { return m.TaskSearch(ctx, fleet, q) })
}

func (r *Router) Projects(ctx context.Context, fleet string) (Projects, error) {
	return route(r, fleet, func(m ReadModel) (Projects, error) { return m.Projects(ctx, fleet) })
}

func (r *Router) Task(ctx context.Context, fleet, ref string) (TaskDetail, error) {
	return route(r, fleet, func(m ReadModel) (TaskDetail, error) { return m.Task(ctx, fleet, ref) })
}

func (r *Router) Attempt(ctx context.Context, fleet, ref string) (AttemptDetail, error) {
	return route(r, fleet, func(m ReadModel) (AttemptDetail, error) { return m.Attempt(ctx, fleet, ref) })
}

func (r *Router) Decision(ctx context.Context, fleet, ref string) (DecisionDetail, error) {
	return route(r, fleet, func(m ReadModel) (DecisionDetail, error) { return m.Decision(ctx, fleet, ref) })
}

func (r *Router) Report(ctx context.Context, fleet, ref string) (ReportDetail, error) {
	return route(r, fleet, func(m ReadModel) (ReportDetail, error) { return m.Report(ctx, fleet, ref) })
}

func (r *Router) Doc(ctx context.Context, fleet, path string, page int) (Doc, error) {
	return route(r, fleet, func(m ReadModel) (Doc, error) { return m.Doc(ctx, fleet, path, page) })
}

func (r *Router) DocWindow(ctx context.Context, fleet, path string, from int) (DocWindow, error) {
	return route(r, fleet, func(m ReadModel) (DocWindow, error) { return m.DocWindow(ctx, fleet, path, from) })
}

func (r *Router) DocBytes(ctx context.Context, fleet, path string) ([]byte, error) {
	return route(r, fleet, func(m ReadModel) ([]byte, error) { return m.DocBytes(ctx, fleet, path) })
}

func (r *Router) AnswerDecision(ctx context.Context, fleet, ref, choice, note string) (Answered, error) {
	return route(r, fleet, func(m ReadModel) (Answered, error) { return m.AnswerDecision(ctx, fleet, ref, choice, note) })
}

func (r *Router) AckReport(ctx context.Context, fleet, ref string) (Acked, error) {
	return route(r, fleet, func(m ReadModel) (Acked, error) { return m.AckReport(ctx, fleet, ref) })
}

func (r *Router) SendMessage(ctx context.Context, fleet, text string, attachments []string) (Sent, error) {
	return route(r, fleet, func(m ReadModel) (Sent, error) { return m.SendMessage(ctx, fleet, text, attachments) })
}

func (r *Router) UploadInbox(ctx context.Context, fleet, name string, body []byte) (InboxFile, error) {
	return route(r, fleet, func(m ReadModel) (InboxFile, error) { return m.UploadInbox(ctx, fleet, name, body) })
}

func (r *Router) InboxHead(ctx context.Context, fleet, name string) (int64, error) {
	return route(r, fleet, func(m ReadModel) (int64, error) { return m.InboxHead(ctx, fleet, name) })
}

func (r *Router) InboxBytes(ctx context.Context, fleet, name string) ([]byte, error) {
	return route(r, fleet, func(m ReadModel) ([]byte, error) { return m.InboxBytes(ctx, fleet, name) })
}

func (r *Router) Supervise(ctx context.Context, fleet, action, ref string) (Control, error) {
	return route(r, fleet, func(m ReadModel) (Control, error) { return m.Supervise(ctx, fleet, action, ref) })
}

func (r *Router) StartWith(ctx context.Context, fleet, harness, model, effort string) (Control, error) {
	return route(r, fleet, func(m ReadModel) (Control, error) { return m.StartWith(ctx, fleet, harness, model, effort) })
}

func (r *Router) Switch(ctx context.Context, fleet string, sw Switch) (Control, error) {
	return route(r, fleet, func(m ReadModel) (Control, error) { return m.Switch(ctx, fleet, sw) })
}

func (r *Router) Models(ctx context.Context, fleet string) (Models, error) {
	return route(r, fleet, func(m ReadModel) (Models, error) { return m.Models(ctx, fleet) })
}

func (r *Router) RefreshModels(ctx context.Context, fleet, harness string) (Refreshed, error) {
	return route(r, fleet, func(m ReadModel) (Refreshed, error) { return m.RefreshModels(ctx, fleet, harness) })
}

func (r *Router) PressKeys(ctx context.Context, fleet string, keys []string, revision int64, digest string, after int64, ref string) (Control, error) {
	return route(r, fleet, func(m ReadModel) (Control, error) {
		return m.PressKeys(ctx, fleet, keys, revision, digest, after, ref)
	})
}

func (r *Router) PressAttemptKeys(ctx context.Context, fleet, ref, key string, revision, after int64, screen string) (Pressed, error) {
	return route(r, fleet, func(m ReadModel) (Pressed, error) {
		return m.PressAttemptKeys(ctx, fleet, ref, key, revision, after, screen)
	})
}

func (r *Router) Poll(ctx context.Context, fleet string, screen Screen, ref string, since PollCursors) (Poll, error) {
	return route(r, fleet, func(m ReadModel) (Poll, error) { return m.Poll(ctx, fleet, screen, ref, since) })
}

func (r *Router) Features(ctx context.Context, fleet string) ([]string, error) {
	return route(r, fleet, func(m ReadModel) ([]string, error) { return m.Features(ctx, fleet) })
}

func (r *Router) Links(ctx context.Context, fleet string, paths []string) ([]*Link, error) {
	return route(r, fleet, func(m ReadModel) ([]*Link, error) { return m.Links(ctx, fleet, paths) })
}

var _ ReadModel = (*Router)(nil)
