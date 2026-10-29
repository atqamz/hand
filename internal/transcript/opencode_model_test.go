package transcript

import (
	"context"
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
