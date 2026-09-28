package transcript

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func rollout(t *testing.T, home, name, body string) string {
	t.Helper()
	dir := filepath.Join(home, "sessions", "2026", "09", "28")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func meta(id string) string {
	return `{"timestamp":"2026-09-28T01:00:00Z","type":"session_meta","payload":{"id":"` + id + `","cwd":"/f"}}` + "\n"
}

func userLine(text string) string {
	return `{"timestamp":"2026-09-28T01:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + text + `"}]}}` + "\n"
}

func texts(es []Entry) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.Role+": "+e.Text)
	}
	return out
}

func TestCodexTranscriptIsCalm(t *testing.T) {
	home := t.TempDir()
	fixture, err := os.ReadFile(filepath.Join("testdata", "codex-calm.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	rollout(t, home, "rollout-2026-09-28T01-00-00-codex-1.jsonl", string(fixture))
	got, err := (&Reader{Paths: Paths{Codex: home}}).Read(context.Background(), "codex", "codex-1", "/fleets/demo")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"hand: supervisor s1 started", "supervisor: Two tasks:\n- t1", "supervisor: Done.", "operator: hi", "supervisor: Hello."}
	if !slices.Equal(texts(got), want) {
		t.Fatalf("entries = %q", texts(got))
	}
	if got[2].At != "2026-09-28T01:00:10.000Z" {
		t.Fatalf("final reply at %q", got[2].At)
	}
}

func TestCodexFollowsTheSessionIDAcrossFiles(t *testing.T) {
	home := t.TempDir()
	rollout(t, home, "rollout-2026-09-28T01-00-00-aaa.jsonl", meta("codex-1")+userLine("one"))
	rollout(t, home, "rollout-2026-09-28T02-00-00-bbb.jsonl", meta("codex-1")+userLine("two"))
	rollout(t, home, "rollout-2026-09-28T03-00-00-ccc.jsonl", meta("other")+userLine("three"))
	got, err := (&Reader{Paths: Paths{Codex: home}}).Read(context.Background(), "codex", "codex-1", "/f")
	if err != nil || !slices.Equal(texts(got), []string{"operator: one", "operator: two"}) {
		t.Fatalf("entries = %q, %v", texts(got), err)
	}
}

func TestCodexTurnSurvivesIncrementalReads(t *testing.T) {
	home := t.TempDir()
	path := rollout(t, home, "rollout-2026-09-28T01-00-00-codex-1.jsonl", meta("codex-1")+`{"timestamp":"2026-09-28T01:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Short."}]}}`+"\n")
	r := &Reader{Paths: Paths{Codex: home}}
	ctx := context.Background()
	if got, err := r.Read(ctx, "codex", "codex-1", "/f"); err != nil || len(got) != 0 {
		t.Fatalf("mid-turn = %q, %v", texts(got), err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"timestamp":"2026-09-28T01:00:03Z","type":"event_msg","payload":{"type":"task_complete"}}` + "\n")
	_ = f.Close()
	if got, err := r.Read(ctx, "codex", "codex-1", "/f"); err != nil || !slices.Equal(texts(got), []string{"supervisor: Short."}) {
		t.Fatalf("after task_complete = %q, %v", texts(got), err)
	}
}

const opencodeExport = `{"info":{"id":"ses_1"},"messages":[` +
	`{"id":"m1","time":{"created":1790570000000},"type":"user","text":"You are supervisor s2 of the Hand fleet demo. Follow AGENTS.md"},` +
	`{"id":"m2","time":{"created":1790570001000},"type":"assistant","finish":"tool-calls","content":[{"type":"text","text":"Checking."},{"type":"tool","tool":"bash"}]},` +
	`{"id":"m3","time":{"created":1790570002000},"type":"assistant","finish":"stop","content":[{"type":"reasoning","text":"hmm"},{"type":"text","text":"Plan:\n1. orient"},{"type":"text","text":"Done."}]},` +
	`{"id":"m4","time":{"created":1790570003000},"type":"idle"}]}`

func fakeOpencode(t *testing.T, count string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "opencode")
	script := "#!/bin/sh\n[ \"$*\" = \"session export --standalone ses_1\" ] || { echo \"bad args: $*\" >&2; exit 1; }\necho run >> " + count + "\ncat <<'JSON'\n" + opencodeExport + "\nJSON\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func runs(t *testing.T, count string) int {
	t.Helper()
	b, _ := os.ReadFile(count)
	return strings.Count(string(b), "run")
}

func TestOpencodeTranscriptIsCalm(t *testing.T) {
	count := filepath.Join(t.TempDir(), "count")
	r := &Reader{Paths: Paths{Opencode: fakeOpencode(t, count), OpencodeData: t.TempDir()}}
	got, err := r.Read(context.Background(), "opencode", "ses_1", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"hand: supervisor s2 started", "supervisor: Plan:\n1. orient", "supervisor: Done."}
	if !slices.Equal(texts(got), want) {
		t.Fatalf("entries = %q", texts(got))
	}
	if got[2].At != "2026-09-28T04:33:22.000Z" {
		t.Fatalf("at = %q", got[2].At)
	}
}

func TestOpencodeReexportsOnlyWhenItsDatabaseChanges(t *testing.T) {
	count := filepath.Join(t.TempDir(), "count")
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "opencode.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Reader{Paths: Paths{Opencode: fakeOpencode(t, count), OpencodeData: data}}
	ctx := context.Background()
	dir := t.TempDir()
	for range 2 {
		if _, err := r.Read(ctx, "opencode", "ses_1", dir); err != nil {
			t.Fatal(err)
		}
	}
	if n := runs(t, count); n != 1 {
		t.Fatalf("exports = %d, want 1 while the database is unchanged", n)
	}
	wal := filepath.Join(data, "opencode.db-wal")
	if err := os.WriteFile(wal, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(wal, later, later); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(ctx, "opencode", "ses_1", dir); err != nil {
		t.Fatal(err)
	}
	if n := runs(t, count); n != 2 {
		t.Fatalf("exports = %d, want 2 after the database changed", n)
	}
}
