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
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/state"
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

var statusRank = map[string]int{state.StatusActive: 0, state.StatusInbox: 1, state.StatusDone: 2, state.StatusAbandoned: 3}

type Board struct {
	st    *state.Store
	token string
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

func New(st *state.Store, token string) http.Handler {
	b := &Board{st: st, token: token, mux: http.NewServeMux()}
	b.mux.HandleFunc("GET /{$}", b.index)
	b.mux.HandleFunc("GET /task/{id}", b.task)
	return b
}

func (b *Board) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'")
	w.Header().Set("X-Frame-Options", "DENY")
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
	b.fail(w, status, err.Error())
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
	statuses := []string{state.StatusActive, state.StatusInbox}
	if all {
		statuses = nil
	}
	tasks, err := b.st.Tasks(ctx, statuses, 500)
	if err != nil {
		b.failErr(w, err)
		return
	}
	slices.SortStableFunc(tasks, func(x, y state.Task) int { return statusRank[x.Status] - statusRank[y.Status] })
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
	b.render(w, http.StatusOK, "index.html", map[string]any{
		"Title": "board", "Refresh": 5, "Cards": cards, "All": all,
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
	reports, err := b.st.Reports(ctx, state.ReportFilter{TaskID: id}, 50)
	if err != nil {
		b.failErr(w, err)
		return
	}
	slices.Reverse(reports)
	events, err := b.st.TaskEvents(ctx, id, 50)
	if err != nil {
		b.failErr(w, err)
		return
	}
	slices.Reverse(events)
	b.render(w, http.StatusOK, "task.html", map[string]any{
		"Title": state.TaskRef(id), "Refresh": 10, "Card": c, "Reports": reports, "Events": events,
	})
}
