package board

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

func TestADeviceNameCannotSpoofTheActorLabel(t *testing.T) {
	ctx := context.WithValue(context.Background(), sessionKey{}, session{Device: &state.Device{Name: "Pi\u202exel) (operator"}})
	if got, want := actor(ctx, "operator (desktop)"), "phone (Pixel operator)"; got != want {
		t.Fatalf("actor = %q, want %q", got, want)
	}
	if got := actor(context.Background(), "operator (desktop)"); got != "operator (desktop)" {
		t.Fatalf("host actor = %q", got)
	}
}

func TestADeviceNameOfStrippedCharactersIsJustAPhone(t *testing.T) {
	ctx := context.WithValue(context.Background(), sessionKey{}, session{Device: &state.Device{Name: "()\x00\u200b\t"}})
	if got := actor(ctx, "operator (desktop)"); got != "phone" {
		t.Fatalf("actor = %q, want %q", got, "phone")
	}
	ctx = context.WithValue(context.Background(), sessionKey{}, session{Device: &state.Device{Name: "Pixel 9"}})
	if got, want := actor(ctx, "operator (desktop)"), "phone (Pixel 9)"; got != want {
		t.Fatalf("actor = %q, want %q", got, want)
	}
}

func TestAStalledNeedLeavesOutTheIdleTime(t *testing.T) {
	got := apiNeed(waiting{Kind: "stalled", Title: "Supervisor idle for 1h12m with 2 active tasks and no running attempt"}).Title
	if want := "Supervisor idle with 2 active tasks and no running attempt"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
}

func TestANeedCarriesTheAttemptTheBoardDraws(t *testing.T) {
	got := apiNeed(waiting{
		Kind: "failure", Ref: "a2", Title: "Flaky test", Task: state.Task{ID: 2, Project: "hand"},
		Attempt: &state.Attempt{ID: 2, TaskID: 2, Harness: "codex", Model: "gpt-6-luna", Status: state.AttemptFailed,
			Reason: "launch did not finish", EndedAt: "2026-11-01T04:50:00Z"},
	})
	if got.Project != "hand" || got.Reason != "launch did not finish" {
		t.Fatalf("need = %+v", got)
	}
	want := APINeedAttempt{Ref: "a2", Harness: "codex", Model: "gpt-6-luna", Status: "failed", EndedAt: "2026-11-01T04:50:00Z"}
	if got.AttemptMeta == nil || *got.AttemptMeta != want {
		t.Fatalf("attempt_meta = %+v, want %+v", got.AttemptMeta, want)
	}
}

func TestANeedWithoutAnAttemptOmitsItsSlugData(t *testing.T) {
	raw, err := json.Marshal(apiNeed(waiting{Kind: "nosup", Title: "Work is waiting for a supervisor"}))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"attempt_meta", "reason", "project"} {
		if _, ok := got[key]; ok {
			t.Fatalf("a need with no attempt or task carries %s: %s", key, raw)
		}
	}
	report := apiNeed(waiting{
		Kind: "report", Ref: "r1", Title: "Fixed", Task: state.Task{ID: 1, Project: "hand"},
		Report: &state.Report{ID: 1, TaskID: 1, AttemptID: 1, Status: state.ReportDone},
	})
	if report.Attempt != "a1" || report.AttemptMeta != nil || report.Reason != "" || report.Project != "hand" {
		t.Fatalf("report need = %+v", report)
	}
}

func TestANeedOmitsAnAttemptsEmptyModelAndEndTime(t *testing.T) {
	raw, err := json.Marshal(apiNeed(waiting{
		Kind: "kept", Ref: "a3", Title: "Stopped", Task: state.Task{ID: 3, Project: "hand"},
		Attempt: &state.Attempt{ID: 3, TaskID: 3, Harness: "agy", Status: state.AttemptRunning},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		AttemptMeta map[string]json.RawMessage `json:"attempt_meta"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"model", "ended_at"} {
		if _, ok := got.AttemptMeta[key]; ok {
			t.Fatalf("an attempt with no model or end time carries %s: %s", key, raw)
		}
	}
	for _, key := range []string{"ref", "harness", "status"} {
		if _, ok := got.AttemptMeta[key]; !ok {
			t.Fatalf("attempt_meta has no %s: %s", key, raw)
		}
	}
}

func TestAWriteWhoseDetailCannotBeReadStillSucceeds(t *testing.T) {
	st, err := state.Open(filepath.Join(t.TempDir(), "hand.db"), func() time.Time { return time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.CreateFleet(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	fleet, err := st.Fleet(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b := &Board{st: st, token: "0123456789abcdef0123456789abcdef"}
	rec := httptest.NewRecorder()
	b.apiMutationOK(rec, fleet, context.Background(), apiReply{receipt: "r1 marked read"}, apiAckUnread,
		func() (any, error) { return nil, errors.New("the store went away") })
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d:\n%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Receipt string `json:"receipt"`
		Warn    string `json:"warn"`
		Data    struct {
			Accepted bool `json:"accepted"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Data.Accepted || got.Receipt != "r1 marked read" {
		t.Fatalf("answer = %+v", got)
	}
	if want := "the report was marked read, but its detail could not be read: the store went away; refresh to see it"; got.Warn != want {
		t.Fatalf("warn = %q, want %q", got.Warn, want)
	}
	if header, _ := url.PathUnescape(rec.Header().Get("X-Hand-Warn")); header != got.Warn {
		t.Fatalf("X-Hand-Warn = %q, JSON warn = %q", header, got.Warn)
	}
}

func TestAnAnswerWhoseFleetCannotBeReadWritesNothing(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hand.db")
	st, err := state.Open(path, func() time.Time { return time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.CreateFleet(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddProject(ctx, "hand", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	task, err := st.AddTask(ctx, "hand", "Cookie", "keep the docs")
	if err != nil {
		t.Fatal(err)
	}
	d, err := st.Ask(ctx, task.ID, "Keep the old cookie name?")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DROP TABLE fleet`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	b := &Board{st: st, token: "0123456789abcdef0123456789abcdef"}
	req := httptest.NewRequest("POST", "/api/v1/decision/"+state.DecisionRef(d.ID)+"/answer", strings.NewReader(`{"choice":"1. Yes"}`))
	req.SetPathValue("id", state.DecisionRef(d.ID))
	req = req.WithContext(context.WithValue(ctx, sessionKey{}, session{CSRF: "csrf"}))
	req.Header.Set("X-Hand-CSRF", "csrf")
	rec := httptest.NewRecorder()
	b.apiAnswer(rec, req)
	var got struct {
		Receipt string   `json:"receipt"`
		Error   APIError `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusInternalServerError || got.Error.Code != "internal" {
		t.Fatalf("answer = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(got.Error.Message, "no such table: fleet") {
		t.Fatalf("error = %+v", got.Error)
	}
	if got.Receipt != "" || rec.Header().Get("X-Hand-Receipt") != "" {
		t.Fatalf("a failed answer carries a receipt: %q, %q", got.Receipt, rec.Header().Get("X-Hand-Receipt"))
	}
	open, err := st.Decision(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if open.Status != state.DecisionOpen || open.Answer != "" {
		t.Fatalf("the answer was written: %+v", open)
	}
}

func TestAPanicWhileReadingTheVersionIsAnError(t *testing.T) {
	st, err := state.Open(filepath.Join(t.TempDir(), "hand.db"), func() time.Time { return time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	b := &Board{st: st, o: Options{Now: func() time.Time { panic("clock") }}}
	if _, err := b.sharedVersion(context.Background(), false); err == nil || !strings.Contains(err.Error(), "clock") {
		t.Fatalf("a version read that panicked = %v", err)
	}
}
