package transcript

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
)

func TestMain(m *testing.M) {
	fakebin.Main(map[string]func([]string) int{"opencode": func(args []string) int {
		p := fakebin.Params()
		if a := strings.Join(args, " "); p["args"] != "" && a != p["args"] {
			fmt.Fprintln(os.Stderr, "bad args:", a)
			return 1
		}
		fakebin.Append(p["count"], "run")
		if d, err := time.ParseDuration(p["sleep"]); err == nil && (p["slow"] == "" || p["slow"] == args[len(args)-1]) {
			time.Sleep(d)
		}
		touch := func() {
			if p["touch"] == "" {
				return
			}
			b, _ := os.ReadFile(p["count"])
			n := strings.Count(string(b), "run")
			at := time.Now().Add(time.Duration(n) * time.Minute)
			f, err := os.OpenFile(p["touch"], os.O_CREATE|os.O_WRONLY, 0o644)
			if err == nil {
				f.Close()
				_ = os.Chtimes(p["touch"], at, at)
			}
		}
		out := p["export"]
		if b, _ := os.ReadFile(p["count"]); p["export2"] != "" && strings.Count(string(b), "run") > 1 {
			out = p["export2"]
		}
		late, _ := time.ParseDuration(p["late"])
		if late == 0 {
			touch()
		}
		fmt.Println(out)
		if late > 0 {
			time.Sleep(late)
			touch()
		}
		return 0
	}})
	os.Exit(m.Run())
}

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
	return fakebin.Install(t, t.TempDir(), "opencode", "opencode", map[string]string{"args": "session export --standalone ses_1", "count": count, "export": opencodeExport})
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
	parts := `{"messages":[` +
		`{"info":{"role":"user","time":{"created":1790570000000}},"parts":[{"type":"text","text":"hello"}]},` +
		`{"info":{"role":"assistant","finish":"stop","time":{"created":1790570001000}},"parts":[{"type":"reasoning","text":"hmm"},{"type":"text","text":"Hi there."}]}]}`
	bin := fakebin.Install(t, t.TempDir(), "opencode", "opencode", map[string]string{"count": count, "export": parts})
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

func touchingOpencode(t *testing.T, count, data, sleep string) string {
	t.Helper()
	return fakebin.Install(t, t.TempDir(), "opencode", "opencode", map[string]string{"count": count, "export": opencodeExport, "touch": filepath.Join(data, "opencode.db-wal"), "sleep": sleep})
}

func TestOpencodeExportsAnEndedSessionOnce(t *testing.T) {
	count := filepath.Join(t.TempDir(), "count")
	data := t.TempDir()
	r := &Reader{Paths: Paths{Opencode: touchingOpencode(t, count, data, ""), OpencodeData: data}}
	ctx, dir := context.Background(), t.TempDir()
	ids := []string{"ses_1", "ses_2", "ses_3"}
	for range 4 {
		for _, id := range ids {
			if _, err := r.ReadEnded(ctx, "opencode", id, dir); err != nil {
				t.Fatal(err)
			}
		}
	}
	if n := runs(t, count); n != len(ids) {
		t.Fatalf("exports = %d, want %d: one per ended session", n, len(ids))
	}
}

func TestOpencodeReadsASessionOnceItEnds(t *testing.T) {
	count := filepath.Join(t.TempDir(), "count")
	data := t.TempDir()
	r := &Reader{Paths: Paths{Opencode: touchingOpencode(t, count, data, ""), OpencodeData: data}}
	ctx, dir := context.Background(), t.TempDir()
	if _, err := r.Read(ctx, "opencode", "ses_1", dir); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := r.ReadEnded(ctx, "opencode", "ses_1", dir); err != nil {
			t.Fatal(err)
		}
	}
	if n := runs(t, count); n != 2 {
		t.Fatalf("exports = %d, want 2: the live read, then one after it ended", n)
	}
}

func TestOpencodeCachesTheLiveSessionAfterItsOwnExport(t *testing.T) {
	count := filepath.Join(t.TempDir(), "count")
	data := t.TempDir()
	r := &Reader{Paths: Paths{Opencode: touchingOpencode(t, count, data, ""), OpencodeData: data}}
	ctx, dir := context.Background(), t.TempDir()
	for range 4 {
		if _, err := r.Read(ctx, "opencode", "ses_1", dir); err != nil {
			t.Fatal(err)
		}
	}
	if n := runs(t, count); n != 1 {
		t.Fatalf("exports = %d, want 1 while nothing else writes the database", n)
	}
}

func stubbed(t *testing.T, params map[string]string) (*Reader, string) {
	t.Helper()
	count, data := filepath.Join(t.TempDir(), "count"), t.TempDir()
	p := map[string]string{"count": count, "export": opencodeExport, "touch": filepath.Join(data, "opencode.db-wal")}
	for k, v := range params {
		p[k] = v
	}
	return &Reader{Paths: Paths{Opencode: fakebin.Install(t, t.TempDir(), "opencode", "opencode", p), OpencodeData: data}}, count
}

func TestOpencodeExportDoesNotBlockStatusOrOtherSessions(t *testing.T) {
	r, count := stubbed(t, map[string]string{"sleep": "2s", "slow": "ses_1"})
	ctx, dir := context.Background(), t.TempDir()
	done := make(chan string, 2)
	go func() {
		_, err := r.Read(ctx, "opencode", "ses_1", dir)
		done <- fmt.Sprint("ses_1 ", err)
	}()
	for runs(t, count) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	r.Status("opencode", "ses_1")
	r.Forget("opencode", "ses_9")
	if _, err := r.Read(ctx, "opencode", "ses_2", dir); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		t.Fatalf("%s ended before the other session returned", got)
	default:
	}
	if got := <-done; got != "ses_1 <nil>" {
		t.Fatal(got)
	}
}

func TestOpencodeReaderLeavesAHungExportWhenItsContextEnds(t *testing.T) {
	r, count := stubbed(t, map[string]string{"sleep": "3s"})
	dir := t.TempDir()
	hung := make(chan struct{})
	go func() {
		defer close(hung)
		_, _ = r.Read(context.Background(), "opencode", "ses_1", dir)
	}()
	defer func() { <-hung }()
	for runs(t, count) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := r.Read(ctx, "opencode", "ses_1", dir)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("read = %v after %v, want the deadline after about 500ms", err, time.Since(start))
	}
	if n := runs(t, count); n != 1 {
		t.Fatalf("exports = %d, want 1", n)
	}
}

func TestOpencodeWaitersShareAFailedExport(t *testing.T) {
	r, count := stubbed(t, map[string]string{"sleep": "300ms", "export": ""})
	ctx, dir := context.Background(), t.TempDir()
	errs := make(chan error, 5)
	for range 5 {
		go func() {
			_, err := r.Read(ctx, "opencode", "ses_1", dir)
			errs <- err
		}()
	}
	for range 5 {
		if err := <-errs; !errors.Is(err, ErrUnreadable) {
			t.Fatalf("read = %v, want %v", err, ErrUnreadable)
		}
	}
	if n := runs(t, count); n != 1 {
		t.Fatalf("exports = %d, want 1: the waiters get the failure instead of retrying", n)
	}
}

func TestOpencodeSeesAWriteThatLandsAfterTheExportWithinTheFloor(t *testing.T) {
	old := reexport
	reexport = 400 * time.Millisecond
	t.Cleanup(func() { reexport = old })
	later := strings.Replace(opencodeExport, `"text":"Done."`, `"text":"Late reply."`, 1)
	r, count := stubbed(t, map[string]string{"late": "200ms", "export2": later})
	ctx, dir := context.Background(), t.TempDir()
	last := func() string {
		t.Helper()
		got, err := r.Read(ctx, "opencode", "ses_1", dir)
		if err != nil {
			t.Fatal(err)
		}
		return got[len(got)-1].Text
	}
	last()
	if got := last(); got == "Late reply." || runs(t, count) != 1 {
		t.Fatalf("last = %q after %d exports, want one export inside the floor", got, runs(t, count))
	}
	time.Sleep(reexport + 100*time.Millisecond)
	if got := last(); got != "Late reply." || runs(t, count) != 2 {
		t.Fatalf("last = %q after %d exports, want the late reply after the floor", got, runs(t, count))
	}
}

func TestOpencodeRetriesAFailedEndedExportOnlyAfterTheRetryWindow(t *testing.T) {
	r, count := stubbed(t, map[string]string{"export": ""})
	ctx, dir := context.Background(), t.TempDir()
	for range 5 {
		if _, err := r.ReadEnded(ctx, "opencode", "ses_1", dir); !errors.Is(err, ErrUnreadable) {
			t.Fatalf("read = %v, want %v", err, ErrUnreadable)
		}
	}
	if n := runs(t, count); n != 1 {
		t.Fatalf("exports = %d, want 1", n)
	}
	r.session("opencode:ses_1").failedAt = time.Now().Add(-endedRetry - time.Second)
	_, _ = r.ReadEnded(ctx, "opencode", "ses_1", dir)
	if n := runs(t, count); n != 2 {
		t.Fatalf("exports = %d, want 2 after the retry window", n)
	}
}

func TestOpencodeConcurrentReadersExportEachSessionOnce(t *testing.T) {
	r, count := stubbed(t, map[string]string{"sleep": "300ms"})
	ctx, dir := context.Background(), t.TempDir()
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Read(ctx, "opencode", fmt.Sprintf("ses_%d", i%2), dir); err != nil {
				t.Error(err)
			}
			r.Status("opencode", "ses_0")
		}()
	}
	wg.Wait()
	if n := runs(t, count); n != 2 {
		t.Fatalf("exports = %d, want 2: one per session", n)
	}
}

func TestOpencodeForgetDropsTheGate(t *testing.T) {
	r, _ := stubbed(t, nil)
	if _, err := r.Read(context.Background(), "opencode", "ses_1", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	r.Forget("opencode", "ses_1")
	if len(r.gates) != 0 || len(r.logs) != 0 {
		t.Fatalf("gates = %d, logs = %d, want none", len(r.gates), len(r.logs))
	}
}

func ompSession(t *testing.T, home, id, body string) string {
	t.Helper()
	dir := filepath.Join(home, "sessions", "-fleets-demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "2026-09-28T01-00-00-000Z_"+id+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOmpTranscriptIsCalm(t *testing.T) {
	home := t.TempDir()
	fixture, err := os.ReadFile(filepath.Join("testdata", "omp-calm.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	ompSession(t, home, "omp-1", string(fixture))
	r := &Reader{Paths: Paths{Omp: home}}
	got, err := r.Read(context.Background(), "omp", "omp-1", "/fleets/demo")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"hand: supervisor s1 started", "supervisor: Two tasks:\n- t1", "operator: hi", "supervisor: Hello."}
	if !slices.Equal(texts(got), want) {
		t.Fatalf("entries = %q", texts(got))
	}
	if st := r.Status("omp", "omp-1"); st.Model != "commandcode/deepseek/deepseek-v4-pro" || st.Effort != "low" || st.Context != 1500 || st.ModelAt != "2026-09-28T01:00:05.000Z" {
		t.Fatalf("status = %+v", st)
	}
}

func TestOmpTranscriptReadsItsSessionFromTheLaunchFolder(t *testing.T) {
	home := t.TempDir()
	fixture, err := os.ReadFile(filepath.Join("testdata", "omp-calm.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	ompSession(t, home, "omp-1", string(fixture))
	r := &Reader{Paths: Paths{}}
	got, err := r.Read(context.Background(), "omp", "omp-1", home)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(texts(got), []string{"hand: supervisor s1 started", "supervisor: Two tasks:\n- t1", "operator: hi", "supervisor: Hello."}) {
		t.Fatalf("entries = %q", texts(got))
	}
}

func TestOmpTranscriptReadsAnAgentFolderWithGlobCharacters(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home[1]")
	fixture, err := os.ReadFile(filepath.Join("testdata", "omp-calm.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	ompSession(t, home, "omp-1", string(fixture))
	r := &Reader{Paths: Paths{Omp: home}}
	got, err := r.Read(context.Background(), "omp", "omp-1", "/fleets/demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("entries = %q", texts(got))
	}
}

func TestOmpReadsASessionThatAppearsLateAndToleratesATruncatedLine(t *testing.T) {
	home := t.TempDir()
	r := &Reader{Paths: Paths{Omp: home}}
	ctx := context.Background()
	if _, err := r.Read(ctx, "omp", "omp-1", "/fleets/demo"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("missing session err = %v", err)
	}
	path := ompSession(t, home, "omp-1", `{"type":"session","version":3,"id":"omp-1","timestamp":"2026-09-28T01:00:00.000Z","cwd":"/fleets/demo"}`+"\n")
	if _, err := r.Read(ctx, "omp", "omp-1", "/fleets/demo"); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"type":"message","timestamp":"2026-09-28T01:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}` + "\n")
	_, _ = f.WriteString(`{"type":"message","timestamp":"2026-09-28T01:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"cut`)
	_ = f.Close()
	if got, err := r.Read(ctx, "omp", "omp-1", "/fleets/demo"); err != nil || !slices.Equal(texts(got), []string{"operator: hello"}) {
		t.Fatalf("after a truncated line = %q, %v", texts(got), err)
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
	if got := r.Status("claude", sessionUUID); got != (Status{Context: 508311, Window: 1_000_000, Compactions: 1, Model: "claude-opus-5-5", ModelAt: "2026-09-28T01:00:04Z"}) {
		t.Fatalf("status = %+v", got)
	}
}

func TestClaudeStatusFollowsTheServedModelAndEffort(t *testing.T) {
	assistant := func(at, model, extra string) string {
		return `{"type":"assistant","timestamp":"` + at + `",` + extra + `"message":{"model":"` + model + `","content":[]}}` + "\n"
	}
	home, path := claudeHome(t, assistant("2026-09-28T01:00:01Z", "claude-sonnet-5-5", `"requestedModel":"claude-sonnet-5-5","effort":"high","perTurnEffort":"high",`))
	r := &Reader{Paths: Paths{Claude: home}}
	ctx := context.Background()
	check := func(want Status) {
		t.Helper()
		if _, err := r.Read(ctx, "claude", sessionUUID, "/f"); err != nil {
			t.Fatal(err)
		}
		if got := r.Status("claude", sessionUUID); got.Model != want.Model || got.Effort != want.Effort || got.ModelAt != want.ModelAt {
			t.Fatalf("status = %+v, want %+v", got, want)
		}
	}
	add := func(body string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(body); err != nil {
			t.Fatal(err)
		}
	}
	check(Status{Model: "claude-sonnet-5-5", Effort: "high", ModelAt: "2026-09-28T01:00:01Z"})

	add(assistant("2026-09-28T01:00:02Z", "<synthetic>", `"isApiErrorMessage":true,"perTurnEffort":"low",`) + assistant("2026-09-28T01:00:03Z", "<synthetic>", ""))
	check(Status{Model: "claude-sonnet-5-5", Effort: "high", ModelAt: "2026-09-28T01:00:01Z"})

	add(assistant("2026-09-28T01:00:04Z", "claude-haiku-5-5", `"requestedModel":"claude-opus-5-5[1m]","effort":"high","perTurnEffort":"medium",`))
	check(Status{Model: "claude-opus-5-5[1m]", Effort: "medium", ModelAt: "2026-09-28T01:00:04Z"})

	add(assistant("2026-09-28T01:00:05Z", "claude-haiku-5-5", `"effort":"max",`))
	check(Status{Model: "claude-haiku-5-5", Effort: "max", ModelAt: "2026-09-28T01:00:05Z"})

	add(assistant("2026-09-28T01:00:06Z", "claude-haiku-5-5", ""))
	check(Status{Model: "claude-haiku-5-5", Effort: "max", ModelAt: "2026-09-28T01:00:06Z"})

	add(assistant("2026-09-28T01:00:07Z", "claude-opus-5-5", ""))
	check(Status{Model: "claude-opus-5-5", ModelAt: "2026-09-28T01:00:07Z"})

	add(assistant("2026-09-28T01:00:08Z", "claude-haiku-5-5", `"isSidechain":true,"requestedModel":"claude-haiku-5-5","effort":"low",`))
	check(Status{Model: "claude-opus-5-5", ModelAt: "2026-09-28T01:00:07Z"})
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
	export := `{"messages":[` +
		`{"info":{"role":"assistant","finish":"stop","time":{"created":1790570001000},"tokens":{"input":10,"output":5,"cache":{"read":1,"write":1}}},"parts":[{"type":"text","text":"first"}]},` +
		`{"info":{"role":"user","time":{"created":1790570002000}},"parts":[{"type":"compaction"}]},` +
		`{"info":{"role":"assistant","finish":"stop","time":{"created":1790570003000},"tokens":{"input":1000,"output":9,"cache":{"read":5000,"write":200}}},"parts":[{"type":"text","text":"second"}]}]}`
	bin := fakebin.Install(t, t.TempDir(), "opencode", "opencode", map[string]string{"count": count, "export": export})
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
	export := `{"info":{},"messages":[` +
		`{"type":"assistant","time":{"created":1790570001000},"tokens":{"input":175,"output":5,"reasoning":24,"cache":{"read":17024,"write":0}},"content":[{"type":"text","text":"hi"}]},` +
		`{"type":"compaction","time":{"created":1790570002000}},` +
		`{"type":"idle","time":{"created":1790570003000}}]}`
	bin := fakebin.Install(t, t.TempDir(), "opencode", "opencode", map[string]string{"count": count, "export": export})
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

func TestCodexStatusFollowsTheNewestTurnContext(t *testing.T) {
	home := t.TempDir()
	turn := func(model, effort string) string {
		return `{"timestamp":"2026-09-28T01:00:02Z","type":"turn_context","payload":{"model":"` + model + `","effort":"` + effort + `"}}` + "\n"
	}
	path := rollout(t, home, "rollout-2026-09-28T01-00-00-codex-1.jsonl", meta("codex-1")+turn("gpt-5.5", "high"))
	r := &Reader{Paths: Paths{Codex: home}}
	ctx := context.Background()
	if _, err := r.Read(ctx, "codex", "codex-1", "/f"); err != nil {
		t.Fatal(err)
	}
	if s := r.Status("codex", "codex-1"); s.Model != "gpt-5.5" || s.Effort != "high" {
		t.Fatalf("first turn = %+v", s)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(turn("gpt-5.4-mini", "low")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := r.Read(ctx, "codex", "codex-1", "/f"); err != nil {
		t.Fatal(err)
	}
	if s := r.Status("codex", "codex-1"); s.Model != "gpt-5.4-mini" || s.Effort != "low" {
		t.Fatalf("after a switch = %+v", s)
	}
}

func TestCodexStatusKeepsTheEffortOfAnUnchangedModel(t *testing.T) {
	home := t.TempDir()
	turn := func(model, effort string) string {
		return `{"timestamp":"2026-09-28T01:00:02Z","type":"turn_context","payload":{"model":"` + model + `","effort":` + effort + `}}` + "\n"
	}
	rollout(t, home, "rollout-2026-09-28T01-00-00-codex-1.jsonl", meta("codex-1")+turn("gpt-5.5", `"high"`)+turn("gpt-5.5", `null`)+turn("gpt-5.5", `{"x":1}`))
	r := &Reader{Paths: Paths{Codex: home}}
	if _, err := r.Read(context.Background(), "codex", "codex-1", "/f"); err != nil {
		t.Fatal(err)
	}
	if s := r.Status("codex", "codex-1"); s.Model != "gpt-5.5" || s.Effort != "high" || s.ModelAt != "2026-09-28T01:00:02Z" {
		t.Fatalf("status = %+v", s)
	}
	rollout(t, home, "rollout-2026-09-28T01-00-00-codex-2.jsonl", meta("codex-2")+turn("gpt-5.5", `"high"`)+turn("gpt-5.4", `null`))
	if _, err := r.Read(context.Background(), "codex", "codex-2", "/f"); err != nil {
		t.Fatal(err)
	}
	if s := r.Status("codex", "codex-2"); s.Model != "gpt-5.4" || s.Effort != "" {
		t.Fatalf("after a model change status = %+v", s)
	}
}
