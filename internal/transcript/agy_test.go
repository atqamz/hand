package transcript

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/agydb/agytest"
)

var agyT0 = time.Date(2026, 10, 1, 5, 30, 0, 0, time.UTC)

func TestAgyTranscriptReadsTheConversation(t *testing.T) {
	dir := t.TempDir()
	agytest.Conversation(t, dir, "c1", "/srv/fleet", 18001, 256000,
		agytest.Step{Type: 14, Text: "hello", At: agyT0},
		agytest.Step{Type: 15, Tool: true, At: agyT0},
		agytest.Step{Type: 15, Text: "done", At: agyT0.Add(time.Second)},
	)
	r := &Reader{Paths: Paths{Agy: dir}}
	got, err := r.Read(context.Background(), "agy", "c1", "/srv/fleet")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Role != "operator" || got[0].Text != "hello" || got[1].Role != "supervisor" || got[1].Text != "done" {
		t.Fatalf("entries = %+v", got)
	}
	if got[1].At != "2026-10-01T05:30:01.000Z" {
		t.Fatalf("reply at = %q", got[1].At)
	}
	if st := r.Status("agy", "c1"); st.Context != 18001 || st.Window != 256000 {
		t.Fatalf("status = %+v", st)
	}
}

func TestAgyTranscriptFollowsNewTurns(t *testing.T) {
	dir := t.TempDir()
	path := agytest.Conversation(t, dir, "c1", "/srv/fleet", 18001, 256000, agytest.Step{Type: 14, Text: "hello", At: agyT0})
	r := &Reader{Paths: Paths{Agy: dir}}
	if _, err := r.Read(context.Background(), "agy", "c1", ""); err != nil {
		t.Fatal(err)
	}
	agytest.Append(t, path, 18330, 256000, agytest.Step{Type: 14, Text: "[hand v1 wake]\nattempt.quiet a1: turn ended", At: agyT0.Add(time.Minute)})
	touch(t, path, time.Now().Add(time.Second))
	got, err := r.Read(context.Background(), "agy", "c1", "")
	if err != nil || len(got) != 2 || got[1].Role != "hand" || got[1].Text != "wake: attempt.quiet a1: turn ended" {
		t.Fatalf("entries = %+v, err %v", got, err)
	}
	if st := r.Status("agy", "c1"); st.Context != 18330 {
		t.Fatalf("status = %+v", st)
	}
}

func TestAgyTranscriptKeepsEntriesWhileLocked(t *testing.T) {
	dir := t.TempDir()
	path := agytest.Conversation(t, dir, "c1", "/srv/fleet", 18001, 256000, agytest.Step{Type: 14, Text: "hello", At: agyT0})
	r := &Reader{Paths: Paths{Agy: dir}}
	if _, err := r.Read(context.Background(), "agy", "c1", ""); err != nil {
		t.Fatal(err)
	}
	w, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	conn, err := w.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, q := range []string{"PRAGMA locking_mode=EXCLUSIVE", "BEGIN EXCLUSIVE", "INSERT INTO steps (idx, step_type) VALUES (99, 0)"} {
		if _, err := conn.ExecContext(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	touch(t, path, time.Now().Add(time.Second))
	got, err := r.Read(context.Background(), "agy", "c1", "")
	if err != nil || len(got) != 1 || got[0].Text != "hello" {
		t.Fatalf("entries = %+v, err %v", got, err)
	}
}

func TestAgyTranscriptThatCannotBeDecodedIsUnreadable(t *testing.T) {
	dir := t.TempDir()
	agytest.Conversation(t, dir, "c1", "/srv/fleet", 0, 0, agytest.Step{Type: 14, Raw: []byte{0x0a, 0xff}}, agytest.Step{Type: 15, Raw: []byte{0x0a, 0xff}})
	if _, err := (&Reader{Paths: Paths{Agy: dir}}).Read(context.Background(), "agy", "c1", ""); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("err = %v", err)
	}
}

func TestAgyTranscriptWaitsForItsFile(t *testing.T) {
	r := &Reader{Paths: Paths{Agy: t.TempDir()}}
	if _, err := r.Read(context.Background(), "agy", "missing", ""); !errors.Is(err, ErrNoSession) {
		t.Fatalf("err = %v", err)
	}
}

func touch(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(filepath.Clean(path)+"-wal", at, at)
}
