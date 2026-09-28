package board

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/luvus"
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
}).ParseFS(files, "templates/*.html"))

const cookieName = "hand_board"

var prLink = regexp.MustCompile(`https://github\.com/[\w.-]+/[\w.-]+/pull/[0-9]+`)

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
	Token     string
}

func New(st *state.Store, token string, o Options) http.Handler {
	b := &Board{st: st, token: token, o: o, mux: http.NewServeMux()}
	b.mux.HandleFunc("GET /{$}", b.shell)
	b.mux.HandleFunc("GET /cards", b.index)
	b.mux.HandleFunc("GET /supervisor/panel", b.panel)
	b.mux.HandleFunc("GET /supervisor/log", b.log)
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
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-src 'self'; frame-ancestors 'self'")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	if t := r.URL.Query().Get("token"); t != "" {
		if !b.valid(t) {
			b.fail(w, http.StatusForbidden, "this board link is not valid")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: t, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		q := r.URL.Query()
		q.Del("token")
		next := r.URL.Path
		if len(q) > 0 {
			next += "?" + q.Encode()
		}
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	if c, err := r.Cookie(cookieName); err != nil || !b.valid(c.Value) {
		b.fail(w, http.StatusForbidden, "open the board with the link `hand board` printed")
		return
	}
	if r.Method == http.MethodPost && !b.valid(r.PostFormValue("csrf")) {
		b.fail(w, http.StatusForbidden, "this form is stale; reload the page and try again")
		return
	}
	b.mux.ServeHTTP(w, r)
}

func (b *Board) valid(t string) bool {
	return subtle.ConstantTimeCompare([]byte(t), []byte(b.token)) == 1
}

func (b *Board) render(w http.ResponseWriter, status int, name string, data map[string]any) {
	data["Fleet"] = "hand"
	if f, err := b.st.Fleet(context.Background()); err == nil {
		data["Fleet"] = f.Name
	}
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
	c := card{Task: t, Token: b.token}
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
		for _, link := range prLink.FindAllString(reports[i].Body, -1) {
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

func (b *Board) index(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	all := r.URL.Query().Get("all") == "1"
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
			b.failErr(w, err)
			return
		}
		tasks = append(tasks, part...)
	}
	cards := make([]card, 0, len(tasks))
	for _, t := range tasks {
		c, err := b.card(ctx, t, 3)
		if err != nil {
			b.failErr(w, err)
			return
		}
		cards = append(cards, c)
	}
	counts, err := b.st.CountTasks(ctx)
	if err != nil {
		b.failErr(w, err)
		return
	}
	open, err := b.st.OpenDecisionCount(ctx, 0)
	if err != nil {
		b.failErr(w, err)
		return
	}
	unacked, err := b.st.UnackedReportCount(ctx)
	if err != nil {
		b.failErr(w, err)
		return
	}
	total := counts[state.StatusActive] + counts[state.StatusInbox]
	if all {
		total += counts[state.StatusDone] + counts[state.StatusAbandoned]
	}
	b.render(w, http.StatusOK, "cards.html", map[string]any{
		"Title": "tasks", "Refresh": cardsRefresh, "BaseTop": true, "Cards": cards, "All": all, "Hidden": total - len(cards),
		"Active": counts[state.StatusActive], "Inbox": counts[state.StatusInbox], "Open": open, "Unacked": unacked,
	})
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
	b.render(w, http.StatusOK, "task.html", map[string]any{
		"Title": state.TaskRef(id), "Refresh": 10, "Card": c, "Token": b.token,
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
	b.render(w, http.StatusOK, "decision.html", map[string]any{"Title": state.DecisionRef(id), "Decision": d, "Task": t, "Token": b.token})
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
	http.Redirect(w, r, "/decision/"+state.DecisionRef(id), http.StatusSeeOther)
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
	http.Redirect(w, r, "/task/"+state.TaskRef(rep.TaskID), http.StatusSeeOther)
}
