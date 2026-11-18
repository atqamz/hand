package board

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"net/http"
	"slices"
	"time"

	"github.com/atqamz/hand/internal/sidecar"
	"github.com/atqamz/hand/internal/state"
)

type APIPhoneIroh struct {
	State string `json:"state"`
	Tone  string `json:"tone"`
	Text  string `json:"text"`
	ID    string `json:"id,omitempty"`
}

type APIPhoneFleet struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Current  bool   `json:"current,omitempty"`
	Selected bool   `json:"selected,omitempty"`
}

type APIPhoneAt struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Current bool   `json:"current,omitempty"`
	Expired bool   `json:"expired,omitempty"`
}

type APIPhoneDevice struct {
	ID       int64        `json:"id,omitempty"`
	Phone    string       `json:"phone,omitempty"`
	Name     string       `json:"name"`
	Login    string       `json:"login,omitempty"`
	PairedAt string       `json:"paired_at"`
	SeenAt   string       `json:"seen_at,omitempty"`
	Expired  bool         `json:"expired,omitempty"`
	Fleets   []APIPhoneAt `json:"fleets,omitempty"`
}

type APIPhoneCode struct {
	Code      string     `json:"code"`
	Link      string     `json:"link"`
	URL       string     `json:"url"`
	Address   string     `json:"address"`
	PlainHTTP bool       `json:"plain_http,omitempty"`
	Fleets    []APIFleet `json:"fleets"`
	ExpiresAt string     `json:"expires_at"`
}

type APIPhoneApp struct {
	Link      string `json:"link"`
	ExpiresAt string `json:"expires_at"`
}

type APIPhoneFailed struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type APIPhoneRevoke struct {
	Revoked []APIFleet       `json:"revoked"`
	Failed  []APIPhoneFailed `json:"failed"`
}

type APIPhone struct {
	Iroh        *APIPhoneIroh    `json:"iroh,omitempty"`
	Fleets      []APIPhoneFleet  `json:"fleets"`
	Code        *APIPhoneCode    `json:"code,omitempty"`
	App         *APIPhoneApp     `json:"app,omitempty"`
	Devices     []APIPhoneDevice `json:"devices"`
	Unreachable []string         `json:"unreachable,omitempty"`
	Revoke      *APIPhoneRevoke  `json:"revoke,omitempty"`
}

type apiPhoneCodeBody struct {
	Base   string   `json:"base"`
	Fleets []string `json:"fleets"`
}

type apiPhoneRevokeBody struct {
	Phone string `json:"phone"`
	Fleet string `json:"fleet"`
	ID    int64  `json:"id"`
}

func (b *Board) irohStatus() sidecar.Status {
	if b.o.Iroh == nil {
		return sidecar.Status{}
	}
	return b.o.Iroh()
}

func apiClock(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func (b *Board) apiPhoneData(ctx context.Context) (APIPhone, error) {
	own, err := b.PhoneRecords(ctx)
	if err != nil {
		return APIPhone{}, err
	}
	return b.apiPhoneDataFrom(ctx, own), nil
}

func (b *Board) apiPhoneDataFrom(ctx context.Context, own []PhoneRecord) APIPhone {
	entries, unread := b.phoneEntries(ctx, own)
	out := APIPhone{Devices: make([]APIPhoneDevice, 0, len(entries)), Unreachable: unread}
	for _, e := range entries {
		d := APIPhoneDevice{Phone: e.Phone, Name: e.Name, Login: e.Login, PairedAt: apiClock(e.PairedAt)}
		if !e.Seen.IsZero() {
			d.SeenAt = apiClock(e.Seen)
		}
		if e.Phone == "" {
			d.ID = e.ID
		}
		if e.Fleets == nil {
			d.Expired = e.Expired
		}
		for _, at := range e.Fleets {
			d.Fleets = append(d.Fleets, APIPhoneAt(at))
		}
		out.Devices = append(out.Devices, d)
	}
	st := b.irohStatus()
	if v := irohView(st); v != nil {
		out.Iroh = &APIPhoneIroh{State: v.State, Tone: v.Tone, Text: "Phone access over iroh is " + v.Text, ID: v.ID}
	}
	code, live, showing := b.showing(b.now())
	var set []string
	if showing {
		set = live.fleets
		switch {
		case live.app:
			if link, err := appLink(st.ID, st.Relay, b.fleetID(), code); err == nil {
				out.App = &APIPhoneApp{Link: link, ExpiresAt: apiClock(live.until)}
			}
		default:
			out.Code = b.apiPhoneCode(ctx, code, live)
		}
	}
	out.Fleets = []APIPhoneFleet{}
	for _, c := range b.pairChoices(ctx, set) {
		out.Fleets = append(out.Fleets, APIPhoneFleet{ID: c.ID, Name: c.Name, Current: c.Current, Selected: c.Ticked})
	}
	return out
}

func (b *Board) apiPhoneCode(ctx context.Context, code string, live pairCode) *APIPhoneCode {
	url := live.base + b.o.Base + "/pair"
	out := &APIPhoneCode{Code: code, Link: url + "#" + code, URL: url, Address: live.base, PlainHTTP: plainHTTP(live.base), Fleets: []APIFleet{}, ExpiresAt: apiClock(live.until)}
	names := b.fleetNames(ctx, live.fleets)
	for i, id := range live.fleets {
		out.Fleets = append(out.Fleets, APIFleet{ID: id, Name: names[i]})
	}
	return out
}

// shortcut: the cursor hashes a keyed digest of the code, never the code, so a
// cursor is safe to log; the fingerprint changes with every new code.
func (b *Board) apiPhoneFingerprint(v APIPhone) uint64 {
	if v.Code != nil {
		c := *v.Code
		c.Code, c.Link = b.mac("phone-code:"+c.Code), ""
		v.Code = &c
	}
	if v.App != nil {
		a := *v.App
		a.Link = b.mac("phone-app:" + a.Link)
		v.App = &a
	}
	raw, _ := json.Marshal(v)
	sum := fnv.New64a()
	_, _ = sum.Write(raw)
	return sum.Sum64()
}

func (b *Board) apiPhoneSend(w http.ResponseWriter, f state.Fleet, since string, v APIPhone) {
	raw, err := json.Marshal(v)
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	b.apiSend(w, f, since, b.apiEncodeCursor(f.ID, b.apiPhoneFingerprint(v)), raw)
}

func (b *Board) apiPhoneHost(w http.ResponseWriter, r *http.Request) bool {
	if s, _ := r.Context().Value(sessionKey{}).(session); s.Device != nil {
		b.apiFail(w, http.StatusForbidden, "forbidden", "a paired phone cannot manage phones; use the host's session")
		return false
	}
	return true
}

func (b *Board) apiPhone(w http.ResponseWriter, r *http.Request) {
	if !b.apiPhoneHost(w, r) {
		return
	}
	f, since, ok := b.apiSince(w, r)
	if !ok {
		return
	}
	v, err := b.apiPhoneData(r.Context())
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	b.apiPhoneSend(w, f, since, v)
}

// apiPhonePrepare reads everything an action's answer needs before the action
// changes anything, so a refusal cannot leave a code minted or a device revoked.
func (b *Board) apiPhonePrepare(w http.ResponseWriter, r *http.Request) (state.Fleet, []PhoneRecord, bool) {
	f, err := b.st.Fleet(r.Context())
	if err != nil {
		b.apiFailErr(w, err)
		return state.Fleet{}, nil, false
	}
	own, err := b.PhoneRecords(r.Context())
	if err != nil {
		b.apiFailErr(w, err)
		return state.Fleet{}, nil, false
	}
	return f, own, true
}

func (b *Board) apiPhoneAnswer(ctx context.Context, w http.ResponseWriter, f state.Fleet, own []PhoneRecord, reply apiReply, mark func(*APIPhone)) {
	v := b.apiPhoneDataFrom(ctx, own)
	if mark != nil {
		mark(&v)
	}
	reply.send(w)
	b.apiPhoneSend(w, f, "", v)
}

func (b *Board) apiPhoneCodeNew(w http.ResponseWriter, r *http.Request) {
	if !b.apiCSRF(w, r) || !b.apiPhoneHost(w, r) {
		return
	}
	var in apiPhoneCodeBody
	if !b.apiObject(w, r, &in, "send one JSON object with base and, optionally, fleets") {
		return
	}
	base, err := cleanBase(in.Base)
	if err != nil {
		b.apiFail(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	set, ok := b.chosenFleetsFrom(r.Context(), in.Fleets)
	if !ok {
		b.apiFail(w, http.StatusForbidden, "forbidden", "this session is not signed in to every fleet you chose; read the phone again and choose from its fleets")
		return
	}
	f, own, ok := b.apiPhonePrepare(w, r)
	if !ok {
		return
	}
	b.mint(b.now(), set, base, false)
	b.apiPhoneAnswer(r.Context(), w, f, own, apiReply{}, nil)
}

func (b *Board) apiPhoneApp(w http.ResponseWriter, r *http.Request) {
	if !b.apiCSRF(w, r) || !b.apiPhoneHost(w, r) {
		return
	}
	st := b.irohStatus()
	if st.State != sidecar.Ready || st.ID == "" || st.Relay == "" || st.Relay == "none" {
		b.apiFail(w, http.StatusConflict, "conflict", "Phone access over iroh is not ready with a relay, so a phone cannot reach this host yet. Start the Board with `--iroh`, or check the sidecar's state in the phone data.")
		return
	}
	if _, err := appLink(st.ID, st.Relay, b.fleetID(), "K7QM3XRD2P"); err != nil {
		b.apiFail(w, http.StatusInternalServerError, "internal", "This host cannot show a pairing link: its iroh relay address does not fit one.")
		return
	}
	f, own, ok := b.apiPhonePrepare(w, r)
	if !ok {
		return
	}
	b.mint(b.now(), []string{b.fleetID()}, "", true)
	b.apiPhoneAnswer(r.Context(), w, f, own, apiReply{}, nil)
}

func (b *Board) apiPhoneRevoke(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !b.apiCSRF(w, r) || !b.apiPhoneHost(w, r) {
		return
	}
	const bad = "send one JSON object with phone, and optionally fleet, or with id"
	var in apiPhoneRevokeBody
	if !b.apiObject(w, r, &in, bad) {
		return
	}
	byID := in.Phone == "" && in.Fleet == "" && in.ID > 0
	byLabel := in.ID == 0 && state.ValidPhoneLabel(in.Phone)
	if byID == byLabel {
		b.apiFail(w, http.StatusBadRequest, "bad_request", bad)
		return
	}
	report := &APIPhoneRevoke{Revoked: []APIFleet{}, Failed: []APIPhoneFailed{}}
	var f state.Fleet
	var own []PhoneRecord
	if byID {
		var ok bool
		f, own, ok = b.apiPhonePrepare(w, r)
		if !ok {
			return
		}
		d, err := b.revokeDevice(ctx, in.ID)
		if err != nil {
			b.apiFailErr(w, err)
			return
		}
		if d != nil {
			b.closePeers(*d)
			report.Revoked = append(report.Revoked, APIFleet{ID: b.fleetID(), Name: b.fleetNames(ctx, []string{b.fleetID()})[0]})
			own = slices.DeleteFunc(own, func(r PhoneRecord) bool { return r.ID == in.ID })
		}
	} else {
		targets := b.hostFleets(ctx)
		if in.Fleet != "" {
			i := slices.IndexFunc(targets, func(l railLink) bool { return l.ID == in.Fleet })
			if i < 0 {
				b.apiFail(w, http.StatusForbidden, "forbidden", "this session is not signed in to that fleet; read the phone again")
				return
			}
			targets = targets[i : i+1]
		}
		var ok bool
		f, own, ok = b.apiPhonePrepare(w, r)
		if !ok {
			return
		}
		done, failed := b.revokeAcross(ctx, targets, in.Phone)
		for _, l := range done {
			report.Revoked = append(report.Revoked, APIFleet{ID: l.ID, Name: l.Name})
		}
		for _, f := range failed {
			report.Failed = append(report.Failed, APIPhoneFailed{ID: f.ID, Name: f.Name, Reason: "the fleet did not answer"})
		}
		if slices.ContainsFunc(done, func(l railLink) bool { return l.ID == b.fleetID() }) {
			own = slices.DeleteFunc(own, func(r PhoneRecord) bool { return r.Phone == in.Phone })
		}
	}
	var done, failed []string
	for _, l := range report.Revoked {
		done = append(done, l.Name)
	}
	for _, f := range report.Failed {
		failed = append(failed, f.Name)
	}
	reply := apiReply{}
	again := "Revoke everywhere"
	if in.Fleet != "" {
		again = "Revoke"
	}
	switch {
	case len(failed) > 0:
		reply.warn = revokeText(done, failed, again)
	case len(done) > 0:
		reply.receipt = "Revoked in " + joined(done)
	}
	b.apiPhoneAnswer(ctx, w, f, own, reply, func(v *APIPhone) { v.Revoke = report })
}
