package transcript

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

func TestCodexFindsARolloutWhoseFirstLineCameLate(t *testing.T) {
	home := t.TempDir()
	path := rollout(t, home, "rollout-2026-09-28T01-00-00-codex-1.jsonl", "")
	r := &Reader{Paths: Paths{Codex: home}}
	ctx := context.Background()
	if _, err := r.Read(ctx, "codex", "codex-1", "/f"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("empty rollout err = %v", err)
	}
	if err := os.WriteFile(path, []byte(meta("codex-1")+userLine("late")), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Read(ctx, "codex", "codex-1", "/f"); err != nil || !slices.Equal(texts(got), []string{"operator: late"}) {
		t.Fatalf("after the first line = %q, %v", texts(got), err)
	}
}

func TestCodexReplaysEveryFileWhenOneShrinks(t *testing.T) {
	home := t.TempDir()
	rollout(t, home, "rollout-2026-09-28T01-00-00-aaa.jsonl", meta("codex-1")+userLine("one"))
	second := rollout(t, home, "rollout-2026-09-28T02-00-00-bbb.jsonl", meta("codex-1")+userLine("two")+userLine("three"))
	r := &Reader{Paths: Paths{Codex: home}}
	ctx := context.Background()
	if got, err := r.Read(ctx, "codex", "codex-1", "/f"); err != nil || len(got) != 3 {
		t.Fatalf("first read = %q, %v", texts(got), err)
	}
	if err := os.WriteFile(second, []byte(meta("codex-1")+userLine("new")), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Read(ctx, "codex", "codex-1", "/f"); err != nil || !slices.Equal(texts(got), []string{"operator: one", "operator: new"}) {
		t.Fatalf("after a shrink = %q, %v", texts(got), err)
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

func TestOpencodeReadsTheInfoAndPartsShape(t *testing.T) {
	count := filepath.Join(t.TempDir(), "count")
	bin := filepath.Join(t.TempDir(), "opencode")
	parts := `{"messages":[` +
		`{"info":{"role":"user","time":{"created":1790570000000}},"parts":[{"type":"text","text":"hello"}]},` +
		`{"info":{"role":"assistant","finish":"stop","time":{"created":1790570001000}},"parts":[{"type":"reasoning","text":"hmm"},{"type":"text","text":"Hi there."}]}]}`
	script := "#!/bin/sh\necho run >> " + count + "\ncat <<'JSON'\n" + parts + "\nJSON\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := (&Reader{Paths: Paths{Opencode: bin, OpencodeData: t.TempDir()}}).Read(context.Background(), "opencode", "ses_1", t.TempDir())
	if err != nil || !slices.Equal(texts(got), []string{"operator: hello", "supervisor: Hi there."}) || got[1].At != "2026-09-28T04:33:21.000Z" {
		t.Fatalf("entries = %q, %v", texts(got), err)
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

func TestClaudeStatusTracksUsageAndCompactions(t *testing.T) {
	body := `{"type":"assistant","timestamp":"2026-09-28T01:00:01Z","message":{"model":"claude-opus-5-5","stop_reason":"end_turn","usage":{"input_tokens":10,"cache_creation_input_tokens":20,"cache_read_input_tokens":30},"content":[{"type":"text","text":"one"}]}}
{"type":"user","timestamp":"2026-09-28T01:00:02Z","isCompactSummary":true,"message":{"content":"summary"}}
{"type":"assistant","timestamp":"2026-09-28T01:00:03Z","isSidechain":true,"message":{"model":"claude-opus-5-5","usage":{"input_tokens":900000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"side"}]}}
{"type":"assistant","timestamp":"2026-09-28T01:00:04Z","message":{"model":"claude-opus-5-5","stop_reason":"end_turn","usage":{"input_tokens":2,"cache_creation_input_tokens":170,"cache_read_input_tokens":508139},"content":[{"type":"text","text":"two"}]}}
`
	home, _ := claudeHome(t, body)
	r := &Reader{Paths: Paths{Claude: home}}
	if _, err := r.Read(context.Background(), "claude", sessionUUID, "/f"); err != nil {
		t.Fatal(err)
	}
	if got := r.Status("claude", sessionUUID); got != (Status{Context: 508311, Window: 1_000_000, Compactions: 1}) {
		t.Fatalf("status = %+v", got)
	}
}

func TestCodexStatusReadsTokenCount(t *testing.T) {
	home := t.TempDir()
	count := func(input int) string {
		return `{"timestamp":"2026-09-28T01:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":` + strconv.Itoa(input) + `,"cached_input_tokens":1},"model_context_window":258400}}}` + "\n"
	}
	rollout(t, home, "rollout-2026-09-28T01-00-00-aaa.jsonl", meta("codex-1")+count(1000)+`{"timestamp":"2026-09-28T01:00:03Z","type":"compacted","payload":{"message":"x"}}`+"\n"+count(72479))
	r := &Reader{Paths: Paths{Codex: home}}
	if _, err := r.Read(context.Background(), "codex", "codex-1", "/f"); err != nil {
		t.Fatal(err)
	}
	if got := r.Status("codex", "codex-1"); got != (Status{Context: 72479, Window: 258400, Compactions: 1}) {
		t.Fatalf("status = %+v", got)
	}
}

func TestOpencodeStatusReadsMessageTokens(t *testing.T) {
	count := filepath.Join(t.TempDir(), "count")
	bin := filepath.Join(t.TempDir(), "opencode")
	export := `{"messages":[` +
		`{"info":{"role":"assistant","finish":"stop","time":{"created":1790570001000},"tokens":{"input":10,"output":5,"cache":{"read":1,"write":1}}},"parts":[{"type":"text","text":"first"}]},` +
		`{"info":{"role":"user","time":{"created":1790570002000}},"parts":[{"type":"compaction"}]},` +
		`{"info":{"role":"assistant","finish":"stop","time":{"created":1790570003000},"tokens":{"input":1000,"output":9,"cache":{"read":5000,"write":200}}},"parts":[{"type":"text","text":"second"}]}]}`
	script := "#!/bin/sh\necho run >> " + count + "\ncat <<'JSON'\n" + export + "\nJSON\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Reader{Paths: Paths{Opencode: bin, OpencodeData: t.TempDir()}}
	if _, err := r.Read(context.Background(), "opencode", "ses_1", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if got := r.Status("opencode", "ses_1"); got != (Status{Context: 6200, Compactions: 1}) {
		t.Fatalf("status = %+v", got)
	}
}

func TestStatusOfAnUnknownSessionIsZero(t *testing.T) {
	if got := (&Reader{}).Status("claude", "nope"); got != (Status{}) {
		t.Fatalf("status = %+v", got)
	}
}

func TestOpencodeStatusReadsTheFlatShape(t *testing.T) {
	count := filepath.Join(t.TempDir(), "count")
	bin := filepath.Join(t.TempDir(), "opencode")
	export := `{"info":{},"messages":[` +
		`{"type":"assistant","time":{"created":1790570001000},"tokens":{"input":175,"output":5,"reasoning":24,"cache":{"read":17024,"write":0}},"content":[{"type":"text","text":"hi"}]},` +
		`{"type":"compaction","time":{"created":1790570002000}},` +
		`{"type":"idle","time":{"created":1790570003000}}]}`
	script := "#!/bin/sh\necho run >> " + count + "\ncat <<'JSON'\n" + export + "\nJSON\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Reader{Paths: Paths{Opencode: bin, OpencodeData: t.TempDir()}}
	if _, err := r.Read(context.Background(), "opencode", "ses_1", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if got := r.Status("opencode", "ses_1"); got != (Status{Context: 17199, Compactions: 1}) {
		t.Fatalf("status = %+v", got)
	}
}

func TestAnAPIErrorKeepsTheLastContext(t *testing.T) {
	body := `{"type":"assistant","timestamp":"2026-09-28T01:00:01Z","message":{"model":"claude-opus-5-5","stop_reason":"end_turn","usage":{"input_tokens":2,"cache_creation_input_tokens":170,"cache_read_input_tokens":508139},"content":[{"type":"text","text":"one"}]}}
{"type":"assistant","timestamp":"2026-09-28T01:00:02Z","isApiErrorMessage":true,"message":{"model":"<synthetic>","usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"You've hit your session limit"}]}}
`
	home, _ := claudeHome(t, body)
	r := &Reader{Paths: Paths{Claude: home}}
	if _, err := r.Read(context.Background(), "claude", sessionUUID, "/f"); err != nil {
		t.Fatal(err)
	}
	if got := r.Status("claude", sessionUUID); got.Context != 508311 {
		t.Fatalf("status after an API error = %+v", got)
	}
}
