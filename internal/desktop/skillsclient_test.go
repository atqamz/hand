package desktop_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/desktop"
)

const skillsEnvelope = `{"version":1,"fleet":{"id":"` + fleetID + `","name":"golden"},"cursor":"c","generated_at":"2026-11-01T05:00:00Z",`

func skillsServer(t *testing.T, delay time.Duration) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/"+fleetID)
		if rest == "/proof" {
			proof(w, r)
			return
		}
		if rest == "/api/v1/session" {
			io.WriteString(w, skillsEnvelope+`"data":{"csrf":"`+testToken+`"}}`)
			return
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("X-Hand-CSRF")+" "+string(b))
		mu.Unlock()
		time.Sleep(delay)
		switch {
		case r.Method == http.MethodGet && rest == "/api/v1/skills":
			io.WriteString(w, skillsEnvelope+`"data":{"scopes":[
{"scope":"fleet","skills":[{"name":"secondhand","source":"hand init","managed":true,"trusted":true,"state":"current"}]},
{"scope":"project","project":"hand","skills":[{"name":"notes","source":"https://github.com/example/notes.git","ref":"v1","commit":"aa","latest":"bb","managed":true,"trusted":false,"state":"behind","diff":"+x\n","diff_cut":true}]},
{"scope":"project","project":"api","error":"the folder is not a git checkout","skills":[]}]}}`)
		case r.Method == http.MethodPost && rest == "/api/v1/skills/sync":
			io.WriteString(w, `{"version":1,"fleet":{"id":"`+fleetID+`","name":"golden"},"cursor":"c","generated_at":"2026-11-01T05:00:00Z","receipt":"Skills sync: 1 refused","data":{"scope":"project","project":"hand","results":[{"name":"notes","state":"refused","detail":"notes moved"}],"refused":1,"reconcile":["hand task add --goal x"]}}`)
		case r.Method == http.MethodPost && rest == "/api/v1/skills/propose":
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"error":{"status":403,"code":"forbidden","message":"a paired Companion cannot change skills; use the host's Board"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":{"status":404,"code":"not_found","message":"not found"}}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestHTTPClientReadsTheSkills(t *testing.T) {
	srv, seen := skillsServer(t, 0)
	got, err := goldenClient(t, srv.URL).Skills(context.Background(), fleetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Scopes) != 3 || got.Scopes[1].Project != "hand" || got.Scopes[2].Error != "the folder is not a git checkout" {
		t.Fatalf("scopes = %+v", got.Scopes)
	}
	s := got.Scopes[1].Skills[0]
	if s.Name != "notes" || s.Commit != "aa" || s.Latest != "bb" || s.Trusted || !s.Managed || s.State != "behind" || s.Diff != "+x\n" || !s.DiffCut {
		t.Fatalf("skill = %+v", s)
	}
	if len(*seen) != 1 || !strings.HasPrefix((*seen)[0], "GET /"+fleetID+"/api/v1/skills ") {
		t.Fatalf("requests = %q", *seen)
	}
}

func TestHTTPClientWaitsForASlowSkillsReadPastItsOrdinaryTimeout(t *testing.T) {
	srv, _ := skillsServer(t, 300*time.Millisecond)
	c := goldenClient(t, srv.URL)
	c.HTTP = &http.Client{Timeout: 100 * time.Millisecond}
	if _, err := c.Skills(context.Background(), fleetID); err != nil {
		t.Fatalf("a skills read is cut at the ordinary timeout: %v", err)
	}
	if _, err := c.SkillsRun(context.Background(), fleetID, "sync", desktop.SkillsRequest{Scope: "fleet"}); err != nil {
		t.Fatalf("a skills action is cut at the ordinary timeout: %v", err)
	}
	if _, err := c.Projects(context.Background(), fleetID); err == nil {
		t.Fatal("another read is no longer bounded by the client's timeout")
	}
}

func TestHTTPClientPostsASkillsActionWithTheCSRFTokenAndKeepsTheReceipt(t *testing.T) {
	srv, seen := skillsServer(t, 0)
	run, err := goldenClient(t, srv.URL).SkillsRun(context.Background(), fleetID, "sync",
		desktop.SkillsRequest{Scope: "project", Project: "hand", Name: "notes", Yes: "notes@bb"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Receipt != "Skills sync: 1 refused" || run.Refused != 1 || run.Project != "hand" ||
		len(run.Results) != 1 || run.Results[0].Detail != "notes moved" || len(run.Reconcile) != 1 {
		t.Fatalf("run = %+v", run)
	}
	want := "POST /" + fleetID + "/api/v1/skills/sync " + testToken + ` {"scope":"project","project":"hand","name":"notes","yes":"notes@bb"}`
	if len(*seen) != 1 || (*seen)[0] != want {
		t.Fatalf("requests = %q, want %q", *seen, want)
	}
}

func TestHTTPClientKeepsTheBoardsSentenceForARefusedSkillsAction(t *testing.T) {
	srv, _ := skillsServer(t, 0)
	_, err := goldenClient(t, srv.URL).SkillsRun(context.Background(), fleetID, "propose", desktop.SkillsRequest{Scope: "fleet", Name: "x"})
	api, ok := err.(*desktop.APIError)
	if !ok || api.Status != 403 || api.Code != "forbidden" || api.Message != "a paired Companion cannot change skills; use the host's Board" {
		t.Fatalf("err = %#v", err)
	}
}
