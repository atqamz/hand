package agydb_test

import (
	"slices"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/agydb"
	"github.com/atqamz/hand/internal/agydb/agytest"
)

var t0 = time.Date(2026, 10, 1, 5, 30, 0, 123000000, time.UTC)

func TestStepsReadOperatorAndReplies(t *testing.T) {
	path := agytest.Conversation(t, t.TempDir(), "c1", "/srv/fleet", 18001, 256000,
		agytest.Step{Type: 14, Text: "hello", At: t0},
		agytest.Step{Type: 15, Tool: true, At: t0},
		agytest.Step{Type: 132, Text: "tool output", At: t0},
		agytest.Step{Type: 15, Text: "hi there", At: t0.Add(time.Second)},
	)
	db, err := agydb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	steps, err := agydb.Steps(db)
	if err != nil {
		t.Fatal(err)
	}
	want := []agydb.Step{{Type: 14, Text: "hello", At: t0}, {Type: 15, Tool: true, At: t0}, {Type: 15, Text: "hi there", At: t0.Add(time.Second)}}
	if !slices.EqualFunc(steps, want, func(a, b agydb.Step) bool {
		return a.Type == b.Type && a.Text == b.Text && a.Tool == b.Tool && a.At.Equal(b.At)
	}) {
		t.Fatalf("steps = %+v", steps)
	}
}

func TestStepsSkipAMalformedPayload(t *testing.T) {
	path := agytest.Conversation(t, t.TempDir(), "c1", "/srv/fleet", 0, 0,
		agytest.Step{Type: 14, Text: "first", At: t0},
		agytest.Step{Type: 15, Raw: []byte{0x0a, 0xff}},
		agytest.Step{Type: 15, Text: "second", At: t0},
	)
	db, err := agydb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	steps, err := agydb.Steps(db)
	if err != nil || len(steps) != 2 || steps[0].Text != "first" || steps[1].Text != "second" {
		t.Fatalf("steps = %+v, err %v", steps, err)
	}
}

func TestWorkspaceAndUsage(t *testing.T) {
	path := agytest.Conversation(t, t.TempDir(), "c1", "/srv/fleet", 18001, 256000, agytest.Step{Type: 14, Text: "hello", At: t0})
	agytest.Append(t, path, 19208, 256000, agytest.Step{Type: 15, Text: "done", At: t0})
	db, err := agydb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if ws, err := agydb.Workspace(db); err != nil || ws != "/srv/fleet" {
		t.Fatalf("workspace = %q, %v", ws, err)
	}
	if used, window, err := agydb.Usage(db); err != nil || used != 19208 || window != 256000 {
		t.Fatalf("usage = %d/%d, %v", used, window, err)
	}
}

func TestWorkspaceComesFromTheFirstOperatorStepThatNamesIt(t *testing.T) {
	path := agytest.Conversation(t, t.TempDir(), "c1", "", 0, 0,
		agytest.Step{Type: 14, Text: "launch", At: t0},
		agytest.Step{Type: 15, Text: "ok", At: t0},
		agytest.Step{Type: 14, Text: "next", At: t0, Workspace: "/srv/fleet"},
	)
	db, err := agydb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if ws, err := agydb.Workspace(db); err != nil || ws != "/srv/fleet" {
		t.Fatalf("workspace = %q, %v", ws, err)
	}
}

func TestOpenIsReadOnly(t *testing.T) {
	path := agytest.Conversation(t, t.TempDir(), "c1", "/srv/fleet", 0, 0, agytest.Step{Type: 14, Text: "hello", At: t0})
	db, err := agydb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT INTO steps (idx, step_type) VALUES (9, 14)"); err == nil {
		t.Fatal("a write through the read-only handle succeeded")
	}
	if steps, err := agydb.Steps(db); err != nil || len(steps) != 1 {
		t.Fatalf("steps = %+v, err %v", steps, err)
	}
}
