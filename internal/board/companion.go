package board

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/atqamz/hand/internal/sidecar"
	"github.com/atqamz/hand/internal/state"
)

const (
	pairLife      = 5 * time.Minute
	pairKeep      = time.Hour
	pairFails     = 10
	addrFails     = 5
	addrWindow    = time.Minute
	companionLife = 90 * 24 * time.Hour
	codeChars     = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	hostCookie    = "__Host-"
	companionName = "hand_phone"
	pairWait      = 20 * time.Second
	appLinkMax    = 213
	appLinkHead   = "handboard://c#"
	appCodeLen    = 10
)

var (
	errAppLink = errors.New("this fleet cannot show a pairing link")
	appIDre    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	appCodeRe  = regexp.MustCompile(`^[` + codeChars + `]{10}$`)
	appRelayRe = regexp.MustCompile(`^https://([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*\.?)(:([1-9][0-9]{0,4}))?/?$`)
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
	PairCompanion(ctx context.Context, r *Redeemed) (Paired, error)
}

type pairKey struct{}

type hostKey struct{}

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

type companions struct {
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
	companionGone
	companionOther
)

func (b *Board) mac(what string) string {
	m := hmac.New(sha256.New, []byte(b.token))
	m.Write([]byte(what))
	return hex.EncodeToString(m.Sum(nil))
}

func (b *Board) secretHash(secret string) string { return b.mac("device:" + secret) }

func (b *Board) deviceCSRF(secret string) string { return b.mac("csrf:" + secret)[:32] }

func proxied(r *http.Request) bool {
	return len(r.Header.Values("X-Forwarded-For")) > 0 || len(r.Header.Values("Forwarded")) > 0
}

func forwarded(r *http.Request) bool { return peerOf(r) != nil || proxied(r) }

func viaFunnel(r *http.Request) bool { return r.Header.Get("Tailscale-Funnel-Request") != "" }

const funnelRefusal = "This board does not answer the public internet. Turn Tailscale Funnel off for it, for example with `tailscale funnel reset`."

func secure(r *http.Request) bool { return r.Header.Get("X-Forwarded-Proto") == "https" }

func (b *Board) fleetID() string { return strings.TrimPrefix(b.o.Base, "/") }

func companionCookieName(id string, https bool) string {
	name := companionName
	if id != "" {
		name += "_" + id
	}
	if https {
		return hostCookie + name
	}
	return name
}

func (b *Board) companionCookie(r *http.Request) string {
	return companionCookieName(b.fleetID(), secure(r))
}

func setCompanionCookie(w http.ResponseWriter, r *http.Request, id string, device int64, secret string) {
	http.SetCookie(w, &http.Cookie{Name: companionCookieName(id, secure(r)), Value: strconv.FormatInt(device, 10) + "." + secret, Path: "/", MaxAge: int(companionLife / time.Second), HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteLaxMode})
}

func deviceLogin(r *http.Request) string {
	if c := peerOf(r); c != nil {
		return c.login()
	}
	return ""
}

func remote(r *http.Request) string {
	if c := peerOf(r); c != nil {
		return c.addr()
	}
	chain := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if last := strings.TrimSpace(chain[strings.LastIndex(chain, ",")+1:]); last != "" {
		return "addr:" + last
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return "addr:" + host
	}
	return "addr:" + r.RemoteAddr
}

func (b *Board) session(r *http.Request) (session, sessionFault) {
	s, fault := b.identify(r)
	if fault == signedIn && s.Device != nil {
		b.companions.mu.Lock()
		b.companions.seen[s.Device.ID] = b.now()
		b.companions.mu.Unlock()
	}
	return s, fault
}

func (b *Board) CompanionSession(r *http.Request) bool {
	_, fault := b.companionIdentify(r)
	return fault == signedIn
}

func (b *Board) identify(r *http.Request) (session, sessionFault) {
	if c, err := r.Cookie(cookieFor(b.o.Base)); err == nil && peerOf(r) == nil && b.valid(c.Value) {
		return session{CSRF: b.token}, signedIn
	}
	return b.companionIdentify(r)
}

func (b *Board) companionIdentify(r *http.Request) (session, sessionFault) {
	c, err := r.Cookie(b.companionCookie(r))
	if err != nil {
		return session{}, noSession
	}
	idText, secret, ok := strings.Cut(c.Value, ".")
	id, err := strconv.ParseInt(idText, 10, 64)
	if !ok || err != nil || id < 1 || len(secret) != 64 {
		return session{}, companionGone
	}
	hash := b.secretHash(secret)
	d, ok, err := b.st.DeviceWithSecret(r.Context(), id, hash)
	if err != nil || !ok {
		return session{}, companionGone
	}
	if b.lapsed(d) {
		return session{}, companionGone
	}
	if d.Login != "" && subtle.ConstantTimeCompare([]byte(d.Login), []byte(deviceLogin(r))) != 1 {
		return session{}, companionOther
	}
	return session{Device: &d, CSRF: b.deviceCSRF(secret), hash: hash}, signedIn
}

func sessionFaults(f sessionFault, viaProxy bool) string {
	switch {
	case f == companionGone:
		return "This phone is no longer paired with this fleet. Pair it again from the host's Board: Phone, in its menu."
	case f == companionOther:
		return "This phone was paired on another connection, or reached the Board another way. Open the address it was paired with, or pair it again from the host's Board."
	case viaProxy:
		return "Pair this phone first: open this fleet's Board, choose Phone in its menu, and show a pairing code."
	}
	return "Open this fleet with `hand open` from inside it. On another device or browser, paste the link `hand open --print` prints."
}

func (b *Board) closePeers(devices ...state.Device) {
	if b.o.ClosePeer == nil {
		return
	}
	for _, d := range devices {
		if id, ok := strings.CutPrefix(d.Login, "iroh:"); ok {
			b.o.ClosePeer(id)
		}
	}
}

func (b *Board) lapsed(d state.Device) bool { return b.now().After(d.PairedAt.Add(companionLife)) }

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
	b.companions.mu.Lock()
	defer b.companions.mu.Unlock()
	b.companions.prune(now)
	for c, v := range b.companions.codes {
		if !v.used && !now.After(v.until) {
			delete(b.companions.codes, c)
		}
	}
	b.companions.codes[code] = pairCode{until: until, fleets: slices.Clone(fleets)}
	return code, until
}

func (p *companions) prune(now time.Time) {
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

func (p *companions) burn() {
	clear(p.codes)
	clear(p.tries)
	p.fails = 0
}

func (b *Board) redeem(addr, typed string, now time.Time) (pairResult, []string) {
	p := &b.companions
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
		return "", errors.New("type the address your phone opens, like https://host.example")
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

type fleetChoice struct {
	ID, Name        string
	Current, Ticked bool
}

func (b *Board) pairChoices(ctx context.Context, set []string) []fleetChoice {
	list := b.hostFleets(ctx)
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

type irohInfo struct{ Tone, State, Text, ID string }

func irohView(st sidecar.Status) *irohInfo {
	switch st.State {
	case sidecar.Ready:
		if st.Relay == "none" {
			return &irohInfo{Tone: "wait", State: "no relay", ID: st.ID, Text: "running, but it reached no relay, so a phone elsewhere cannot dial it."}
		}
		return &irohInfo{Tone: "pass", State: "ready", ID: st.ID, Text: "online. The Hand app can pair with this host."}
	case sidecar.Starting:
		text := "starting."
		if st.Reason != "" {
			text = "restarting after: " + Scrub(st.Reason) + "."
		}
		return &irohInfo{Tone: "wait", State: "starting", Text: text}
	case sidecar.Failed:
		return &irohInfo{Tone: "fail", State: "failed", Text: Scrub(st.Reason) + "."}
	}
	return nil
}

func (b *Board) connectPage(w http.ResponseWriter, r *http.Request, status int, extra map[string]any) {
	ctx := r.Context()
	s, _ := ctx.Value(sessionKey{}).(session)
	data := map[string]any{"Title": "phone", "Page": "connect", "Fleets": b.fleets(ctx), "StripData": b.stripData(ctx), "Token": s.CSRF, "Phone": s.Device}
	if s.Device == nil {
		own, err := b.PhoneRecords(ctx)
		if err != nil {
			b.failErr(w, err)
			return
		}
		entries, unread := b.phoneEntries(ctx, own)
		data["Devices"], data["Unread"] = entries, joined(unread)
		var named []string
		for _, d := range own {
			if strings.HasPrefix(d.Login, "iroh:") {
				named = append(named, d.Name)
			}
		}
		data["AppPaired"] = joined(named)
		if b.o.Iroh != nil {
			data["Iroh"] = irohView(b.o.Iroh())
		}
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
	for _, l := range b.hostFleets(r.Context()) {
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
	for _, l := range b.hostFleets(ctx) {
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
		b.fail(w, http.StatusForbidden, "a paired phone cannot pair another device; show the code on the host's Board")
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

func (b *Board) connectApp(w http.ResponseWriter, r *http.Request) {
	if s, _ := r.Context().Value(sessionKey{}).(session); s.Device != nil {
		b.fail(w, http.StatusForbidden, "a paired phone cannot pair another device; show the link on the host's Board")
		return
	}
	var st sidecar.Status
	if b.o.Iroh != nil {
		st = b.o.Iroh()
	}
	if st.State != sidecar.Ready || st.ID == "" || st.Relay == "" || st.Relay == "none" {
		b.connectPage(w, r, http.StatusBadRequest, map[string]any{"AppError": "Phone access over iroh is not ready with a relay, so a phone cannot reach this host yet. Start the Board with `--iroh`, or check the sidecar's state above."})
		return
	}
	if _, err := appLink(st.ID, st.Relay, b.fleetID(), "K7QM3XRD2P"); err != nil {
		b.connectPage(w, r, http.StatusInternalServerError, map[string]any{"AppError": "This host cannot show a pairing link: its iroh relay address does not fit one."})
		return
	}
	code, until := b.mint(b.now(), []string{b.fleetID()})
	link, err := appLink(st.ID, st.Relay, b.fleetID(), code)
	if err != nil {
		b.connectPage(w, r, http.StatusInternalServerError, map[string]any{"AppError": "This host cannot show a pairing link: its iroh relay address does not fit one."})
		return
	}
	qr, _ := qrSVG(link, "QR code for the Hand app pairing link")
	w.Header().Set("Cache-Control", "no-store")
	b.connectPage(w, r, http.StatusOK, map[string]any{"AppLink": link, "AppQR": qr, "AppExpires": until})
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
	var revoked *state.Device
	if d, err := b.st.RevokeDevice(r.Context(), id); err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			b.failErr(w, err)
			return
		}
	} else {
		revoked = &d
	}
	b.companions.mu.Lock()
	delete(b.companions.seen, id)
	b.companions.mu.Unlock()
	if s.Device != nil {
		http.SetCookie(w, &http.Cookie{Name: b.companionCookie(r), Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteLaxMode})
		b.fail(w, http.StatusOK, "This phone is signed out. Pair it again from the host's Board: Phone, in its menu.")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if revoked != nil {
			b.closePeers(*revoked)
		}
		return
	}
	if revoked != nil {
		b.closePeers(*revoked)
	}
	http.Redirect(w, r, b.o.Base+"/connect", http.StatusSeeOther)
}

var pairFailure = map[pairResult]struct {
	status     int
	code, text string
}{
	pairWrong:   {http.StatusForbidden, "wrong_code", "That code is not right. Check it against the one on the Board, or show a new code there."},
	pairExpired: {http.StatusForbidden, "code_expired", "That code has expired. A code works for 5 minutes. Show a new code on the Board."},
	pairUsed:    {http.StatusForbidden, "code_used", "That code was already used. A code works once. Show a new code on the Board."},
	pairBurned:  {http.StatusForbidden, "codes_cancelled", "Too many wrong codes. Every code was cancelled. Show a new code on the Board."},
	pairBlocked: {http.StatusTooManyRequests, "rate_limited", "Too many wrong codes from this phone. Wait a minute, then try again."},
}

func (b *Board) pairPage(w http.ResponseWriter, r *http.Request, status int, msg, name string) {
	w.Header().Set("Cache-Control", "no-store")
	b.render(w, status, "pair.html", map[string]any{"Title": "pair", "Page": "", "Error": msg, "Name": name, "Login": deviceLogin(r), "Neutral": true})
}

func (b *Board) pairFleet(ctx context.Context, rd *Redeemed, id string) (Paired, error) {
	if id == b.fleetID() {
		return b.PairCompanion(ctx, rd)
	}
	if grant, ok := ctx.Value(pairKey{}).(func(context.Context, *Redeemed, string) (Paired, error)); ok {
		return grant(ctx, rd, id)
	}
	return Paired{}, errors.New("this board cannot reach another fleet")
}

func (b *Board) PairCompanion(ctx context.Context, rd *Redeemed) (Paired, error) {
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

func (b *Board) pairSet(r *http.Request, set []string, name string) (*Redeemed, []Paired, error) {
	rd := &Redeemed{fleets: set, done: map[string]bool{}, secret: randomText(32), name: name, login: deviceLogin(r), label: randomText(state.PhoneLabelLen / 2)}
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
	return rd, paired, failed
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
	rd, paired, failed := b.pairSet(r, set, name)
	if len(paired) == 0 {
		if len(set) == 1 {
			b.failNeutralErr(w, failed)
			return
		}
		b.failNeutral(w, http.StatusInternalServerError, "No fleet could be paired, and the code is spent. Show a new code on the Board.")
		return
	}
	for _, p := range paired {
		setCompanionCookie(w, r, p.ID, p.Device, rd.secret)
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

func appLink(endpoint, relay, fleet, code string) (string, error) {
	code = strings.ReplaceAll(code, "-", "")
	if !appIDre.MatchString(endpoint) || !state.FleetID.MatchString(fleet) || len(code) != appCodeLen || !appCodeRe.MatchString(code) {
		return "", errAppLink
	}
	link := appLinkHead + "v=1&e=" + endpoint + "&f=" + fleet + "&c=" + code
	if !publicRelay(relay) {
		if !appRelay(relay) {
			return "", errAppLink
		}
		link += "&r=" + appEscape(relay)
	}
	if len(link) > appLinkMax {
		return "", errAppLink
	}
	return link, nil
}

func publicRelay(relay string) bool {
	if relay == "" || relay == "none" {
		return true
	}
	u, err := url.Parse(relay)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	return host == "n0.iroh.link" || strings.HasSuffix(host, ".n0.iroh.link")
}

func appRelay(relay string) bool {
	m := appRelayRe.FindStringSubmatch(relay)
	if m == nil || len(m[1]) > 253 {
		return false
	}
	labels := strings.Split(strings.TrimSuffix(m[1], "."), ".")
	if numericLabel(labels[len(labels)-1]) {
		return false
	}
	if m[6] != "" {
		if port, _ := strconv.Atoi(m[6]); port > 65535 {
			return false
		}
	}
	return true
}

func numericLabel(label string) bool {
	if strings.Trim(label, "0123456789") == "" {
		return true
	}
	rest, ok := strings.CutPrefix(label, "0x")
	return ok && rest != "" && strings.Trim(rest, "0123456789abcdef") == ""
}

func appEscape(s string) string {
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			const hex = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}
