package transcript

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCalmKeepsFinalRepliesAndSubstantiveMidTurnText(t *testing.T) {
	cases := []struct {
		text  string
		final bool
		keep  bool
	}{
		{"ok", true, true},
		{"checking", false, false},
		{strings.Repeat("é", 240), false, true},
		{strings.Repeat("é", 239), false, false},
		{"a\nb", false, true},
		{"   " + strings.Repeat("x", 239) + "   ", false, false},
		{"  \n  ", true, false},
	}
	for _, c := range cases {
		if got := calm(c.text, c.final); got != c.keep {
			t.Fatalf("calm(%q, %v) = %v, want %v", c.text, c.final, got, c.keep)
		}
	}
}

func TestClassifyShowsHandsOwnInputsAsOneLine(t *testing.T) {
	digest := "[hand v1 wake]\nattempt.reported a1: r1 done\nattempt.quiet a1: turn ended\ndecision.answered d1: yes"
	var long strings.Builder
	long.WriteString("[hand v1 wake]")
	for range 60 {
		long.WriteString("\nattempt.blocked a1: q")
	}
	cases := []struct {
		in   string
		want Entry
	}{
		{digest, Entry{Role: "hand", Text: "wake: attempt.reported a1: r1 done; attempt.quiet a1: turn ended; decision.answered d1: yes"}},
		{"You are supervisor s3 of the Hand fleet demo. Follow AGENTS.md: run `hand orient` now.", Entry{Role: "hand", Text: "supervisor s3 started"}},
		{"[Request interrupted by user]", Entry{Role: "hand", Text: "interrupted"}},
		{" hello ", Entry{Role: "operator", Text: "hello"}},
	}
	for _, c := range cases {
		if got, ok := classify(c.in); !ok || got != c.want {
			t.Fatalf("classify(%q) = %+v, %v", c.in, got, ok)
		}
	}
	got, ok := classify(long.String())
	if !ok || got.Role != "hand" || len([]rune(got.Text)) != 201 || !strings.HasSuffix(got.Text, "…") || !strings.HasPrefix(got.Text, "wake: attempt.blocked a1: q; ") {
		t.Fatalf("long digest = %q", got.Text)
	}
	if _, ok := classify("  \n "); ok {
		t.Fatal("blank text classified")
	}
}

const sessionUUID = "0f8fad5b-d9cb-469f-a165-70867728950e"

func claudeHome(t *testing.T, body string) (string, string) {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "projects", "-fleets-demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, sessionUUID+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, path
}

func TestClaudeTranscriptIsCalm(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "claude-calm.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	home, _ := claudeHome(t, string(fixture))
	r := &Reader{Paths: Paths{Claude: home}}
	got, err := r.Read(context.Background(), "claude", sessionUUID, "/fleets/demo")
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Role: "hand", Text: "supervisor s1 started", At: "2026-09-28T01:00:00.000Z"},
		{Role: "supervisor", Text: "Found two tasks:\n- t1\n- t2", At: "2026-09-28T01:00:05.000Z"},
		{Role: "supervisor", Text: "Fleet oriented.", At: "2026-09-28T01:00:06.000Z"},
		{Role: "operator", Text: "Please look at t1.", At: "2026-09-28T01:00:10.000Z"},
		{Role: "hand", Text: "wake: attempt.reported a1: r1 done", At: "2026-09-28T01:00:12.000Z"},
		{Role: "operator", Text: "hello there", At: "2026-09-28T01:00:14.000Z"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("entries =\n%+v\nwant\n%+v", got, want)
	}
}

func TestAPartialLineWaitsForItsEnd(t *testing.T) {
	line := func(ts, text string) string {
		return `{"type":"user","timestamp":"` + ts + `","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	third := line("2026-09-28T01:00:03Z", "third")
	home, path := claudeHome(t, line("2026-09-28T01:00:01Z", "first")+line("2026-09-28T01:00:02Z", "second")+third[:20])
	r := &Reader{Paths: Paths{Claude: home}}
	ctx := context.Background()
	got, err := r.Read(ctx, "claude", sessionUUID, "/fleets/demo")
	if err != nil || len(got) != 2 {
		t.Fatalf("first read = %+v, %v", got, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(third[20:]); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	got, err = r.Read(ctx, "claude", sessionUUID, "/fleets/demo")
	if err != nil {
		t.Fatal(err)
	}
	texts := []string{}
	for _, e := range got {
		texts = append(texts, e.Text)
	}
	if !slices.Equal(texts, []string{"first", "second", "third"}) {
		t.Fatalf("second read = %q", texts)
	}
}

func TestUnknownAndUnreadableRecords(t *testing.T) {
	ctx := context.Background()
	home, _ := claudeHome(t, `{"type":"brand-new-kind","x":1}`+"\n"+`{"type":"user","timestamp":"t","message":{"role":"user","content":"hi"}}`+"\n")
	if got, err := (&Reader{Paths: Paths{Claude: home}}).Read(ctx, "claude", sessionUUID, "/f"); err != nil || len(got) != 1 || got[0].Text != "hi" {
		t.Fatalf("unknown record = %+v, %v", got, err)
	}
	home, _ = claudeHome(t, `{"foo":1}`+"\n"+`{"bar":2}`+"\n")
	if _, err := (&Reader{Paths: Paths{Claude: home}}).Read(ctx, "claude", sessionUUID, "/f"); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("unreadable err = %v", err)
	}
	empty := &Reader{Paths: Paths{Claude: t.TempDir()}}
	if _, err := empty.Read(ctx, "claude", sessionUUID, "/f"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("missing err = %v", err)
	}
	if _, err := empty.Read(ctx, "claude", "", "/f"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("empty sessionUUID err = %v", err)
	}
	if _, err := empty.Read(ctx, "claude", "../*", "/f"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("pattern sessionUUID err = %v", err)
	}
}
