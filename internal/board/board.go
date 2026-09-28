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
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

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
	"when":     when,
	"tint":     tint,
	"view": func(root map[string]any, w waiting, open bool) map[string]any {
		return map[string]any{"R": root, "W": w, "Open": open}
	},
}).ParseFS(files, "templates/*.html"))

const (
	cookieName = "hand_board"
	csp        = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'"
)

var prLink = regexp.MustCompile(`https://github\.com/[\w.-]+/[\w.-]+/pull/[0-9]+`)

var (
	taskPill     = map[string]string{state.StatusActive: "running", state.StatusInbox: "neutral", state.StatusDone: "passing", state.StatusAbandoned: "neutral"}
	decisionPill = map[string]string{state.DecisionOpen: "waiting", state.DecisionAnswered: "passing", state.DecisionWithdrawn: "neutral"}
)

const (
	maxCards     = 500
	historyLimit = 50
)

type Options struct {
	Controls   bool
	Control    func(ctx context.Context, args ...string) error
	Luvus      luvus.Client
	Transcript *transcript.Reader
	Home       string
	Base       string
	Tick       time.Duration
}

type Board struct {
	st    *state.Store
	token string
	o     Options
	mux   *http.ServeMux
}

type card struct {
	Task      state.Task
	Plan      *state.Plan
	Attempts  []state.Attempt
	Report    *state.Report
	Decisions []state.Decision
	PRs       []string
}

func New(st *state.Store, token string, o Options) http.Handler {
	if o.Tick <= 0 {
		o.Tick = time.Second
	}
	b := &Board{st: st, token: token, o: o, mux: http.NewServeMux()}
	b.mux.HandleFunc("GET /{$}", b.fleet)
	b.mux.HandleFunc("GET /supervisor/log", b.history)
	b.mux.HandleFunc("GET /events", b.events)
	b.mux.HandleFunc("POST /supervisor/start", b.start)
	b.mux.HandleFunc("POST /supervisor/resume", b.simple("resume"))
	b.mux.HandleFunc("POST /supervisor/stop", b.simple("stop"))
	b.mux.HandleFunc("POST /supervisor/interrupt", b.simple("interrupt"))
	b.mux.HandleFunc("POST /supervisor/keys", b.keys)
	b.mux.HandleFunc("POST /supervisor/send", b.send)
	b.mux.HandleFunc("GET /task/{id}", b.task)
	b.mux.HandleFunc("GET /decision/{id}", b.decision)
	b.mux.HandleFunc("POST /decision/{id}/answer", b.answer)
	b.mux.HandleFunc("POST /report/{id}/ack", b.ack)
	return b
}

func (b *Board) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("X-Frame-Options", "DENY")
	if strings.HasPrefix(r.URL.Path, "/static/") {
		ServeStatic(w, r)
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
			b.fail(w, http.StatusForbidden, "this board link is not valid")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: t, Path: b.o.Base + "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		q := r.URL.Query()
		q.Del("token")
		next := b.o.Base + r.URL.Path
		if len(q) > 0 {
			next += "?" + q.Encode()
		}
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	if c, err := r.Cookie(cookieName); err != nil || !b.valid(c.Value) {
		b.fail(w, http.StatusForbidden, "open this fleet with `hand open` from inside it")
		return
	}
	if r.Method == http.MethodPost && !b.valid(r.PostFormValue("csrf")) {
		b.fail(w, http.StatusForbidden, "this form is stale; reload the page and try again")
		return
	}
	b.mux.ServeHTTP(w, r)
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
	data["Base"] = b.o.Base
	data["Fleet"] = "hand"
	if f, err := b.st.Fleet(context.Background()); err == nil {
		data["Fleet"], data["FleetID"] = f.Name, f.ID
	}
	renderPage(w, status, name, data)
}

func renderPage(w http.ResponseWriter, status int, name string, data map[string]any) {
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
	w.Header().Set("X-Hand-Error", strings.Join(strings.Fields(msg), " "))
	b.render(w, status, "error.html", map[string]any{"Title": strconv.Itoa(status), "Status": status, "Message": msg})
}

func (b *Board) failErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, state.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, state.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, state.ErrInvalid):
		status = http.StatusBadRequest
	}
	b.fail(w, status, Scrub(err.Error()))
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

func PRLinks(text string) []string { return prLink.FindAllString(text, -1) }

var absPath = regexp.MustCompile(`(^|[\s"'(=])/[^\s"'():,]+`)

func Scrub(msg string) string {
	return absPath.ReplaceAllStringFunc(msg, func(m string) string {
		i := strings.IndexByte(m, '/')
		return m[:i] + filepath.Base(m[i:])
	})
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
	for i := len(reports) - 1; i >= 0; i-- {
		for _, link := range PRLinks(reports[i].Body) {
			if !slices.Contains(c.PRs, link) {
				c.PRs = append(c.PRs, link)
			}
		}
	}
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
	f, _ := data["facts"].(facts)
	prs, err := b.prLinks(ctx, tasks)
	if err != nil {
		return err
	}
	f.prs = prs
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
	data["Checks"], data["All"], data["Hidden"] = checks, all, total-len(checks)
	return nil
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
		row := check{Task: t, Attempt: &a, State: "idle"}
		switch {
		case a.Live():
			row.State = "running"
		case facts{done: done}.failing(a):
			row.State = "failing"
		case a.Status == state.AttemptExited:
			row.State = "passing"
		}
		rows = append(rows, row)
	}
	b.render(w, http.StatusOK, "task.html", map[string]any{
		"Title": state.TaskRef(id), "Card": c, "Token": b.token, "Pill": taskPill[t.Status], "Attempts": rows,
		"Unread": unread, "Reports": reports, "MoreReports": moreReports, "Events": events, "MoreEvents": moreEvents,
	})
}

func (b *Board) decision(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "d")
	if err != nil {
		b.failErr(w, err)
		return
	}
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
	b.render(w, http.StatusOK, "decision.html", map[string]any{"Title": state.DecisionRef(id), "Decision": d, "Task": t, "Token": b.token, "Pill": decisionPill[d.Status]})
}

func (b *Board) answer(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "d")
	if err != nil {
		b.failErr(w, err)
		return
	}
	if _, err := b.st.Answer(r.Context(), id, r.PostFormValue("answer"), "operator (board)"); err != nil {
		b.failErr(w, err)
		return
	}
	http.Redirect(w, r, b.o.Base+"/decision/"+state.DecisionRef(id), http.StatusSeeOther)
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
	http.Redirect(w, r, b.o.Base+"/task/"+state.TaskRef(rep.TaskID), http.StatusSeeOther)
}
