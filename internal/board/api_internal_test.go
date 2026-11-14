package board

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
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

func TestAWriteWhoseDetailCannotBeReadStillSucceeds(t *testing.T) {
	st, err := state.Open(filepath.Join(t.TempDir(), "hand.db"), func() time.Time { return time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.CreateFleet(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	b := &Board{st: st, token: "0123456789abcdef0123456789abcdef"}
	rec := httptest.NewRecorder()
	b.apiMutationOK(rec, context.Background(), apiReply{receipt: "r1 marked read"}, apiAckUnread,
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
	if want := apiAckUnread + ": the store went away; refresh to see it"; got.Warn != want {
		t.Fatalf("warn = %q, want %q", got.Warn, want)
	}
	if header, _ := url.PathUnescape(rec.Header().Get("X-Hand-Warn")); header != got.Warn {
		t.Fatalf("X-Hand-Warn = %q, JSON warn = %q", header, got.Warn)
	}
}

func TestAPanicWhileReadingTheVersionIsAnError(t *testing.T) {
	b := &Board{}
	if _, err := b.sharedVersion(context.Background(), false); err == nil {
		t.Fatal("a version read without a store did not fail")
	}
}
