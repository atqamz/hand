package board

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/markdown"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

//go:embed templates/*.html
var files embed.FS

var pages = template.Must(template.New("").Funcs(template.FuncMap{
	"task":     state.TaskRef,
	"attempt":  state.AttemptRef,
	"report":   state.ReportRef,
	"decision": state.DecisionRef,
	"plan":     func(rev int) string { return state.PlanRef(rev) },
	"asset":    assetURL,
	"md":       markdown.Render,
	"mdrefs":   markdown.RenderRefs,
	"refs":     markdown.Refs,
	"inline":   markdown.Inline,
	"when":     when,
	"options":  options,
	"prose":    prose,
	"chosen":   chosen,
	"note":     note,
	"tabonce":  tabOnce,
	"away":     away,
	"plain":    plain,
	"shown":    shown,
	"rest": func(s string) string {
		_, rest, _ := strings.Cut(strings.TrimSpace(s), "\n")
		return strings.TrimSpace(rest)
	},
	"words": func(kind string) string { return strings.NewReplacer(".", " ", "_", " ").Replace(kind) },
	"hm":    hm,
	"base":  base,
	"tint":  tint,
	"view": func(root map[string]any, w waiting, open bool) map[string]any {
		return map[string]any{"R": root, "W": w, "Open": open}
	},
	"rep": func(root map[string]any, r state.Report) map[string]any {
		return map[string]any{"R": root, "Rep": r}
	},
	"pick": func(root map[string]any, d state.Decision, needs bool) map[string]any {
		return map[string]any{"R": root, "D": d, "Needs": needs}
	},
}).ParseFS(files, "templates/*.html"))

const (
	cookieName = "hand_board"
	csp        = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; media-src 'self'; frame-src 'self'; form-action 'self'; frame-ancestors 'none'; manifest-src 'self'; worker-src 'self'"
)

func cookieFor(base string) string { return cookieName + strings.ReplaceAll(base, "/", "_") }

var eventKind = regexp.MustCompile(`\b(attempt|decision|report|supervisor|task|timer)\.([a-z]+)\b`)

var refColon = regexp.MustCompile(`\b([adprst][0-9]+):\s*(;|$)`)

func plain(s string) string {
	s = eventKind.ReplaceAllString(strings.TrimPrefix(s, "wake: "), "$1 $2")
	return strings.TrimSpace(refColon.ReplaceAllString(s, "$1$2"))
}

var refAnchor = regexp.MustCompile(`<a class="ref" `)

func away(on any, h template.HTML) template.HTML {
	if on != true {
		return h
	}
	return template.HTML(strings.ReplaceAll(string(h), `<a class="ref" href=`, `<a class="ref" target="_blank" rel="noopener" href=`))
}

func tabOnce(h template.HTML) template.HTML {
	n := 0
	return template.HTML(refAnchor.ReplaceAllStringFunc(string(h), func(m string) string {
		if n++; n == 1 {
			return m
		}
		return `<a class="ref" tabindex="-1" `
	}))
}

var finished = []string{state.StatusDone, state.StatusAbandoned}

var (
	taskPill     = map[string]string{state.StatusActive: "running", state.StatusInbox: "neutral", state.StatusDone: "passing", state.StatusAbandoned: "neutral"}
	decisionPill = map[string]string{state.DecisionOpen: "waiting", state.DecisionAnswered: "passing", state.DecisionWithdrawn: "neutral"}
)

const (
	maxCards       = 500
	historyLimit   = 50
	railFinished   = 5
	archivePage    = 50
	maxArchivePage = 1_000_000
)

type Options struct {
	Controls   bool
	Control    func(ctx context.Context, args ...string) (warn string, err error)
	Luvus      luvus.Client
	Transcript *transcript.Reader
	Home       string
	Base       string
	Tick       time.Duration
	Beat       time.Duration
	Now        func() time.Time
	Harness    harness.Env
	Models     harness.Options
	Phone      func(context.Context) PhoneLink
}

type Board struct {
	st      *state.Store
	token   string
	o       Options
	mux     *http.ServeMux
	phones  phones
	refresh refresher
}

type card struct {
	Task      state.Task
	Plan      *state.Plan
	Attempts  []state.Attempt
	Report    *state.Report
	Decisions []state.Decision
	Delivered []Deliverable
}

func New(st *state.Store, token string, o Options) http.Handler {
	if o.Tick <= 0 {
		o.Tick = time.Second
	}
	if o.Beat <= 0 {
		o.Beat = 15 * time.Second
	}
	b := &Board{st: st, token: token, o: o, mux: http.NewServeMux(), phones: phones{codes: map[string]pairCode{}, tries: map[string]burst{}, seen: map[int64]time.Time{}}, refresh: refresher{busy: map[string]bool{}, started: map[string]time.Time{}}}
	b.mux.HandleFunc("GET /{$}", b.fleet)
	b.mux.HandleFunc("GET /supervisor/log", b.history)
	b.mux.HandleFunc("GET /events", b.events)
	b.mux.HandleFunc("POST /supervisor/start", b.start)
	b.mux.HandleFunc("POST /supervisor/resume", b.simple("resume"))
	b.mux.HandleFunc("POST /supervisor/stop", b.simple("stop"))
	b.mux.HandleFunc("POST /supervisor/interrupt", b.simple("interrupt"))
	b.mux.HandleFunc("POST /supervisor/force", b.simple("force"))
	b.mux.HandleFunc("POST /attempt/{ref}/keys", b.attemptKeys)
	b.mux.HandleFunc("POST /supervisor/keys", b.keys)
	b.mux.HandleFunc("POST /supervisor/send", b.send)
	b.mux.HandleFunc("POST /supervisor/image", b.image)
	b.mux.HandleFunc("GET /inbox/{name}", b.inbox)
	b.mux.HandleFunc("GET /doc/{path...}", b.doc)
	b.mux.HandleFunc("POST /supervisor/switch", b.switchModel)
	b.mux.HandleFunc("POST /models/refresh", b.refreshModels)
	b.mux.HandleFunc("GET /task/{id}", b.task)
	b.mux.HandleFunc("GET /attempt/{id}", b.attempt)
	b.mux.HandleFunc("GET /tasks", b.archive)
	b.mux.HandleFunc("GET /ref/{ref}", b.ref)
	b.mux.HandleFunc("GET /decision/{id}", b.decision)
	b.mux.HandleFunc("POST /decision/{id}/answer", b.answer)
	b.mux.HandleFunc("POST /report/{id}/ack", b.ack)
	b.mux.HandleFunc("GET /connect", b.connect)
	b.mux.HandleFunc("POST /connect/code", b.connectCode)
	b.mux.HandleFunc("POST /connect/revoke", b.connectRevoke)
	b.mux.HandleFunc("POST /connect/revoke/in", b.connectRevokePhone(false))
	b.mux.HandleFunc("POST /connect/revoke/everywhere", b.connectRevokePhone(true))
	return b
}

func (b *Board) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if strings.HasPrefix(r.URL.Path, "/static/") {
		ServeStatic(w, r)
		return
	}
	if viaFunnel(r) {
		b.failNeutral(w, http.StatusForbidden, funnelRefusal)
		return
	}
	if r.URL.Path == "/proof" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, Proof(b.token, r.URL.Query().Get("nonce")))
		return
	}
	if t := r.URL.Query().Get("token"); t != "" {
		if !b.valid(t) {
			b.failNeutral(w, http.StatusForbidden, "this board link is not valid")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: cookieFor(b.o.Base), Value: t, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		q := r.URL.Query()
		q.Del("token")
		next := b.o.Base + r.URL.EscapedPath()
		if len(q) > 0 {
			next += "?" + q.Encode()
		}
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	if r.URL.Path == "/pair" && (r.Method == http.MethodGet || r.Method == http.MethodPost) {
		b.pair(w, r)
		return
	}
	s, fault := b.session(r)
	if fault != signedIn {
		b.failNeutral(w, http.StatusForbidden, sessionFaults(fault, forwarded(r)))
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), sessionKey{}, s))
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, maxPost)
		err := r.ParseForm()
		if err == nil {
			err = r.ParseMultipartForm(maxPost)
		}
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			b.fail(w, http.StatusRequestEntityTooLarge, "the request is too large")
			return
		}
	}
	if r.Method == http.MethodPost && subtle.ConstantTimeCompare([]byte(r.PostFormValue("csrf")), []byte(s.CSRF)) != 1 {
		b.fail(w, http.StatusForbidden, "this form is stale; reload the page and try again")
		return
	}
	b.mux.ServeHTTP(&pageErrors{ResponseWriter: w, b: b}, r)
}

type pageErrors struct {
	http.ResponseWriter
	b    *Board
	skip bool
}

var pageErrorText = map[int]string{http.StatusNotFound: "there is no page here", http.StatusMethodNotAllowed: "this page does not take that kind of request"}

func (p *pageErrors) WriteHeader(code int) {
	if msg, ok := pageErrorText[code]; ok && strings.HasPrefix(p.Header().Get("Content-Type"), "text/plain") {
		p.skip = true
		p.Header().Del("X-Content-Type-Options")
		p.b.fail(p.ResponseWriter, code, msg)
		return
	}
	p.ResponseWriter.WriteHeader(code)
}

func (p *pageErrors) Write(b []byte) (int, error) {
	if p.skip {
		return len(b), nil
	}
	return p.ResponseWriter.Write(b)
}

func (p *pageErrors) Flush() {
	if f, ok := p.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (p *pageErrors) Unwrap() http.ResponseWriter { return p.ResponseWriter }

var (
	optionLine  = regexp.MustCompile(`^\s*(\d+|[A-Z])[.)]\s+(.+)$`)
	recommended = regexp.MustCompile(`(?i)\s*\(recommended\)`)
	consequence = regexp.MustCompile(`\s+[—–-]\s+`)
	other       = regexp.MustCompile(`(?i)^other\b`)
)

type option struct {
	Key, Label, Note, Value  string
	Recommended, Other, Long bool
}

func parseOption(line string) (option, bool) {
	m := optionLine.FindStringSubmatch(line)
	if m == nil {
		return option{}, false
	}
	text := strings.TrimSpace(m[2])
	o := option{Key: m[1], Recommended: recommended.MatchString(text)}
	text = strings.TrimSpace(recommended.ReplaceAllString(text, ""))
	o.Label = text
	if parts := consequence.Split(text, 2); len(parts) == 2 {
		o.Label, o.Note = parts[0], parts[1]
	}
	o.Other, o.Long = other.MatchString(o.Label), utf8.RuneCountInString(o.Label) > 90
	o.Value = o.Key + ". " + o.Label
	return o, o.Label != ""
}

func parseBody(body string) ([]option, string) {
	var opts []option
	var keep []string
	seen := map[string]bool{}
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
		} else if o, ok := parseOption(line); ok && !fenced && !seen[o.Key] {
			seen[o.Key] = true
			opts = append(opts, o)
			continue
		}
		keep = append(keep, line)
	}
	if len(opts) == 0 {
		return nil, body
	}
	return opts, strings.TrimSpace(strings.Join(keep, "\n"))
}

func options(body string) []option {
	opts, _ := parseBody(body)
	return opts
}

func prose(body string) string {
	_, rest := parseBody(body)
	return rest
}

func chosen(opts []option, answer string) string {
	first, _, _ := strings.Cut(answer, "\n")
	if m := optionLine.FindStringSubmatch(first); m != nil {
		for _, o := range opts {
			if o.Key == m[1] {
				return o.Key
			}
		}
	}
	return ""
}

func note(opts []option, answer string) string {
	if chosen(opts, answer) == "" {
		return strings.TrimSpace(answer)
	}
	_, rest, _ := strings.Cut(answer, "\n")
	return strings.TrimSpace(rest)
}

func (b *Board) refTitle(ctx context.Context) func(string) string {
	seen := map[string]string{}
	return func(ref string) string {
		if t, ok := seen[ref]; ok {
			return t
		}
		t := shown(b.describe(ctx, ref))
		seen[ref] = t
		return t
	}
}

func (b *Board) describe(ctx context.Context, ref string) string {
	if len(ref) < 2 {
		return ""
	}
	id, err := strconv.ParseInt(ref[1:], 10, 64)
	if err != nil {
		return ""
	}
	switch ref[0] {
	case 't':
		if t, err := b.st.Task(ctx, id); err == nil {
			return t.Title
		}
	case 'a':
		if a, err := b.st.Attempt(ctx, id); err == nil {
			if t, err := b.st.Task(ctx, a.TaskID); err == nil {
				return strings.Join(strings.Fields("on "+state.TaskRef(t.ID)+` "`+t.Title+`" · `+a.Harness+" "+a.Model), " ")
			}
		}
	case 'd':
		if d, err := b.st.Decision(ctx, id); err == nil {
			return d.Headline()
		}
	case 'r':
		if r, err := b.st.Report(ctx, id); err == nil {
			return r.Summary()
		}
	}
	return ""
}

func (b *Board) now() time.Time {
	if b.o.Now != nil {
		return b.o.Now()
	}
	return time.Now()
}

func Proof(token, nonce string) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = io.WriteString(mac, nonce)
	return hex.EncodeToString(mac.Sum(nil))
}

func (b *Board) valid(t string) bool {
	return subtle.ConstantTimeCompare([]byte(t), []byte(b.token)) == 1
}

func (b *Board) render(w http.ResponseWriter, status int, name string, data map[string]any) {
	renderPage(w, status, name, b.pageData(data))
}

func (b *Board) pageData(data map[string]any) map[string]any {
	data["Base"], data["Controls"] = b.o.Base, b.o.Controls
	if _, ok := data["Titles"]; !ok {
		data["Titles"] = b.refTitle(context.Background())
	}
	if _, ok := data["Local"]; !ok {
		data["Local"] = b.local()
	}
	data["Fleet"] = "hand"
	if neutral, _ := data["Neutral"].(bool); neutral {
		data["Fleet"] = "this Board"
	} else if f, err := b.st.Fleet(context.Background()); err == nil {
		data["Fleet"], data["FleetID"] = f.Name, f.ID
	}
	return data
}

func renderPage(w http.ResponseWriter, status int, name string, data map[string]any) {
	data["Worker"] = installed(w)
	var buf bytes.Buffer
	if err := pages.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (b *Board) fail(w http.ResponseWriter, status int, msg string) {
	b.failWith(w, status, msg, nil)
}

func (b *Board) failNeutral(w http.ResponseWriter, status int, msg string) {
	b.failWith(w, status, msg, map[string]any{"Neutral": true})
}

func (b *Board) failWith(w http.ResponseWriter, status int, msg string, extra map[string]any) {
	w.Header().Set("X-Hand-Error", url.PathEscape(strings.Join(strings.Fields(msg), " ")))
	data := map[string]any{"Title": strconv.Itoa(status), "Status": status, "Message": sentence(msg)}
	for k, v := range extra {
		data[k] = v
	}
	b.render(w, status, "error.html", data)
}

func sentence(msg string) string {
	msg = strings.TrimSpace(msg)
	if rest, ok := strings.CutPrefix(msg, "not found: "); ok {
		msg = rest + " was not found"
	}
	if msg == "" {
		return msg
	}
	if !strings.HasSuffix(msg, ".") && !strings.HasSuffix(msg, "?") && !strings.HasSuffix(msg, "!") {
		msg += "."
	}
	r, n := utf8.DecodeRuneInString(msg)
	return string(unicode.ToUpper(r)) + msg[n:]
}

func (b *Board) failErr(w http.ResponseWriter, err error) {
	status, msg := failure(err)
	b.failWith(w, status, msg, map[string]any{"StripData": b.stripData(context.Background())})
}

func (b *Board) failNeutralErr(w http.ResponseWriter, err error) {
	status, msg := failure(err)
	b.failNeutral(w, status, msg)
}

func failure(err error) (int, string) {
	status, msg := http.StatusInternalServerError, Scrub(err.Error())
	switch {
	case errors.Is(err, state.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, state.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, state.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, syscall.ENOSPC):
		status, msg = http.StatusInsufficientStorage, "the disk is full; free some space and try again"
	}
	return status, msg
}

func tint(id any) int {
	s, _ := id.(string)
	if s == "" {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return int(h.Sum32() % 12)
}

var absPath = regexp.MustCompile(`(^|[\s"'(=])(?:/|[A-Za-z]:[\\/]|\\\\[?]\\[A-Za-z]:[\\/]|\\\\)(?:[^\s"'():,/\\]+(?: [^\s"'():,/\\]+)*[/\\])*[^\s"'():,]+`)

func Scrub(msg string) string {
	return absPath.ReplaceAllStringFunc(msg, func(m string) string {
		lead := absPath.FindStringSubmatch(m)[1]
		p := strings.TrimRight(m[len(lead):], `/\`)
		return lead + p[strings.LastIndexAny(p, `/\`)+1:]
	})
}

func (b *Board) ref(w http.ResponseWriter, r *http.Request) {
	ctx, ref := r.Context(), r.PathValue("ref")
	missing := fmt.Errorf("%w: %q is not a known ref", state.ErrNotFound, ref)
	id, err := strconv.ParseInt(ref[min(1, len(ref)):], 10, 64)
	if err != nil || id <= 0 {
		b.failErr(w, missing)
		return
	}
	var to string
	switch ref[0] {
	case 't':
		_, err = b.st.Task(ctx, id)
		to = "/task/" + ref
	case 'd':
		_, err = b.st.Decision(ctx, id)
		to = "/decision/" + ref
	case 'a':
		var a state.Attempt
		a, err = b.st.Attempt(ctx, id)
		to = "/attempt/" + state.AttemptRef(a.ID)
	case 'r':
		var rep state.Report
		rep, err = b.st.Report(ctx, id)
		to = "/task/" + state.TaskRef(rep.TaskID) + "#" + ref
	default:
		err = missing
	}
	if err != nil {
		b.failErr(w, err)
		return
	}
	http.Redirect(w, r, b.o.Base+to, http.StatusSeeOther)
}

func pathID(r *http.Request, prefix string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimPrefix(r.PathValue("id"), prefix), 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%w: %q is not a %s id", state.ErrNotFound, r.PathValue("id"), prefix)
	}
	return n, nil
}

func (b *Board) card(ctx context.Context, t state.Task, attempts int) (card, error) {
	c := card{Task: t}
	p, ok, err := b.st.CurrentPlan(ctx, t.ID)
	if err != nil {
		return c, err
	}
	if ok {
		c.Plan = &p
	}
	if c.Attempts, err = b.st.Attempts(ctx, t.ID, attempts); err != nil {
		return c, err
	}
	slices.Reverse(c.Attempts)
	if c.Decisions, err = b.st.OpenDecisions(ctx, t.ID, 20); err != nil {
		return c, err
	}
	reports, err := b.st.Reports(ctx, state.ReportFilter{TaskID: t.ID}, 20)
	if err != nil {
		return c, err
	}
	events, err := b.st.DeliveredEvents(ctx, t.ID)
	if err != nil {
		return c, err
	}
	c.Delivered = b.linked(Newest(reports, events))
	if len(reports) > 0 {
		c.Report = &reports[len(reports)-1]
	}
	return c, nil
}

func (b *Board) tasksData(ctx context.Context, data map[string]any, q url.Values) error {
	all := q.Get("all") == "1"
	groups := []string{state.StatusActive, state.StatusInbox}
	if all {
		groups = append(groups, state.StatusDone, state.StatusAbandoned)
	}
	var tasks []state.Task
	for _, status := range groups {
		if len(tasks) >= maxCards {
			break
		}
		part, err := b.st.Tasks(ctx, []string{status}, maxCards-len(tasks))
		if err != nil {
			return err
		}
		tasks = append(tasks, part...)
	}
	shown := len(tasks)
	if !all {
		recent, _, err := b.st.SearchTasks(ctx, state.TaskQuery{Statuses: finished, Limit: railFinished})
		if err != nil {
			return err
		}
		tasks = append(tasks, recent...)
	}
	f, _ := data["facts"].(facts)
	delivered, err := b.deliverables(ctx, tasks)
	if err != nil {
		return err
	}
	f.delivered = delivered
	checks := make([]check, 0, len(tasks))
	for _, t := range tasks {
		checks = append(checks, f.check(t))
	}
	counts, err := b.st.CountTasks(ctx)
	if err != nil {
		return err
	}
	total := counts[state.StatusActive] + counts[state.StatusInbox]
	if all {
		total += counts[state.StatusDone] + counts[state.StatusAbandoned]
	}
	var sections []taskGroup
	for _, status := range groups {
		g := taskGroup{Name: strings.ToUpper(status[:1]) + status[1:], Count: counts[status]}
		for _, c := range checks[:shown] {
			if c.Task.Status == status {
				g.Checks = append(g.Checks, c)
			}
		}
		if len(g.Checks) > 0 {
			sections = append(sections, g)
		}
	}
	if recent := checks[shown:]; len(recent) > 0 {
		sections = append(sections, taskGroup{Name: "Finished", Count: counts[state.StatusDone] + counts[state.StatusAbandoned], Checks: recent})
	}
	data["Groups"], data["All"], data["Hidden"] = sections, all, total-shown
	data["FinishedCount"] = counts[state.StatusDone] + counts[state.StatusAbandoned]
	return nil
}

type taskGroup struct {
	Name   string
	Count  int
	Checks []check
}

func (b *Board) task(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "t")
	if err != nil {
		b.failErr(w, err)
		return
	}
	t, err := b.st.Task(ctx, id)
	if err != nil {
		b.failErr(w, err)
		return
	}
	c, err := b.card(ctx, t, 50)
	if err != nil {
		b.failErr(w, err)
		return
	}
	reports, err := b.st.Reports(ctx, state.ReportFilter{TaskID: id}, historyLimit+1)
	if err != nil {
		b.failErr(w, err)
		return
	}
	unread, err := b.st.Reports(ctx, state.ReportFilter{TaskID: id, Unacked: true}, maxCards)
	if err != nil {
		b.failErr(w, err)
		return
	}
	moreReports := len(reports) > historyLimit
	if moreReports {
		reports = reports[1:]
	}
	slices.Reverse(reports)
	events, err := b.st.TaskEvents(ctx, id, historyLimit+1)
	if err != nil {
		b.failErr(w, err)
		return
	}
	moreEvents := len(events) > historyLimit
	if moreEvents {
		events = events[1:]
	}
	slices.Reverse(events)
	done, err := b.st.DoneReportAttempts(ctx)
	if err != nil {
		b.failErr(w, err)
		return
	}
	rows := make([]check, 0, len(c.Attempts))
	for _, a := range c.Attempts {
		rows = append(rows, check{Task: t, Attempt: &a, State: attemptState(a, done)})
	}
	decisions, err := b.st.Decisions(ctx, id, historyLimit)
	if err != nil {
		b.failErr(w, err)
		return
	}
	b.render(w, http.StatusOK, "task.html", map[string]any{
		"Title": state.TaskRef(id), "Page": "task", "Fleets": b.fleets(ctx), "Card": c, "Token": b.csrf(ctx), "Pill": taskPill[t.Status], "Attempts": rows, "StripData": b.stripData(ctx), "Decisions": decisions, "Titles": b.refTitle(ctx),
		"Unread": unread, "Reports": reports, "MoreReports": moreReports, "Events": events, "MoreEvents": moreEvents,
	})
}

func (b *Board) decision(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "d")
	if err != nil {
		b.failErr(w, err)
		return
	}
	b.decisionPage(w, r, id, http.StatusOK, "")
}

func (b *Board) decisionPage(w http.ResponseWriter, r *http.Request, id int64, status int, notice string) {
	ctx := r.Context()
	d, err := b.st.Decision(ctx, id)
	if err != nil {
		b.failErr(w, err)
		return
	}
	t, err := b.st.Task(ctx, d.TaskID)
	if err != nil {
		b.failErr(w, err)
		return
	}
	b.render(w, status, "decision.html", map[string]any{"Title": state.DecisionRef(id), "Page": "decision", "Fleets": b.fleets(ctx), "Decision": d, "Task": t, "Token": b.csrf(ctx), "Pill": decisionPill[d.Status], "StripData": b.stripData(ctx), "Titles": b.refTitle(ctx), "Notice": notice})
}

func (b *Board) rejected(w http.ResponseWriter, r *http.Request, id int64, status int, msg, notice string) {
	w.Header().Set("X-Hand-Error", url.PathEscape(strings.Join(strings.Fields(msg), " ")))
	b.decisionPage(w, r, id, status, notice)
}

func (b *Board) asksForANote(ctx context.Context, id int64, choice string) bool {
	d, err := b.st.Decision(ctx, id)
	return err == nil && slices.ContainsFunc(options(d.Body()), func(o option) bool { return o.Other && o.Value == choice })
}

func (b *Board) answer(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "d")
	if err != nil {
		b.failErr(w, err)
		return
	}
	note := strings.TrimSpace(r.PostFormValue("answer"))
	choice := strings.TrimSpace(r.PostFormValue("choice"))
	answer := strings.TrimSpace(choice + "\n\n" + note)
	if answer == "" {
		const msg = "pick an option or write an answer"
		b.rejected(w, r, id, http.StatusBadRequest, msg, sentence(msg))
		return
	}
	if note == "" && b.asksForANote(r.Context(), id, choice) {
		const msg = "write your answer: that option asks for one"
		b.rejected(w, r, id, http.StatusBadRequest, msg, sentence(msg))
		return
	}
	if _, err := b.st.Answer(r.Context(), id, answer, "operator (board)"); err != nil {
		if d, derr := b.st.Decision(r.Context(), id); derr == nil && errors.Is(err, state.ErrConflict) && d.Status != state.DecisionOpen {
			msg := state.DecisionRef(id) + " is already " + d.Status
			notice := msg + "."
			if d.Answer != "" {
				msg += ": " + d.Answer
			}
			b.rejected(w, r, id, http.StatusConflict, msg, notice)
			return
		}
		b.failErr(w, err)
		return
	}
	receipt := state.DecisionRef(id) + " answered · no supervisor is running; it waits"
	if ref, running := b.supRef(r.Context()); running {
		receipt = state.DecisionRef(id) + " answered · " + ref + " reads it now"
	}
	b.done(w, r, b.o.Base+"/decision/"+state.DecisionRef(id), receipt)
}

func (b *Board) ack(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "r")
	if err != nil {
		b.failErr(w, err)
		return
	}
	rep, err := b.st.AckReport(r.Context(), id, "operator (board)")
	if err != nil {
		b.failErr(w, err)
		return
	}
	b.done(w, r, b.o.Base+"/task/"+state.TaskRef(rep.TaskID), state.ReportRef(id)+" marked read")
}

func hm(at string) string { return parse(at).UTC().Format("15:04") }
