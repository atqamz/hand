package board_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

var httpsOnly = map[string]string{"X-Forwarded-Proto": "https"}

type phoneSkills struct {
	*companionRig
	spy    *skillsSpy
	cookie *http.Cookie
	csrf   string
}

func newPhoneSkills(t *testing.T) *phoneSkills { return newPhoneSkillsOn(t, nil) }

func newPhoneSkillsOn(t *testing.T, swap func(*fixture)) *phoneSkills {
	t.Helper()
	p := newCompanionRig(t)
	if swap != nil {
		swap(p.fx)
	}
	spy := &skillsSpy{}
	spy.install(p.fx)
	p.h = p.fx.handler()
	cookie := p.pair()
	rec := p.do("GET", "/api/v1/session", nil, cookie, httpsOnly)
	var env apiReply[board.APISession]
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &env) != nil || env.Data.CSRF == "" {
		t.Fatalf("session = %d:\n%s", rec.Code, rec.Body.String())
	}
	return &phoneSkills{p, spy, cookie, env.Data.CSRF}
}

func (p *phoneSkills) post(verb, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	p.t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/skills/"+verb, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-Hand-CSRF", csrf)
	}
	req.AddCookie(cookie)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	p.h.ServeHTTP(rec, req)
	return rec
}

func (p *phoneSkills) phone(verb, body string) *httptest.ResponseRecorder {
	p.t.Helper()
	return p.post(verb, body, p.cookie, p.csrf)
}

func (p *phoneSkills) hostPost(verb, body string) *httptest.ResponseRecorder {
	p.t.Helper()
	return p.post(verb, body, host, token)
}

func (p *phoneSkills) ran() int {
	p.spy.mu.Lock()
	defer p.spy.mu.Unlock()
	return len(p.spy.actions)
}

func (p *phoneSkills) refuses(rec *httptest.ResponseRecorder, status int, code, sentence string) {
	p.t.Helper()
	e := testAPIError(p.t, rec, status, code)
	if sentence != "" && e.Message != sentence {
		p.t.Fatalf("message = %q, want %q", e.Message, sentence)
	}
}

const (
	phoneNoUpdate = "a paired Companion cannot update, propose, add or remove skills; use the host's Board"
	phoneNoYes    = "a paired Companion cannot apply a third-party update; read the diff, then tell the supervisor or use the host's Board"
	phoneNoUser   = "a paired Companion cannot sync the user scope; use the host's Board"
)

func TestAPhoneSyncsTheFleetScopeWithoutAYes(t *testing.T) {
	p := newPhoneSkills(t)
	rec := p.phone("sync", `{"scope":"fleet","name":"triage"}`)
	var env struct {
		Receipt string
		Data    board.APISkillsRun
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &env) != nil || env.Receipt != "Skills sync: 1 current, 2 updated" || env.Data.Scope != "fleet" {
		t.Fatalf("a phone's sync = %d:\n%s", rec.Code, rec.Body.String())
	}
	if len(p.spy.actions) != 1 || p.spy.actions[0] != (board.SkillsAction{Verb: "sync", Scope: "fleet", Name: "triage", Phone: true}) {
		t.Fatalf("hook calls = %+v", p.spy.actions)
	}
}

func TestAPhoneSyncsAProjectScope(t *testing.T) {
	p := newPhoneSkills(t)
	if rec := p.phone("sync", `{"scope":"project","project":"app"}`); rec.Code != http.StatusOK {
		t.Fatalf("a phone's project sync = %d:\n%s", rec.Code, rec.Body.String())
	}
	if len(p.spy.actions) != 1 || p.spy.actions[0] != (board.SkillsAction{Verb: "sync", Scope: "project", Project: "app", Phone: true}) {
		t.Fatalf("hook calls = %+v", p.spy.actions)
	}
}

func TestAPhoneCannotSyncTheUserScope(t *testing.T) {
	p := newPhoneSkills(t)
	p.refuses(p.phone("sync", `{"scope":"user"}`), http.StatusForbidden, "forbidden", phoneNoUser)
	if p.ran() != 0 {
		t.Fatal("the hook ran")
	}
	if rec := p.hostPost("sync", `{"scope":"fleet"}`); rec.Code != http.StatusOK {
		t.Fatalf("a refused phone kept the lock: the host's sync = %d", rec.Code)
	}
}

func TestAPhoneCannotSyncWithAYes(t *testing.T) {
	p := newPhoneSkills(t)
	for _, scope := range []string{`"fleet"`, `"project","project":"app"`} {
		p.refuses(p.phone("sync", `{"scope":`+scope+`,"yes":"triage@abc"}`), http.StatusForbidden, "forbidden", phoneNoYes)
	}
	p.refuses(p.phone("sync", `{"scope":"user","yes":"triage@abc"}`), http.StatusForbidden, "forbidden", "")
	if p.ran() != 0 {
		t.Fatal("the hook ran")
	}
}

func TestAPhoneCannotUpdateProposeAddOrRemove(t *testing.T) {
	p := newPhoneSkills(t)
	for _, c := range []struct{ verb, body string }{
		{"update", `{"scope":"fleet"}`},
		{"update", `{"scope":"project","project":"app","name":"triage"}`},
		{"propose", `{"scope":"fleet","name":"triage"}`},
		{"add", `{"scope":"fleet","source":"triage"}`},
		{"add", `{"scope":"project","project":"app","source":"triage","ref":"v1"}`},
		{"remove", `{"scope":"fleet","name":"triage"}`},
		{"remove", `{"scope":"user","name":"triage"}`},
		{"update", ``},
		{"propose", `{"scope":"nowhere"}`},
		{"add", `{"scope":"nowhere"}`},
		{"remove", ``},
	} {
		p.refuses(p.phone(c.verb, c.body), http.StatusForbidden, "forbidden", phoneNoUpdate)
	}
	if p.ran() != 0 || len(p.events("skills.added", "skills.removed")) != 0 {
		t.Fatal("the hook ran or an event was written")
	}
}

func TestAPhoneNeedsTheCSRFTokenForEverySkillsVerb(t *testing.T) {
	p := newPhoneSkills(t)
	for _, verb := range []string{"sync", "update", "propose"} {
		p.refuses(p.post(verb, `{"scope":"fleet","name":"x"}`, p.cookie, ""), http.StatusForbidden, "csrf_required", "")
		p.refuses(p.post(verb, `{"scope":"fleet","name":"x"}`, p.cookie, "wrong"), http.StatusForbidden, "csrf_required", "")
	}
	if p.ran() != 0 {
		t.Fatal("the hook ran")
	}
}

func TestAPhonesMalformedSyncAnswers400Before403(t *testing.T) {
	p := newPhoneSkills(t)
	for _, body := range []string{
		``, `{`, `{"scope":"all"}`, `{"scope":"project"}`, `{"scope":"user","project":"app"}`,
		`{"scope":"user","yes":"a\u0000b"}`, `{"scope":"fleet","name":"a\u0000b","yes":"x@y"}`, `{"scope":"fleet"} {}`,
	} {
		p.refuses(p.phone("sync", body), http.StatusBadRequest, "bad_request", "")
	}
	if p.ran() != 0 {
		t.Fatal("the hook ran")
	}
}

func TestARevokedPhoneAndAnotherDevicesCookieAreRefusedBeforeTheSkillsHandler(t *testing.T) {
	p := newPhoneSkills(t)
	other := p.pair()
	rec := p.do("POST", "/connect/revoke", map[string][]string{"csrf": {token}, "id": {p.deviceID(p.cookie)}}, host, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke = %d", rec.Code)
	}
	if rec := p.phone("sync", `{"scope":"fleet"}`); rec.Code != http.StatusForbidden && rec.Code != http.StatusUnauthorized {
		t.Fatalf("a revoked phone's sync = %d", rec.Code)
	}
	if rec := p.post("sync", `{"scope":"fleet"}`, &http.Cookie{Name: other.Name, Value: "99." + strings.SplitN(other.Value, ".", 2)[1]}, p.csrf); rec.Code != http.StatusForbidden && rec.Code != http.StatusUnauthorized {
		t.Fatalf("a forged device number's sync = %d", rec.Code)
	}
	if p.ran() != 0 || len(p.events("skills.synced")) != 0 {
		t.Fatalf("a refused phone ran the hook %d times or wrote %q", p.ran(), p.events("skills.synced"))
	}
}

func TestTheHostKeepsEverySkillsVerbScopeAndYes(t *testing.T) {
	p := newPhoneSkills(t)
	for _, c := range []struct{ verb, body string }{
		{"sync", `{"scope":"user"}`},
		{"sync", `{"scope":"fleet","name":"triage","yes":"triage@abc"}`},
		{"sync", `{"scope":"project","project":"app","yes":"triage@abc"}`},
		{"update", `{"scope":"fleet"}`},
		{"propose", `{"scope":"user","name":"triage"}`},
	} {
		if rec := p.hostPost(c.verb, c.body); rec.Code != http.StatusOK {
			t.Fatalf("the host's %s %s = %d:\n%s", c.verb, c.body, rec.Code, rec.Body.String())
		}
	}
	if p.ran() != 5 {
		t.Fatalf("hook calls = %d", p.ran())
	}
	for _, a := range p.spy.actions {
		if a.Phone {
			t.Fatalf("the host's action carries Phone: %+v", a)
		}
	}
}

func TestAPhoneSyncAndARefusedPhoneRespectTheOneAtATimeLock(t *testing.T) {
	p := newPhoneSkills(t)
	entered, release := make(chan struct{}), make(chan struct{})
	p.spy.read = func(context.Context, board.SkillsQuery) (board.APISkills, error) {
		close(entered)
		<-release
		return board.APISkills{Scopes: []board.APISkillScope{}}, nil
	}
	done := make(chan int)
	go func() { done <- p.do("GET", "/api/v1/skills", nil, host, nil).Code }()
	<-entered
	p.refuses(p.phone("sync", `{"scope":"fleet"}`), http.StatusConflict, "conflict", "")
	p.refuses(p.phone("sync", `{"scope":"user"}`), http.StatusForbidden, "forbidden", phoneNoUser)
	p.refuses(p.phone("update", `{"scope":"fleet"}`), http.StatusForbidden, "forbidden", phoneNoUpdate)
	p.refuses(p.phone("sync", `{"scope":"fleet","yes":"x@y"}`), http.StatusForbidden, "forbidden", phoneNoYes)
	close(release)
	if code := <-done; code != http.StatusOK {
		t.Fatalf("the read = %d", code)
	}
	if p.ran() != 0 || len(p.events("skills.synced")) != 0 {
		t.Fatal("a refused request ran or wrote")
	}
	if rec := p.hostPost("sync", `{"scope":"fleet"}`); rec.Code != http.StatusOK {
		t.Fatalf("the host after the refusals = %d", rec.Code)
	}
}

func TestSyncRecordsWhoAskedAndNothingElse(t *testing.T) {
	p := newPhoneSkills(t)
	p.spy.run = func(_ context.Context, a board.SkillsAction) (board.APISkillsRun, error) {
		return board.APISkillsRun{Scope: a.Scope, Project: a.Project, Diffs: []board.APISkillDiff{{Name: "tool", Diff: "+secret https://pat:pw@host/x /home/u/.ssh/key"}}, Results: []board.APISkillResult{
			{Name: "triage", State: "updated", Detail: "/home/u/.agents/skills"},
			{Name: "tool", State: "behind", Detail: "https://pat:pw@github.com/acme/tool.git"},
		}}, nil
	}
	p.phone("sync", `{"scope":"fleet"}`)
	p.phone("sync", `{"scope":"project","project":"app"}`)
	p.hostPost("sync", `{"scope":"fleet"}`)
	p.hostPost("sync", `{"scope":"user"}`)
	want := []string{
		"skills.synced fleet by phone (Pixel): 1 behind, 1 updated",
		"skills.synced project app by phone (Pixel): 1 behind, 1 updated",
		"skills.synced fleet by operator (desktop): 1 behind, 1 updated",
		"skills.synced user by operator (desktop): 1 behind, 1 updated",
	}
	if got := p.events("skills.synced"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events = %q", got)
	}
	for _, e := range p.events("skills.synced") {
		for _, leak := range []string{"pat:", "/home", "github.com", "secret"} {
			if strings.Contains(e, leak) {
				t.Fatalf("event %q holds %q", e, leak)
			}
		}
	}
}

func TestSyncWritesNoEventForARefusalAndOnlyOneForUpdateAndPropose(t *testing.T) {
	p := newPhoneSkills(t)
	p.phone("sync", `{"scope":"user"}`)
	p.phone("sync", `{"scope":"fleet","yes":"a@b"}`)
	p.phone("update", `{"scope":"fleet"}`)
	p.phone("sync", `{"scope":"all"}`)
	p.post("sync", `{"scope":"fleet"}`, p.cookie, "")
	p.hostPost("update", `{"scope":"fleet"}`)
	p.hostPost("propose", `{"scope":"fleet","name":"triage"}`)
	if got := p.events("skills.synced"); len(got) != 0 {
		t.Fatalf("events = %q", got)
	}
}

func TestAddAndRemoveRecordOneEventEachWithTheSkillName(t *testing.T) {
	p := newPhoneSkills(t)
	p.spy.run = func(_ context.Context, a board.SkillsAction) (board.APISkillsRun, error) {
		st := map[string]string{"add": "added", "remove": "removed"}[a.Verb]
		return board.APISkillsRun{Scope: a.Scope, Project: a.Project, Results: []board.APISkillResult{{Name: "triage", State: st, Detail: "/home/u/.agents/skills https://pat:pw@github.com/acme/x.git"}}}, nil
	}
	for _, c := range []struct{ verb, body string }{
		{"add", `{"scope":"fleet","source":" triage ","ref":"v1"}`},
		{"add", `{"scope":"project","project":"app","source":"review"}`},
		{"remove", `{"scope":"user","name":"triage"}`},
		{"remove", `{"scope":"project","project":"app","name":"review"}`},
	} {
		if rec := p.hostPost(c.verb, c.body); rec.Code != http.StatusOK {
			t.Fatalf("%s %s = %d:\n%s", c.verb, c.body, rec.Code, rec.Body.String())
		}
	}
	want := []string{
		"skills.added fleet by operator (desktop): triage",
		"skills.added project app by operator (desktop): review",
		"skills.removed user by operator (desktop): triage",
		"skills.removed project app by operator (desktop): review",
	}
	got := p.events("skills.added", "skills.removed")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events = %q", got)
	}
	if len(p.events("skills.synced")) != 0 {
		t.Fatal("add or remove wrote skills.synced")
	}
}

func TestAddAndRemoveWriteNoEventWhenRefusedOrNothingChanged(t *testing.T) {
	p := newPhoneSkills(t)
	p.phone("add", `{"scope":"fleet","source":"triage"}`)
	p.phone("remove", `{"scope":"fleet","name":"triage"}`)
	p.post("add", `{"scope":"fleet","source":"triage"}`, host, "")
	p.post("remove", `{"scope":"fleet","name":"triage"}`, host, "wrong")
	p.hostPost("add", `{"scope":"all","source":"triage"}`)
	p.hostPost("add", `{"scope":"fleet"}`)
	p.hostPost("add", `{"scope":"fleet","source":"triage","name":"triage"}`)
	p.hostPost("remove", `{"scope":"fleet"}`)
	p.hostPost("remove", `{"scope":"fleet","name":"a\u0000b"}`)
	p.hostPost("remove", `{"scope":"fleet","name":"triage","source":"x"}`)
	if p.ran() != 0 {
		t.Fatalf("a refused request ran the hook %d times", p.ran())
	}
	for _, err := range []error{state.ErrConflict, state.ErrInvalid, state.ErrNotFound, errors.New("git failed")} {
		p.spy.run = func(context.Context, board.SkillsAction) (board.APISkillsRun, error) {
			return board.APISkillsRun{}, err
		}
		p.hostPost("add", `{"scope":"fleet","source":"triage"}`)
		p.hostPost("remove", `{"scope":"fleet","name":"triage"}`)
	}
	p.spy.run = func(_ context.Context, a board.SkillsAction) (board.APISkillsRun, error) {
		return board.APISkillsRun{Scope: a.Scope, Refused: 1, Results: []board.APISkillResult{{Name: "triage", State: "refused", Detail: "edited here"}}}, nil
	}
	p.hostPost("remove", `{"scope":"fleet","name":"triage"}`)
	if got := p.events("skills.added", "skills.removed", "skills.synced"); len(got) != 0 {
		t.Fatalf("events = %q", got)
	}
}

func TestAFailedSyncRecordsTheErrorKindNotTheMessage(t *testing.T) {
	p := newPhoneSkills(t)
	p.spy.run = func(context.Context, board.SkillsAction) (board.APISkillsRun, error) {
		return board.APISkillsRun{}, errors.New("git failed in /home/u/proj: https://pat:pw@github.com/acme/x.git")
	}
	p.refuses(p.phone("sync", `{"scope":"project","project":"leaky-project"}`), http.StatusInternalServerError, "internal", "")
	want := "skills.synced project by phone (Pixel): failed internal"
	if got := p.events("skills.synced"); len(got) != 1 || got[0] != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
}

func TestASyncWhoseEventCannotBeWrittenAnswers500(t *testing.T) {
	p := newPhoneSkillsOn(t, func(fx *fixture) {
		path := filepath.Join(t.TempDir(), "hand.db")
		st, err := state.Open(path, func() time.Time { return time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC) })
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		if _, err := st.CreateFleet(context.Background(), "test"); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", "file:"+path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec(`CREATE TRIGGER refuse_sync BEFORE INSERT ON event WHEN NEW.kind = 'skills.synced' BEGIN SELECT RAISE(ABORT, 'event refused'); END`); err != nil {
			t.Fatal(err)
		}
		fx.st = st
	})
	p.refuses(p.phone("sync", `{"scope":"fleet"}`), http.StatusInternalServerError, "internal", "")
}
