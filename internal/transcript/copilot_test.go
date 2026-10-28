package transcript

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func copilotHome(t *testing.T, body string) (string, string) {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "session-state", sessionUUID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, path
}

func copilotFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCopilotTranscriptShowsToolCallsAndTheFinalMessage(t *testing.T) {
	home, _ := copilotHome(t, copilotFixture(t, "copilot-calm.jsonl"))
	r := &Reader{Paths: Paths{Copilot: home}}
	got, err := r.Read(context.Background(), "copilot", sessionUUID, "/fleets/demo")
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Role: "tool", Text: "bash: cat a.txt", At: "2026-10-28T10:07:36.258Z"},
		{Role: "worker", Text: "done", At: "2026-10-28T10:07:37.530Z"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("entries =\n%+v\nwant\n%+v", got, want)
	}
	if st := r.Status("copilot", sessionUUID); st.Selected != "auto" || st.Used != "gpt-5.6-luna" {
		t.Fatalf("status = %+v", st)
	}
}

func TestCopilotStatusKeepsTheModelItWasAskedFor(t *testing.T) {
	home, _ := copilotHome(t, copilotFixture(t, "copilot-substituted.jsonl"))
	r := &Reader{Paths: Paths{Copilot: home}}
	got, err := r.Read(context.Background(), "copilot", sessionUUID, "/fleets/demo")
	if err != nil || len(got) != 1 || got[0].Text != "ok" {
		t.Fatalf("entries = %+v, %v", got, err)
	}
	if st := r.Status("copilot", sessionUUID); st.Selected != "gpt-nosuch" || st.Used != "gpt-5.6-luna" {
		t.Fatalf("status = %+v", st)
	}
}

func TestCopilotSkipsWhatItDoesNotKnow(t *testing.T) {
	body := strings.Join([]string{
		`{"type":"brand.new.kind","data":{"content":["not","a","string"]}}`,
		`{"type":"assistant.message","data":"not an object"}`,
		`{"type":"tool.execution_start","data":{"toolName":"view","arguments":"a string"},"timestamp":"t1"}`,
		`{"type":"tool.execution_start","data":{"toolName":"bash","arguments":{"command":"\n  first\nsecond"}},"timestamp":"t2"}`,
		`not json at all`,
		`{"data":{"content":"typeless"}}`,
		`{"type":"assistant.message","data":{"content":"  ","model":"m"}}`,
		`{"type":"user.message","data":{"content":"brief","turnId":"0"}}`,
		`{"type":"user.message","data":{"content":"no turn"}}`,
		`{"type":"user.message","data":{"content":"keep going","turnId":"3"},"timestamp":"t3"}`,
		`{"type":"assistant.message","data":{"content":"half`,
	}, "\n")
	home, path := copilotHome(t, body)
	r := &Reader{Paths: Paths{Copilot: home}}
	ctx := context.Background()
	got, err := r.Read(ctx, "copilot", sessionUUID, "/f")
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Role: "tool", Text: "view", At: "t1"},
		{Role: "tool", Text: "bash: first", At: "t2"},
		{Role: "operator", Text: "keep going", At: "t3"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("entries =\n%+v\nwant\n%+v", got, want)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(` end"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if got, err = r.Read(ctx, "copilot", sessionUUID, "/f"); err != nil || len(got) != 4 || got[3].Text != "half end" {
		t.Fatalf("after the line ended = %+v, %v", got, err)
	}
}

func TestCopilotClipsALongMessage(t *testing.T) {
	long := strings.Repeat("é", copilotText)
	home, _ := copilotHome(t, `{"type":"assistant.message","data":{"content":"`+long+`"}}`+"\n")
	got, err := (&Reader{Paths: Paths{Copilot: home}}).Read(context.Background(), "copilot", sessionUUID, "/f")
	if err != nil || len(got) != 1 || len(got[0].Text) > copilotText+len("…") || !strings.HasSuffix(got[0].Text, "é…") {
		t.Fatalf("%d entries, err %v", len(got), err)
	}
}

func TestCopilotKeepsTheLatestEntriesOnly(t *testing.T) {
	var b strings.Builder
	for i := range 2*copilotKeep + 5 {
		fmt.Fprintf(&b, `{"type":"assistant.message","data":{"content":"m%d"},"timestamp":"t"}`+"\n", i)
	}
	home, _ := copilotHome(t, b.String())
	got, err := (&Reader{Paths: Paths{Copilot: home}}).Read(context.Background(), "copilot", sessionUUID, "/f")
	if err != nil || len(got) != copilotKeep || got[0].Text != fmt.Sprintf("m%d", copilotKeep+5) || got[copilotKeep-1].Text != fmt.Sprintf("m%d", 2*copilotKeep+4) {
		t.Fatalf("kept %d entries, first %q, err %v", len(got), got[0].Text, err)
	}
}

func TestCopilotWithoutASessionOrHome(t *testing.T) {
	ctx := context.Background()
	if _, err := (&Reader{Paths: Paths{Copilot: t.TempDir()}}).Read(ctx, "copilot", sessionUUID, "/f"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("missing err = %v", err)
	}
	if _, err := (&Reader{}).Read(ctx, "copilot", sessionUUID, "/f"); err == nil || errors.Is(err, ErrNoSession) {
		t.Fatalf("unknown home err = %v", err)
	}
	if _, err := (&Reader{Paths: Paths{Copilot: t.TempDir()}}).Read(ctx, "copilot", "../x", "/f"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("pattern id err = %v", err)
	}
	home, _ := copilotHome(t, `{"foo":1}`+"\n")
	if _, err := (&Reader{Paths: Paths{Copilot: home}}).Read(ctx, "copilot", sessionUUID, "/f"); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("unreadable err = %v", err)
	}
}

func TestCopilotReadsALargeFileWithoutLoadingIt(t *testing.T) {
	const chunk, chunks = 16 << 10, 1536
	home, path := copilotHome(t, "")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := copilotFixture(t, "copilot-calm.jsonl")
	if _, err := f.WriteString(fixture); err != nil {
		t.Fatal(err)
	}
	filler := `{"type":"tool.execution_complete","data":{"result":{"content":"` + strings.Repeat("x", chunk) + `"}}}` + "\n"
	for range chunks {
		if _, err := f.WriteString(filler); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.WriteString(`{"type":"assistant.message","data":{"conte`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	size := int64(len(fixture)) + int64(len(filler))*chunks
	var peak uint64
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
				runtime.GC()
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				peak = max(peak, m.HeapInuse)
			}
		}
	}()
	r := &Reader{Paths: Paths{Copilot: home}}
	got, err := r.Read(context.Background(), "copilot", sessionUUID, "/f")
	close(stop)
	<-done
	if err != nil || len(got) != 2 {
		t.Fatalf("entries = %+v, %v", got, err)
	}
	if off := r.logs["copilot:"+sessionUUID].offsets[path]; off != size {
		t.Fatalf("offset = %d, want %d", off, size)
	}
	if peak > uint64(size)/8 {
		t.Fatalf("heap peaked at %d bytes reading %d", peak, size)
	}
}
