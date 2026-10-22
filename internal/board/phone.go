package board

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/atqamz/hand/internal/state"
)

const (
	pairLife   = 5 * time.Minute
	pairKeep   = time.Hour
	pairFails  = 10
	addrFails  = 5
	addrWindow = time.Minute
	phoneLife  = 90 * 24 * time.Hour
	codeChars  = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	hostCookie = "__Host-"
	phoneName  = "hand_phone"
	pairWait   = 20 * time.Second
)

type pairCode struct {
	until  time.Time
	used   bool
	fleets []string
}

type Redeemed struct {
	mu                         sync.Mutex
	fleets                     []string
	done                       map[string]bool
	secret, name, login, label string
}

type Paired struct {
	ID, Name string
	Device   int64
}

type Pairer interface {
	PairPhone(ctx context.Context, r *Redeemed) (Paired, error)
}

type pairKey struct{}

type laptopKey struct{}

var errNotInCode = errors.New("this pairing code does not name this fleet")

func (r *Redeemed) take(id string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(r.fleets, id) || r.done[id] {
		return false
	}
	if r.done == nil {
		r.done = map[string]bool{}
	}
	r.done[id] = true
	return true
}

type burst struct {
	n     int
	since time.Time
}

type phones struct {
	mu    sync.Mutex
	codes map[string]pairCode
	fails int
	tries map[string]burst
	seen  map[int64]time.Time
}

type pairResult int

const (
	pairOK pairResult = iota
	pairWrong
	pairExpired
	pairUsed
	pairBurned
	pairBlocked
)

type session struct {
	Device *state.Device
	CSRF   string
	hash   string
}

type sessionKey struct{}

type sessionFault int

const (
	signedIn sessionFault = iota
	noSession
	phoneGone
	phoneOther
)

func (b *Board) mac(what string) string {
	m := hmac.New(sha256.New, []byte(b.token))
	m.Write([]byte(what))
	return hex.EncodeToString(m.Sum(nil))
}

func (b *Board) secretHash(secret string) string { return b.mac("device:" + secret) }

func (b *Board) deviceCSRF(secret string) string { return b.mac("csrf:" + secret)[:32] }

func forwarded(r *http.Request) bool {
	return r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("Tailscale-User-Login") != ""
}

func viaFunnel(r *http.Request) bool { return r.Header.Get("Tailscale-Funnel-Request") != "" }

const funnelRefusal = "This board does not answer the public internet. Turn Tailscale Funnel off for it, for example with `tailscale funnel reset`."

func secure(r *http.Request) bool { return r.Header.Get("X-Forwarded-Proto") == "https" }

func (b *Board) fleetID() string { return strings.TrimPrefix(b.o.Base, "/") }

func phoneCookieName(id string, https bool) string {
	name := phoneName
	if id != "" {
		name += "_" + id
	}
	if https {
		return hostCookie + name
	}
	return name
}

func (b *Board) phoneCookie(r *http.Request) string { return phoneCookieName(b.fleetID(), secure(r)) }

func setPhoneCookie(w http.ResponseWriter, r *http.Request, id string, device int64, secret string) {
	http.SetCookie(w, &http.Cookie{Name: phoneCookieName(id, secure(r)), Value: strconv.FormatInt(device, 10) + "." + secret, Path: "/", MaxAge: int(phoneLife / time.Second), HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteStrictMode})
}

func tailnetLogin(r *http.Request) string {
	v := r.Header.Get("Tailscale-User-Login")
	if d, err := new(mime.WordDecoder).DecodeHeader(v); err == nil {
		v = d
	}
	return state.CleanLogin(v)
}

func remote(r *http.Request) string {
	chain := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if last := strings.TrimSpace(chain[strings.LastIndex(chain, ",")+1:]); last != "" {
		return last
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (b *Board) session(r *http.Request) (session, sessionFault) {
	s, fault := b.identify(r)
	if fault == signedIn && s.Device != nil {
		b.phones.mu.Lock()
		b.phones.seen[s.Device.ID] = b.now()
		b.phones.mu.Unlock()
	}
	return s, fault
}

func (b *Board) PhoneSession(r *http.Request) bool {
	_, fault := b.phoneIdentify(r)
	return fault == signedIn
}

func (b *Board) identify(r *http.Request) (session, sessionFault) {
	if c, err := r.Cookie(cookieFor(b.o.Base)); err == nil && b.valid(c.Value) {
		return session{CSRF: b.token}, signedIn
	}
	return b.phoneIdentify(r)
}

func (b *Board) phoneIdentify(r *http.Request) (session, sessionFault) {
	c, err := r.Cookie(b.phoneCookie(r))
	if err != nil {
		return session{}, noSession
	}
	idText, secret, ok := strings.Cut(c.Value, ".")
	id, err := strconv.ParseInt(idText, 10, 64)
	if !ok || err != nil || id < 1 || len(secret) != 64 {
		return session{}, phoneGone
	}
	hash := b.secretHash(secret)
	d, ok, err := b.st.DeviceWithSecret(r.Context(), id, hash)
	if err != nil || !ok {
		return session{}, phoneGone
	}
	if b.lapsed(d) {
		return session{}, phoneGone
	}
	if d.Login != "" && subtle.ConstantTimeCompare([]byte(d.Login), []byte(tailnetLogin(r))) != 1 {
		return session{}, phoneOther
	}
	return session{Device: &d, CSRF: b.deviceCSRF(secret), hash: hash}, signedIn
}

func sessionFaults(f sessionFault, viaProxy bool) string {
	switch {
	case f == phoneGone:
		return "This phone is no longer paired with this fleet. Pair it again from the Board on your laptop: Phone, in its menu."
	case f == phoneOther:
		return "This phone was paired for another tailnet login, or reached the Board another way. Open the address it was paired with, or pair it again from your laptop."
	case viaProxy:
		return "Pair this phone first: on your laptop, open this fleet's Board, choose Phone in its menu, and show a pairing code."
	}
	return "Open this fleet with `hand open` from inside it. On another device or browser, paste the link `hand open --print` prints."
}

func (b *Board) lapsed(d state.Device) bool { return b.now().After(d.PairedAt.Add(phoneLife)) }

func (b *Board) csrf(ctx context.Context) string {
	s, _ := ctx.Value(sessionKey{}).(session)
	return s.CSRF
}

func (b *Board) stillSignedIn(ctx context.Context) bool {
	s, _ := ctx.Value(sessionKey{}).(session)
	if s.Device == nil {
		return true
	}
	d, ok, err := b.st.DeviceWithSecret(ctx, s.Device.ID, s.hash)
	return err == nil && ok && !b.lapsed(d)
}

func randomText(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func newCode() string {
	buf := make([]byte, 10)
	_, _ = rand.Read(buf)
	for i, c := range buf {
		buf[i] = codeChars[c&31]
	}
	return string(buf[:5]) + "-" + string(buf[5:])
}

func normCode(s string) string {
	if len(s) > 32 {
		return ""
	}
	s = strings.ToUpper(strings.NewReplacer("-", "", " ", "", "O", "0", "o", "0", "I", "1", "i", "1", "L", "1", "l", "1").Replace(s))
	if len(s) != 10 {
		return ""
	}
	return s[:5] + "-" + s[5:]
}

func (b *Board) mint(now time.Time, fleets []string) (string, time.Time) {
	code, until := newCode(), now.Add(pairLife)
	b.phones.mu.Lock()
	defer b.phones.mu.Unlock()
	b.phones.prune(now)
	for c, v := range b.phones.codes {
		if !v.used && !now.After(v.until) {
			delete(b.phones.codes, c)
		}
	}
	b.phones.codes[code] = pairCode{until: until, fleets: slices.Clone(fleets)}
	return code, until
}

func (p *phones) prune(now time.Time) {
	for c, v := range p.codes {
		if now.After(v.until.Add(pairKeep)) {
			delete(p.codes, c)
		}
	}
	for a, v := range p.tries {
		if now.Sub(v.since) >= addrWindow {
			delete(p.tries, a)
		}
	}
}

func (p *phones) burn() {
	clear(p.codes)
	clear(p.tries)
	p.fails = 0
}

func (b *Board) redeem(addr, typed string, now time.Time) (pairResult, []string) {
	p := &b.phones
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune(now)
	if len(p.codes) == 0 {
		return pairWrong, nil
	}
	if t := p.tries[addr]; t.n >= addrFails {
		return pairBlocked, nil
	}
	want := normCode(typed)
	var hit string
	for c := range p.codes {
		if subtle.ConstantTimeCompare([]byte(c), []byte(want)) == 1 {
			hit = c
		}
	}
	entry, found := p.codes[hit]
	if found && !entry.used && !now.After(entry.until) {
		entry.used = true
		p.codes[hit] = entry
		p.fails = 0
		delete(p.tries, addr)
		return pairOK, slices.Clone(entry.fleets)
	}
	t := p.tries[addr]
	if t.n == 0 {
		t.since = now
	}
	t.n++
	p.tries[addr] = t
	if p.fails++; p.fails >= pairFails {
		p.burn()
		return pairBurned, nil
	}
	switch {
	case !found:
		return pairWrong, nil
	case entry.used:
		return pairUsed, nil
	}
	return pairExpired, nil
}

func cleanBase(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return "", errors.New("type the address your phone opens, like https://laptop.tailnet.example")
	}
	return u.Scheme + "://" + u.Host, nil
}

func plainHTTP(base string) bool {
	u, _ := url.Parse(base)
	if u == nil || u.Scheme != "http" {
		return false
	}
	if u.Hostname() == "localhost" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip == nil || !ip.IsLoopback()
}

func (b *Board) connect(w http.ResponseWriter, r *http.Request) {
	b.connectPage(w, r, http.StatusOK, nil)
}

type deviceRow struct {
	state.Device
	Seen    time.Time
	Expired bool
}

type fleetChoice struct {
	ID, Name        string
	Current, Ticked bool
}

func (b *Board) pairChoices(ctx context.Context, set []string) []fleetChoice {
	list := b.laptopFleets(ctx)
	if len(list) < 2 {
		return nil
	}
	out := make([]fleetChoice, len(list))
	for i, l := range list {
		out[i] = fleetChoice{ID: l.ID, Name: l.Name, Current: l.Current, Ticked: l.Current || slices.Contains(set, l.ID)}
	}
	slices.SortStableFunc(out, func(x, y fleetChoice) int {
		switch {
		case x.Current == y.Current:
			return 0
		case x.Current:
			return -1
		}
		return 1
	})
	return out
}

func (b *Board) connectPage(w http.ResponseWriter, r *http.Request, status int, extra map[string]any) {
	ctx := r.Context()
	s, _ := ctx.Value(sessionKey{}).(session)
	data := map[string]any{"Title": "phone", "Page": "connect", "Fleets": b.fleets(ctx), "StripData": b.stripData(ctx), "Token": s.CSRF, "Phone": s.Device}
	if s.Device == nil {
		devices, err := b.st.Devices(ctx)
		if err != nil {
			b.failErr(w, err)
			return
		}
		b.phones.mu.Lock()
		rows := make([]deviceRow, len(devices))
		for i, d := range devices {
			rows[i] = deviceRow{d, b.phones.seen[d.ID], b.lapsed(d)}
		}
		b.phones.mu.Unlock()
		data["Devices"] = rows
		link := PhoneLink{}
		if b.o.Phone != nil {
			link = b.o.Phone(ctx)
		}
		data["Link"], data["Address"] = link, link.Ready()
		set, _ := extra["Set"].([]string)
		data["Choices"] = b.pairChoices(ctx, set)
	}
	for k, v := range extra {
		data[k] = v
	}
	b.render(w, status, "connect.html", data)
}

func (b *Board) chosenFleets(r *http.Request) ([]string, bool) {
	own := b.fleetID()
	offered := map[string]bool{}
	for _, l := range b.laptopFleets(r.Context()) {
		offered[l.ID] = true
	}
	set := []string{own}
	for _, id := range r.PostForm["fleet"] {
		if id == own || slices.Contains(set, id) {
			continue
		}
		if !offered[id] {
			return nil, false
		}
		set = append(set, id)
	}
	return set, true
}

func joined(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func (b *Board) fleetNames(ctx context.Context, set []string) []string {
	byID := map[string]string{}
	for _, l := range b.laptopFleets(ctx) {
		byID[l.ID] = l.Name
	}
	names := make([]string, 0, len(set))
	for _, id := range set {
		names = append(names, byID[id])
	}
	return names
}

func (b *Board) connectCode(w http.ResponseWriter, r *http.Request) {
	if s, _ := r.Context().Value(sessionKey{}).(session); s.Device != nil {
		b.fail(w, http.StatusForbidden, "a paired phone cannot pair another device; show the code on your laptop")
		return
	}
	set, ok := b.chosenFleets(r)
	if !ok {
		b.fail(w, http.StatusForbidden, "this browser is not signed in to every fleet you chose; reload the Phone page and choose again")
		return
	}
	base, err := cleanBase(r.PostFormValue("base"))
	if err != nil {
		b.connectPage(w, r, http.StatusBadRequest, map[string]any{"AddressError": err.Error(), "Address": r.PostFormValue("base"), "Set": set})
		return
	}
	code, until := b.mint(b.now(), set)
	link := base + b.o.Base + "/pair#" + code
	qr, _ := qrSVG(link, "QR code for the pairing link")
	w.Header().Set("Cache-Control", "no-store")
	extra := map[string]any{"Code": code, "PairLink": link, "QR": qr, "Expires": until, "Address": base, "Plain": plainHTTP(base), "PairURL": base + b.o.Base + "/pair", "Set": set}
	if len(set) > 1 {
		extra["PairsWith"] = joined(b.fleetNames(r.Context(), set))
	}
	b.connectPage(w, r, http.StatusOK, extra)
}

func (b *Board) connectRevoke(w http.ResponseWriter, r *http.Request) {
	s, _ := r.Context().Value(sessionKey{}).(session)
	id, err := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	if err != nil {
		b.fail(w, http.StatusBadRequest, "no phone was named")
		return
	}
	if s.Device != nil && s.Device.ID != id {
		b.fail(w, http.StatusForbidden, "a paired phone can sign out only itself")
		return
	}
	if _, err := b.st.RevokeDevice(r.Context(), id); err != nil && !errors.Is(err, state.ErrNotFound) {
		b.failErr(w, err)
		return
	}
	b.phones.mu.Lock()
	delete(b.phones.seen, id)
	b.phones.mu.Unlock()
	if s.Device != nil {
		http.SetCookie(w, &http.Cookie{Name: b.phoneCookie(r), Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteStrictMode})
		b.fail(w, http.StatusOK, "This phone is signed out. Pair it again from the Board on your laptop: Phone, in its menu.")
		return
	}
	http.Redirect(w, r, b.o.Base+"/connect", http.StatusSeeOther)
}

var pairFailure = map[pairResult]struct {
	status int
	text   string
}{
	pairWrong:   {http.StatusForbidden, "That code is not right. Check it against the one on your laptop, or show a new code there."},
	pairExpired: {http.StatusForbidden, "That code has expired. A code works for 5 minutes. Show a new code on your laptop."},
	pairUsed:    {http.StatusForbidden, "That code was already used. A code works once. Show a new code on your laptop."},
	pairBurned:  {http.StatusForbidden, "Too many wrong codes. Every code was cancelled. Show a new code on your laptop."},
	pairBlocked: {http.StatusTooManyRequests, "Too many wrong codes from this phone. Wait a minute, then try again."},
}

func (b *Board) pairPage(w http.ResponseWriter, r *http.Request, status int, msg, name string) {
	w.Header().Set("Cache-Control", "no-store")
	b.render(w, status, "pair.html", map[string]any{"Title": "pair", "Page": "", "Error": msg, "Name": name, "Login": tailnetLogin(r), "Neutral": true})
}

func (b *Board) pairFleet(ctx context.Context, rd *Redeemed, id string) (Paired, error) {
	if id == b.fleetID() {
		return b.PairPhone(ctx, rd)
	}
	if grant, ok := ctx.Value(pairKey{}).(func(context.Context, *Redeemed, string) (Paired, error)); ok {
		return grant(ctx, rd, id)
	}
	return Paired{}, errors.New("this board cannot reach another fleet")
}

func (b *Board) PairPhone(ctx context.Context, rd *Redeemed) (Paired, error) {
	id := b.fleetID()
	if !rd.take(id) {
		return Paired{}, errNotInCode
	}
	f, err := b.st.Fleet(ctx)
	if err != nil {
		return Paired{}, err
	}
	d, err := b.st.AddDevice(ctx, rd.name, rd.login, b.secretHash(rd.secret), rd.label)
	if err != nil {
		return Paired{}, err
	}
	return Paired{ID: id, Name: f.Name, Device: d.ID}, nil
}

func (b *Board) pair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		b.pairPage(w, r, http.StatusOK, "", "Phone")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		b.pairPage(w, r, http.StatusBadRequest, "That form could not be read. Reload the page and try again.", "Phone")
		return
	}
	name := r.PostFormValue("name")
	res, set := b.redeem(remote(r), r.PostFormValue("code"), b.now())
	if res != pairOK {
		f := pairFailure[res]
		b.pairPage(w, r, f.status, f.text, name)
		return
	}
	rd := &Redeemed{fleets: set, done: map[string]bool{}, secret: randomText(32), name: name, login: tailnetLogin(r), label: randomText(state.PhoneLabelLen / 2)}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), pairWait)
	defer cancel()
	var paired []Paired
	var failed error
	for _, id := range set {
		p, err := b.pairFleet(ctx, rd, id)
		if err == nil && p.ID != id {
			err = errNotInCode
		}
		if err != nil {
			failed = err
			continue
		}
		paired = append(paired, p)
	}
	if len(paired) == 0 {
		if len(set) == 1 {
			b.failNeutralErr(w, failed)
			return
		}
		b.failNeutral(w, http.StatusInternalServerError, "No fleet could be paired, and the code is spent. Show a new code on your laptop.")
		return
	}
	for _, p := range paired {
		setPhoneCookie(w, r, p.ID, p.Device, rd.secret)
	}
	if len(set) == 1 {
		http.Redirect(w, r, b.o.Base+"/", http.StatusSeeOther)
		return
	}
	names := make([]string, len(paired))
	for i, p := range paired {
		names[i] = p.Name
	}
	w.Header().Set("Cache-Control", "no-store")
	b.render(w, http.StatusOK, "pair.html", map[string]any{"Title": "paired", "Page": "", "Neutral": true, "Paired": paired, "PairedWith": joined(names), "Missed": len(set) - len(paired)})
}
