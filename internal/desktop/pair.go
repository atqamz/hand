package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/atqamz/hand/internal/desktop/remote"
	"github.com/atqamz/hand/internal/sidecar"
)

type PairRequest struct {
	Link  remote.Link
	Name  string
	Held  func(id string) bool
	Again bool
}

type RemoteDesk interface {
	Hosts() ([]remote.Host, error)
	Status(host string) sidecar.ClientStatus
	Pair(ctx context.Context, req PairRequest) error
	Forget(host string) error
}

type HostClients interface {
	RemoteClients
	Stop(host string)
}

type Remotes struct {
	Store   *remote.Store
	Clients HostClients
	HTTP    *http.Client
	Now     func() time.Time
	Ready   time.Duration
}

type PairError struct {
	Code, Message string
	Transient     bool
}

func (e *PairError) Error() string { return e.Message }

var pairTexts = map[string]string{
	"wrong_code":      "That code is not right. Check it against the one on the Board, or show a new code there.",
	"code_expired":    "That code has expired. A code works for 5 minutes. Show a new code on the Board.",
	"code_used":       "That code was already used. A code works once. Show a new code on the Board.",
	"codes_cancelled": "Too many wrong codes. Every code was cancelled. Show a new code on the Board.",
	"rate_limited":    "Too many wrong codes from this device. Wait a minute, then try again.",
	"fleet_held":      "This window already holds that fleet. Forget its host first if you mean to pair it again.",
	"other_host":      "This link is for another host than the one being paired again.",
}

func LinkText(err error) string {
	var le *remote.LinkError
	if errors.As(err, &le) && le.Reason == "version_unsupported" {
		return "This code needs a newer Hand. Update Hand."
	}
	return "This is not a Hand pairing code. Show a new one on the Board."
}

func (r *Remotes) Hosts() ([]remote.Host, error) { return r.Store.Hosts() }

func (r *Remotes) Status(host string) sidecar.ClientStatus { return r.Clients.Status(host) }

func (r *Remotes) Forget(host string) error {
	r.Clients.Stop(host)
	return r.Store.Forget(host)
}

func pairFail(code string) *PairError { return &PairError{Code: code, Message: pairTexts[code]} }

func (r *Remotes) Pair(ctx context.Context, req PairRequest) error {
	l := req.Link
	hosts, err := r.Store.Hosts()
	if err != nil {
		return errors.New("The remote hosts file cannot be read; fix or remove it first.")
	}
	at := slices.IndexFunc(hosts, func(h remote.Host) bool { return h.ID == l.Host })
	heldHere := at >= 0 && slices.ContainsFunc(hosts[at].Fleets, func(f remote.Fleet) bool { return f.ID == l.Fleet })
	switch {
	case req.Again && at < 0:
		return pairFail("other_host")
	case (req.Held != nil && req.Held(l.Fleet) || heldHere) && !(req.Again && heldHere):
		return pairFail("fleet_held")
	}
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	host := remote.Host{ID: l.Host, Relay: l.Relay, PairedAt: now().UTC()}
	if err := r.Clients.Start(host, at < 0); err != nil {
		return &PairError{Code: "start", Message: "The connection to the host could not start: " + err.Error()}
	}
	cleanup := func() {
		if at < 0 {
			r.Clients.Stop(host.ID)
			_ = r.Store.Forget(host.ID)
		}
	}
	st, err := r.listening(ctx, host.ID)
	if err != nil {
		cleanup()
		return err
	}
	paired, err := r.redeem(ctx, st, l, req.Name)
	if err != nil {
		cleanup()
		return err
	}
	host.ClientID = st.ID
	stored := false
	for _, f := range paired {
		var err error
		if req.Again && f.ID == l.Fleet && heldHere {
			err = r.Store.Replace(host, remote.Fleet{ID: f.ID, Name: f.Name}, f.Cookie)
		} else {
			err = r.Store.Add(host, remote.Fleet{ID: f.ID, Name: f.Name}, f.Cookie, req.Held)
		}
		switch {
		case err == nil:
			stored = true
		case errors.Is(err, remote.ErrFleetHeld):
		default:
			cleanup()
			return errors.New("The pairing worked, but this window could not keep it: " + err.Error())
		}
	}
	if !stored {
		cleanup()
		return pairFail("fleet_held")
	}
	return nil
}

func (r *Remotes) listening(ctx context.Context, host string) (sidecar.ClientStatus, error) {
	wait := r.Ready
	if wait <= 0 {
		wait = 25 * time.Second
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		switch st := r.Clients.Status(host); {
		case st.Addr != "" && st.ID != "":
			return st, nil
		case st.State == sidecar.Failed || st.State == sidecar.Stopped:
			msg := "The connection to the host could not start."
			if st.Reason != "" {
				msg += " " + st.Reason
			}
			return st, &PairError{Code: "start", Message: msg}
		}
		select {
		case <-ctx.Done():
			return sidecar.ClientStatus{}, ctx.Err()
		case <-deadline.C:
			return sidecar.ClientStatus{}, &PairError{Code: "unreachable", Message: lostMessage("unreachable"), Transient: true}
		case <-tick.C:
		}
	}
}

func lostMessage(link string) string {
	if err := lostLink(sidecar.ClientStatus{Link: link}); err != nil {
		return err.Error()
	}
	return "The host did not answer."
}

type pairedFleet struct{ ID, Name, Cookie string }

func (r *Remotes) redeem(ctx context.Context, st sidecar.ClientStatus, l remote.Link, name string) ([]pairedFleet, error) {
	body, err := json.Marshal(map[string]string{"code": l.Code(), "name": name})
	if err != nil {
		return nil, errors.New("The pairing request could not be made.")
	}
	hc := r.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 40 * time.Second, Transport: &http.Transport{DisableKeepAlives: true, MaxConnsPerHost: 6}}
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+st.Addr+"/"+l.Fleet+"/api/v1/pair", bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("The pairing request could not be made.")
	}
	hr.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(hr)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		link := r.Clients.Status(l.Host).Link
		if lostLink(sidecar.ClientStatus{Link: link}) == nil {
			link = sidecar.Unreachable
		}
		return nil, &PairError{Code: link, Message: lostMessage(link), Transient: true}
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	if res.StatusCode != http.StatusOK {
		var e APIErrorResponse
		_ = json.Unmarshal(raw, &e)
		if _, known := pairTexts[e.Error.Code]; known {
			return nil, pairFail(e.Error.Code)
		}
		if res.StatusCode == http.StatusTooManyRequests {
			return nil, pairFail("rate_limited")
		}
		return nil, &PairError{Code: "refused", Message: "The host refused the code. Show a new code on the Board."}
	}
	var env struct {
		Data struct {
			Fleets []struct {
				ID, Name   string
				CookieName string `json:"cookie_name"`
				Cookie     string `json:"cookie_value"`
			} `json:"fleets"`
		} `json:"data"`
	}
	bad := &PairError{Code: "answer", Message: "The host's answer is not the pairing answer this window reads."}
	if json.Unmarshal(raw, &env) != nil {
		return nil, bad
	}
	var out []pairedFleet
	for _, f := range env.Data.Fleets {
		if f.CookieName != "hand_phone_"+f.ID {
			return nil, bad
		}
		out = append(out, pairedFleet{ID: f.ID, Name: f.Name, Cookie: f.Cookie})
	}
	if !slices.ContainsFunc(out, func(f pairedFleet) bool { return f.ID == l.Fleet }) {
		return nil, bad
	}
	return out, nil
}
