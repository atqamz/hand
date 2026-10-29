package transcript

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
)

func opencodeModel(t *testing.T, fixture, from, to string) (string, string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	export := strings.ReplaceAll(strings.TrimSpace(string(b)), from, to)
	bin := fakebin.Install(t, t.TempDir(), "opencode", "opencode", map[string]string{"count": filepath.Join(t.TempDir(), "count"), "export": export})
	r := &Reader{Paths: Paths{Opencode: bin, OpencodeData: t.TempDir()}}
	if _, err := r.Read(context.Background(), "opencode", "ses_1", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	s := r.Status("opencode", "ses_1")
	return s.Model, s.Effort
}

func TestOpencodeModelComesFromTheAssistantMessage(t *testing.T) {
	model, effort := opencodeModel(t, "opencode-assistant.json", "", "")
	if model != "anthropic/claude-sonnet-5-5" || effort != "" {
		t.Fatalf("model = %q, effort = %q", model, effort)
	}
}

func TestOpencodeModelShowsAVariant(t *testing.T) {
	model, effort := opencodeModel(t, "opencode-assistant.json", `"variant":"default"`, `"variant":"high"`)
	if model != "anthropic/claude-sonnet-5-5" || effort != "high" {
		t.Fatalf("model = %q, effort = %q", model, effort)
	}
}

func TestOpencodeModelFollowsTheNewestAssistantMessage(t *testing.T) {
	const last = `"id":"msg_186a33413001km9KIo33YqM5VC","type":"assistant","time":{"created":1793260205093,"streamed":1793260206868,"completed":1793260208382},"model":{"id":"claude-sonnet-5-5","providerID":"anthropic","variant":"default"}`
	model, effort := opencodeModel(t, "opencode-assistant.json", last, strings.Replace(strings.Replace(last, "claude-sonnet-5-5", "claude-opus-5-5", 1), `"default"`, `"max"`, 1))
	if model != "anthropic/claude-opus-5-5" || effort != "max" {
		t.Fatalf("model = %q, effort = %q", model, effort)
	}
}

func TestOpencodeModelFallsBackToTheSessionModel(t *testing.T) {
	model, effort := opencodeModel(t, "opencode-info-model.json", "", "")
	if model != "anthropic/claude-sonnet-5-5" || effort != "" {
		t.Fatalf("model = %q, effort = %q", model, effort)
	}
}

func TestOpencodeModelStaysEmptyWithoutAny(t *testing.T) {
	model, effort := opencodeModel(t, "opencode-info-model.json", `"model":{"id":"claude-sonnet-5-5","providerID":"anthropic","variant":"default"}`, `"model":null`)
	if model != "" || effort != "" {
		t.Fatalf("model = %q, effort = %q", model, effort)
	}
}

func TestOpencodeModelIgnoresAnAssistantWithoutAModelID(t *testing.T) {
	const last = `"id":"msg_186a33413001km9KIo33YqM5VC","type":"assistant","time":{"created":1793260205093,"streamed":1793260206868,"completed":1793260208382},"model":{"id":"claude-sonnet-5-5","providerID":"anthropic","variant":"default"}`
	model, _ := opencodeModel(t, "opencode-assistant.json", last, strings.Replace(last, `"model":{"id":"claude-sonnet-5-5","providerID":"anthropic","variant":"default"}`, `"model":{}`, 1))
	if model != "anthropic/claude-sonnet-5-5" {
		t.Fatalf("model = %q", model)
	}
}

func TestOpencodeModelFollowsAModelSwitchWithoutAReply(t *testing.T) {
	const tail = `"content":[{"type":"text","text":"Done."}]}]}`
	const switched = `"content":[{"type":"text","text":"Done."}]},{"id":"msg_1","type":"model-switched","time":{"created":1793260300000},"model":{"id":"gpt-5","providerID":"openai","variant":"max"},"previous":{"id":"claude-sonnet-5-5","providerID":"anthropic","variant":"default"}}]}`
	model, effort := opencodeModel(t, "opencode-assistant.json", tail, switched)
	if model != "openai/gpt-5" || effort != "max" {
		t.Fatalf("model = %q, effort = %q", model, effort)
	}
}

func TestOpencodeModelOfAnotherTypeDoesNotBlankTheTranscript(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "opencode-assistant.json"))
	if err != nil {
		t.Fatal(err)
	}
	export := strings.ReplaceAll(strings.TrimSpace(string(b)), `"model":{"id":"claude-sonnet-5-5","providerID":"anthropic","variant":"default"}`, `"model":"claude-sonnet-5-5"`)
	bin := fakebin.Install(t, t.TempDir(), "opencode", "opencode", map[string]string{"export": export})
	r := &Reader{Paths: Paths{Opencode: bin, OpencodeData: t.TempDir()}}
	if _, err := r.Read(context.Background(), "opencode", "ses_1", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if s := r.Status("opencode", "ses_1"); s.Model != "" || s.Context != 79047 {
		t.Fatalf("model = %q, context = %d", s.Model, s.Context)
	}
}

func TestOpencodeModelsReadsTheSessionList(t *testing.T) {
	list := `{"data":[{"id":"ses_1","model":{"id":"gpt-5","providerID":"openai","variant":"high"}},{"id":"ses_2","model":{"id":"claude-sonnet-5-5","providerID":"anthropic","variant":"default"}},{"id":"ses_3","model":null},{"id":"ses_4","model":"x"},{"model":{"id":"y"}}],"cursor":{}}`
	bin := fakebin.Install(t, t.TempDir(), "opencode", "opencode", map[string]string{"args": "api --standalone GET /api/session?limit=1000", "export": list})
	got, err := OpencodeModels(context.Background(), bin, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]ModelLabel{"ses_1": {"openai/gpt-5", "high"}, "ses_2": {"anthropic/claude-sonnet-5-5", ""}, "ses_3": {}, "ses_4": {}}
	if !maps.Equal(got, want) {
		t.Fatalf("models = %+v", got)
	}
}

func TestOpencodeModelsRefusesAnUnexpectedShape(t *testing.T) {
	for _, out := range []string{"not json", `{"cursor":{}}`, `{"data":"x"}`, `[]`} {
		bin := fakebin.Install(t, t.TempDir(), "opencode", "opencode", map[string]string{"export": out})
		if got, err := OpencodeModels(context.Background(), bin, t.TempDir()); err == nil {
			t.Fatalf("%s: models = %+v", out, got)
		}
	}
}
