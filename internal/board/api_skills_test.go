package board_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

type skillsSpy struct {
	mu      sync.Mutex
	queries []board.SkillsQuery
	actions []board.SkillsAction
	ctxs    []context.Context
	read    func(context.Context, board.SkillsQuery) (board.APISkills, error)
	run     func(context.Context, board.SkillsAction) (board.APISkillsRun, error)
}

func (s *skillsSpy) install(fx *fixture) {
	fx.options.Skills = func(ctx context.Context, q board.SkillsQuery) (board.APISkills, error) {
		s.mu.Lock()
		s.queries = append(s.queries, q)
		s.mu.Unlock()
		if s.read != nil {
			return s.read(ctx, q)
		}
		return board.APISkills{Scopes: []board.APISkillScope{{Scope: "fleet", Skills: []board.APISkill{{Name: "triage", Managed: true, State: "current"}}}}}, nil
	}
	fx.options.SkillsRun = func(ctx context.Context, a board.SkillsAction) (board.APISkillsRun, error) {
		s.mu.Lock()
		s.actions = append(s.actions, a)
		s.ctxs = append(s.ctxs, ctx)
		s.mu.Unlock()
		if s.run != nil {
			return s.run(ctx, a)
		}
		return board.APISkillsRun{Scope: a.Scope, Project: a.Project, Results: []board.APISkillResult{{Name: "triage", State: "updated"}, {Name: "other", State: "current"}, {Name: "third", State: "updated"}}}, nil
	}
}

func skillsFixture(t *testing.T) (*fixture, *skillsSpy, http.Handler) {
	t.Helper()
	fx := apiFixture(t, func() time.Time { return frozen })
	spy := &skillsSpy{}
	spy.install(fx)
	return fx, spy, fx.handler()
}

func TestAPISkillsReadPassesTheScopeAndAnswersTheEnvelope(t *testing.T) {
	_, spy, h := skillsFixture(t)
	env, data := apiInto[board.APISkills](t, h, "/api/v1/skills")
	if len(data.Scopes) != 1 || data.Scopes[0].Skills[0].Name != "triage" || env.Fleet.Name != "golden" || env.Cursor == "" {
		t.Fatalf("skills = %+v, %+v", env, data)
	}
	apiInto[board.APISkills](t, h, "/api/v1/skills?scope=project&project=app")
	apiInto[board.APISkills](t, h, "/api/v1/skills?scope=user")
	want := []board.SkillsQuery{{}, {Scope: "project", Project: "app"}, {Scope: "user"}}
	if fmt.Sprint(spy.queries) != fmt.Sprint(want) {
		t.Fatalf("queries = %+v, want %+v", spy.queries, want)
	}
	rec := request(h, "GET", "/api/v1/skills?since="+url.QueryEscape(env.Cursor), nil, true)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("an unchanged read with its own cursor = %d", rec.Code)
	}
}

func TestAPISkillsReadRefusals(t *testing.T) {
	_, spy, h := skillsFixture(t)
	for _, q := range []string{"scope=team", "scope=fleet&project=app", "project=app", "scope=project&project=" + url.QueryEscape("a\nb"), "scope=project&project=" + strings.Repeat("x", 513)} {
		testAPIError(t, request(h, "GET", "/api/v1/skills?"+q, nil, true), http.StatusBadRequest, "bad_request")
	}
	testAPIError(t, request(h, "GET", "/api/v1/skills", nil, false), http.StatusUnauthorized, "unauthorized")
	testAPIError(t, request(h, "GET", "/api/v1/skills?since=nope", nil, true), http.StatusBadRequest, "bad_cursor")
	if len(spy.queries) != 0 {
		t.Fatalf("a refused read reached the hook: %+v", spy.queries)
	}
}

func TestAPISkillsWithoutTheHooksAreOff(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	h := fx.handler()
	testAPIError(t, request(h, "GET", "/api/v1/skills", nil, true), http.StatusForbidden, "forbidden")
	testAPIError(t, apiPost(t, h, "/api/v1/skills/sync", map[string]string{"scope": "fleet"}, token, true), http.StatusForbidden, "forbidden")
	testAPIError(t, apiPost(t, h, "/api/v1/skills/add", map[string]string{"scope": "fleet", "source": "x"}, token, true), http.StatusForbidden, "forbidden")
	testAPIError(t, apiPost(t, h, "/api/v1/skills/remove", map[string]string{"scope": "fleet", "name": "x"}, token, true), http.StatusForbidden, "forbidden")
}

func TestAPISkillsActionsPassWhatTheBodyNamesAndAnswerWithAReceipt(t *testing.T) {
	_, spy, h := skillsFixture(t)
	env, out := apiBody[board.APISkillsRun](t, apiPost(t, h, "/api/v1/skills/sync", map[string]string{"scope": "project", "project": "app", "name": "triage", "yes": "triage@abc"}, token, true))
	if env.Receipt != "Skills sync: 1 current, 2 updated" || out.Scope != "project" || out.Project != "app" || len(out.Results) != 3 {
		t.Fatalf("sync = %+v, %+v", env, out)
	}
	apiBody[board.APISkillsRun](t, apiPost(t, h, "/api/v1/skills/update", map[string]string{"scope": "fleet"}, token, true))
	apiBody[board.APISkillsRun](t, apiPost(t, h, "/api/v1/skills/propose", map[string]string{"scope": "user", "name": "triage"}, token, true))
	want := []board.SkillsAction{
		{Verb: "sync", Scope: "project", Project: "app", Name: "triage", Yes: "triage@abc"},
		{Verb: "update", Scope: "fleet"},
		{Verb: "propose", Scope: "user", Name: "triage"},
	}
	if fmt.Sprint(spy.actions) != fmt.Sprint(want) {
		t.Fatalf("actions = %+v, want %+v", spy.actions, want)
	}
}

func TestAPISkillsActionRefusals(t *testing.T) {
	_, spy, h := skillsFixture(t)
	for _, path := range []string{"sync", "update", "propose", "add", "remove"} {
		testAPIError(t, apiPost(t, h, "/api/v1/skills/"+path, map[string]string{"scope": "fleet", "name": "x"}, "", true), http.StatusForbidden, "csrf_required")
		testAPIError(t, apiPost(t, h, "/api/v1/skills/"+path, map[string]string{"scope": "fleet", "name": "x"}, "wrong", true), http.StatusForbidden, "csrf_required")
		testAPIError(t, apiPost(t, h, "/api/v1/skills/"+path, map[string]string{"scope": "fleet", "name": "x"}, token, false), http.StatusUnauthorized, "unauthorized")
		testAPIError(t, apiPost(t, h, "/api/v1/skills/"+path, nil, token, true), http.StatusBadRequest, "bad_request")
		testAPIError(t, apiPost(t, h, "/api/v1/skills/"+path, map[string]string{"scope": "all", "name": "x"}, token, true), http.StatusBadRequest, "bad_request")
		testAPIError(t, apiPost(t, h, "/api/v1/skills/"+path, map[string]string{"scope": "project", "name": "x"}, token, true), http.StatusBadRequest, "bad_request")
		testAPIError(t, apiPost(t, h, "/api/v1/skills/"+path, map[string]string{"scope": "fleet", "project": "app", "name": "x"}, token, true), http.StatusBadRequest, "bad_request")
		testAPIError(t, apiPost(t, h, "/api/v1/skills/"+path, map[string]string{"scope": "fleet", "name": "a\x00b"}, token, true), http.StatusBadRequest, "bad_request")
		testAPIError(t, apiPostRaw(t, h, "/api/v1/skills/"+path, `{"scope":"fleet","name":"x"} {}`, token, true), http.StatusBadRequest, "bad_request")
		testAPIError(t, apiPostRaw(t, h, "/api/v1/skills/"+path, `{"scope":`+strings.Repeat(" ", 70<<10)+`"fleet"}`, token, true), http.StatusBadRequest, "bad_request")
		testAPIError(t, request(h, "GET", "/api/v1/skills/"+path, nil, true), http.StatusNotFound, "not_found")
	}
	testAPIError(t, apiPost(t, h, "/api/v1/skills/propose", map[string]string{"scope": "fleet"}, token, true), http.StatusBadRequest, "bad_request")
	testAPIError(t, apiPost(t, h, "/api/v1/skills/update", map[string]string{"scope": "fleet", "yes": "x@y"}, token, true), http.StatusBadRequest, "bad_request")
	testAPIError(t, apiPost(t, h, "/api/v1/skills/propose", map[string]string{"scope": "fleet", "name": "x", "yes": "x@y"}, token, true), http.StatusBadRequest, "bad_request")
	for verb, bodies := range map[string][]map[string]string{
		"remove": {
			{"scope": "fleet"},
			{"scope": "fleet", "name": "  "},
			{"scope": "fleet", "name": "x", "yes": "x@y"},
			{"scope": "fleet", "name": "x", "source": "x"},
			{"scope": "fleet", "name": "x", "ref": "main"},
			{"scope": "fleet", "name": strings.Repeat("x", 513)},
		},
		"add": {
			{"scope": "fleet"},
			{"scope": "fleet", "source": " "},
			{"scope": "fleet", "source": "x", "name": "x"},
			{"scope": "fleet", "source": "x", "yes": "x@y"},
			{"scope": "fleet", "source": strings.Repeat("x", 513)},
			{"scope": "fleet", "source": "x", "ref": strings.Repeat("x", 513)},
			{"scope": "fleet", "source": "x", "ref": "a\nb"},
			{"scope": "fleet", "source": "a\x00b"},
			{"scope": "project", "source": "x"},
			{"scope": "user", "project": "app", "source": "x"},
		},
		"sync":    {{"scope": "fleet", "source": "x"}, {"scope": "fleet", "ref": "main"}},
		"update":  {{"scope": "fleet", "source": "x"}},
		"propose": {{"scope": "fleet", "name": "x", "ref": "main"}},
	} {
		for _, body := range bodies {
			testAPIError(t, apiPost(t, h, "/api/v1/skills/"+verb, body, token, true), http.StatusBadRequest, "bad_request")
		}
	}
	if len(spy.actions) != 0 {
		t.Fatalf("a refused action reached the hook: %+v", spy.actions)
	}
}

func TestAPISkillsAddAndRemovePassWhatTheBodyNamesAndAnswerWithAReceipt(t *testing.T) {
	_, spy, h := skillsFixture(t)
	spy.run = func(_ context.Context, a board.SkillsAction) (board.APISkillsRun, error) {
		st := map[string]string{"add": "added", "remove": "removed"}[a.Verb]
		return board.APISkillsRun{Scope: a.Scope, Project: a.Project, Results: []board.APISkillResult{{Name: "triage", State: st}}}, nil
	}
	env, out := apiBody[board.APISkillsRun](t, apiPost(t, h, "/api/v1/skills/add", map[string]string{"scope": "project", "project": "app", "source": " triage ", "ref": "v1"}, token, true))
	if env.Receipt != "Skills add: 1 added" || out.Results[0].State != "added" {
		t.Fatalf("add = %+v, %+v", env, out)
	}
	env, _ = apiBody[board.APISkillsRun](t, apiPost(t, h, "/api/v1/skills/remove", map[string]string{"scope": "user", "name": "triage"}, token, true))
	if env.Receipt != "Skills remove: 1 removed" {
		t.Fatalf("remove = %+v", env)
	}
	want := []board.SkillsAction{
		{Verb: "add", Scope: "project", Project: "app", Source: "triage", Ref: "v1"},
		{Verb: "remove", Scope: "user", Name: "triage"},
	}
	if fmt.Sprint(spy.actions) != fmt.Sprint(want) {
		t.Fatalf("actions = %+v, want %+v", spy.actions, want)
	}
}

func TestAPISkillsErrorsKeepTheirKind(t *testing.T) {
	_, spy, h := skillsFixture(t)
	for kind, status := range map[error]int{state.ErrInvalid: 400, state.ErrNotFound: 404, state.ErrConflict: 409} {
		spy.run = func(context.Context, board.SkillsAction) (board.APISkillsRun, error) {
			return board.APISkillsRun{}, fmt.Errorf("%w: no project in /home/pat/secret/app", kind)
		}
		spy.read = func(context.Context, board.SkillsQuery) (board.APISkills, error) {
			return board.APISkills{}, fmt.Errorf("%w: no project in /home/pat/secret/app", kind)
		}
		for _, rec := range []*struct{ code int }{{apiPost(t, h, "/api/v1/skills/sync", map[string]string{"scope": "fleet"}, token, true).Code}, {request(h, "GET", "/api/v1/skills", nil, true).Code}} {
			if rec.code != status {
				t.Fatalf("%v answered %d, want %d", kind, rec.code, status)
			}
		}
		rec := apiPost(t, h, "/api/v1/skills/sync", map[string]string{"scope": "fleet"}, token, true)
		if strings.Contains(rec.Body.String(), "/home/pat") {
			t.Fatalf("the message carries a path: %s", rec.Body.String())
		}
	}
	spy.run = func(context.Context, board.SkillsAction) (board.APISkillsRun, error) {
		return board.APISkillsRun{}, errors.New("boom")
	}
	testAPIError(t, apiPost(t, h, "/api/v1/skills/sync", map[string]string{"scope": "fleet"}, token, true), http.StatusInternalServerError, "internal")
}

func TestAPISkillsRunOneRequestAtATime(t *testing.T) {
	_, spy, h := skillsFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	spy.read = func(ctx context.Context, q board.SkillsQuery) (board.APISkills, error) {
		close(entered)
		<-release
		return board.APISkills{Scopes: []board.APISkillScope{}}, nil
	}
	done := make(chan int)
	go func() { done <- request(h, "GET", "/api/v1/skills", nil, true).Code }()
	<-entered
	e := testAPIError(t, request(h, "GET", "/api/v1/skills", nil, true), http.StatusConflict, "conflict")
	if !strings.Contains(e.Message, "another skills request") {
		t.Fatalf("message = %q", e.Message)
	}
	testAPIError(t, apiPost(t, h, "/api/v1/skills/sync", map[string]string{"scope": "fleet"}, token, true), http.StatusConflict, "conflict")
	testAPIError(t, apiPost(t, h, "/api/v1/skills/add", map[string]string{"scope": "fleet", "source": "triage"}, token, true), http.StatusConflict, "conflict")
	testAPIError(t, apiPost(t, h, "/api/v1/skills/remove", map[string]string{"scope": "fleet", "name": "triage"}, token, true), http.StatusConflict, "conflict")
	if len(spy.actions) != 0 {
		t.Fatalf("a refused action ran: %+v", spy.actions)
	}
	close(release)
	if code := <-done; code != http.StatusOK {
		t.Fatalf("the first read = %d", code)
	}
	spy.read = nil
	apiInto[board.APISkills](t, h, "/api/v1/skills")
}

func TestAPISkillsActionOutlivesTheClientAndIsBounded(t *testing.T) {
	_, spy, h := skillsFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	spy.run = func(c context.Context, a board.SkillsAction) (board.APISkillsRun, error) {
		cancel()
		if c.Err() != nil {
			return board.APISkillsRun{}, errors.New("the client's leaving stopped the action")
		}
		return board.APISkillsRun{Results: []board.APISkillResult{}}, nil
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", "/api/v1/skills/update", strings.NewReader(`{"scope":"fleet"}`))
	req.Header.Set("X-Hand-CSRF", token)
	req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d: %s", rec.Code, rec.Body.String())
	}
	dl, ok := spy.ctxs[0].Deadline()
	if !ok || time.Until(dl) < 4*time.Minute || time.Until(dl) > 5*time.Minute+time.Second {
		t.Fatalf("deadline = %v, %v", dl, ok)
	}
}

func TestAPairedPhoneReadsSkillsAndSyncsOnlyWithoutAYes(t *testing.T) {
	r := newPeerRig(t, "alpha")
	f := r.fleets[0]
	c := r.as(peerA)
	companion := r.mustPair(c, f)
	if rep := r.apiGet(c, f, "skills", nil, companion); rep.Code != http.StatusOK {
		t.Fatalf("a phone's read = %d:\n%s", rep.Code, rep.Body)
	}
	csrf := r.csrfOf(c, f, companion)
	for _, refused := range []struct{ verb, body, sentence string }{
		{"update", `{"scope":"fleet","name":"triage"}`, "a paired Companion cannot update, propose, add or remove skills; use the host's Board"},
		{"propose", `{"scope":"fleet","name":"triage"}`, "a paired Companion cannot update, propose, add or remove skills; use the host's Board"},
		{"add", `{"scope":"fleet","source":"triage"}`, "a paired Companion cannot update, propose, add or remove skills; use the host's Board"},
		{"remove", `{"scope":"fleet","name":"triage"}`, "a paired Companion cannot update, propose, add or remove skills; use the host's Board"},
		{"sync", `{"scope":"fleet","yes":"triage@abc"}`, "a paired Companion cannot apply a third-party update"},
		{"sync", `{"scope":"user"}`, "a paired Companion cannot sync the user scope; use the host's Board"},
	} {
		rep := r.post(c, f, "skills/"+refused.verb, refused.body, csrf, companion)
		if rep.Code != http.StatusForbidden || !strings.Contains(rep.Body, `"code":"forbidden"`) || !strings.Contains(rep.Body, refused.sentence) {
			t.Fatalf("a phone's %s %s = %d:\n%s", refused.verb, refused.body, rep.Code, rep.Body)
		}
	}
	if n := r.skillRuns.Load(); n != 0 {
		t.Fatalf("a refused phone ran the hook %d times", n)
	}
	if rep := r.post(c, f, "skills/sync", `{"scope":"fleet"}`, csrf, companion); rep.Code != http.StatusOK {
		t.Fatalf("a phone's sync = %d:\n%s", rep.Code, rep.Body)
	}
	if n := r.skillRuns.Load(); n != 1 {
		t.Fatalf("the hook ran %d times, want 1", n)
	}
	if rep := r.post(c, f, "skills/sync", `{"scope":"fleet"}`, csrf); rep.Code != http.StatusUnauthorized && rep.Code != http.StatusForbidden || r.skillRuns.Load() != 1 {
		t.Fatalf("a phone without its cookie = %d", rep.Code)
	}
	other := r.as(peerB)
	if rep := r.post(other, f, "skills/sync", `{"scope":"fleet"}`, csrf, companion); rep.Code == http.StatusOK || r.skillRuns.Load() != 1 {
		t.Fatalf("another endpoint holding the cookie = %d, hook runs %d", rep.Code, r.skillRuns.Load())
	}
}

func TestAPISkillsStopWhenTheBoardShutsDown(t *testing.T) {
	fx := apiFixture(t, func() time.Time { return frozen })
	spy := &skillsSpy{}
	spy.install(fx)
	life, stop := context.WithCancel(context.Background())
	fx.options.Lifetime = life
	h := fx.handler()
	entered := make(chan struct{}, 2)
	wait := func(ctx context.Context) error {
		entered <- struct{}{}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Second):
			return errors.New("the action was not stopped")
		}
	}
	spy.run = func(ctx context.Context, a board.SkillsAction) (board.APISkillsRun, error) {
		return board.APISkillsRun{}, wait(ctx)
	}
	spy.read = func(ctx context.Context, q board.SkillsQuery) (board.APISkills, error) {
		return board.APISkills{}, wait(ctx)
	}
	codes := make(chan int, 2)
	go func() {
		codes <- apiPost(t, h, "/api/v1/skills/sync", map[string]string{"scope": "fleet"}, token, true).Code
	}()
	<-entered
	stop()
	select {
	case code := <-codes:
		if code == http.StatusOK {
			t.Fatalf("a stopped action answered %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the action outlived the board")
	}
	go func() { codes <- request(h, "GET", "/api/v1/skills", nil, true).Code }()
	select {
	case <-codes:
	case <-time.After(10 * time.Second):
		t.Fatal("the read outlived the board")
	}
}
