package harness

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/agydb/agytest"
	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/state"
)

const uuid = "0f8fad5b-d9cb-469f-a165-70867728950e"

func TestSupervisorArgvIsExact(t *testing.T) {
	cases := []struct {
		name    string
		bin     string
		spec    Spec
		session string
		prompt  string
		resume  bool
		want    []string
	}{
		{"claude new", "/bin/claude", Spec{"claude", "sonnet", "medium"}, uuid, "go", false, []string{"/bin/claude", "--dangerously-skip-permissions", "--session-id", uuid, "--model", "sonnet", "--effort", "medium", "go"}},
		{"claude resume", "/bin/claude", Spec{"claude", "sonnet", "medium"}, uuid, "", true, []string{"/bin/claude", "--dangerously-skip-permissions", "--resume", uuid, "--model", "sonnet", "--effort", "medium"}},
		{"claude bare", "/bin/claude", Spec{Harness: "claude"}, uuid, "go", false, []string{"/bin/claude", "--dangerously-skip-permissions", "--session-id", uuid, "go"}},
		{"codex new", "/bin/codex", Spec{"codex", "gpt-6-luna", "low"}, "", "go", false, []string{"/bin/codex", "--dangerously-bypass-approvals-and-sandbox", "--disable", "hooks", "-m", "gpt-6-luna", "-c", "model_reasoning_effort=low", "go"}},
		{"codex resume", "/bin/codex", Spec{"codex", "gpt-6-luna", "low"}, "abc", "", true, []string{"/bin/codex", "resume", "--dangerously-bypass-approvals-and-sandbox", "--disable", "hooks", "-m", "gpt-6-luna", "-c", "model_reasoning_effort=low", "abc"}},
		{"opencode new", "/bin/opencode", Spec{Harness: "opencode"}, "", "go", false, []string{"/bin/opencode", "--standalone", "--auto", "--prompt", "go"}},
		{"opencode resume", "/bin/opencode", Spec{Harness: "opencode"}, "ses_1", "", true, []string{"/bin/opencode", "--standalone", "--auto", "--session", "ses_1"}},
	}
	for _, c := range cases {
		got, err := SupervisorArgv(c.bin, c.spec, c.session, c.prompt, c.resume)
		if err != nil || !slices.Equal(got, c.want) {
			t.Fatalf("%s: argv = %q, %v", c.name, got, err)
		}
	}
	for name, bad := range map[string]func() ([]string, error){
		"resume with a prompt": func() ([]string, error) {
			return SupervisorArgv("/bin/claude", Spec{Harness: "claude"}, uuid, "go", true)
		},
		"resume without session": func() ([]string, error) { return SupervisorArgv("/bin/codex", Spec{Harness: "codex"}, "", "", true) },
		"new without a prompt":   func() ([]string, error) { return SupervisorArgv("/bin/codex", Spec{Harness: "codex"}, "", "", false) },
		"claude session not uuid": func() ([]string, error) {
			return SupervisorArgv("/bin/claude", Spec{Harness: "claude"}, "nope", "go", false)
		},
		"unknown harness": func() ([]string, error) { return SupervisorArgv("/bin/x", Spec{Harness: "gemini"}, "", "go", false) },
	} {
		if _, err := bad(); !errors.Is(err, state.ErrInvalid) {
			t.Fatalf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestNewSessionIDIsAV4UUID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	a, b := NewSessionID(), NewSessionID()
	if !re.MatchString(a) || !re.MatchString(b) || a == b {
		t.Fatalf("ids %q %q", a, b)
	}
}

const marker = "You are supervisor s1 of the Hand fleet "

func rollout(t *testing.T, home string, at time.Time, id, cwd, prompt string) {
	t.Helper()
	dir := filepath.Join(home, "sessions", at.Format("2006"), at.Format("01"), at.Format("02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"timestamp":"` + at.UTC().Format(time.RFC3339Nano) + `","type":"session_meta","payload":{"id":"` + id + `","cwd":"` + cwd + `","timestamp":"` + at.UTC().Format(time.RFC3339Nano) + `"}}` + "\n"
	user := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + prompt + `"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "rollout-"+id+".jsonl"), []byte(line+`{"type":"event_msg"}`+"\n"+user), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCodexSessionFindsTheFirstRolloutForTheFolderSinceLaunch(t *testing.T) {
	home := t.TempDir()
	since := time.Now().Add(-time.Minute)
	rollout(t, home, time.Now(), "elsewhere", "/other", marker+"x.")
	rollout(t, home, since.Add(-time.Hour), "too-old", "/fleet", marker+"x.")
	rollout(t, home, since.Add(time.Second/4), "operator", "/fleet", "Trust this folder for me")
	rollout(t, home, since.Add(time.Second/2), "quoting", "/fleet", "Why did Hand say "+marker+"x.?")
	rollout(t, home, since.Add(time.Second), "mine", "/fleet", "\\n"+marker+"x. Follow AGENTS.md")
	rollout(t, home, since.Add(2*time.Second), "later", "/fleet", marker+"x.")
	if id, err := CodexSession(home, "/fleet", since, marker); err != nil || id != "mine" {
		t.Fatalf("session = %q, %v", id, err)
	}
	if id, err := CodexSession(t.TempDir(), "/fleet", since, marker); err != nil || id != "" {
		t.Fatalf("empty home = %q, %v", id, err)
	}
}

func TestOpencodeSessionParsesTheList(t *testing.T) {
	dir := t.TempDir()
	fixture, err := filepath.Abs(filepath.Join("testdata", "opencode-session-list.json"))
	if err != nil {
		t.Fatal(err)
	}
	exports := map[string]string{
		"list":                           fixture,
		"holders":                        holders(t),
		"ses_newer000000000000000000001": `{"messages":[{"type":"user","text":"\"` + marker + `demo.\""}]}`,
		"ses_older000000000000000000003": `{"messages":[{"type":"user","text":"quote: ` + marker + `demo."},{"type":"assistant","text":"` + marker + `demo."}]}`,
		"other":                          `{"messages":[{"type":"user","text":"hello"}]}`,
	}
	bin := fakebin.Install(t, t.TempDir(), "opencode", "opencode", exports)
	began := time.Now()
	if id, err := OpencodeSession(bin, dir, time.UnixMilli(1790570400000), marker); err != nil || id != "ses_newer000000000000000000001" {
		t.Fatalf("session = %q, %v", id, err)
	}
	if took := time.Since(began); took > 4*time.Second {
		t.Fatalf("a child holding stdout kept the lookup waiting %s", took)
	}
	exports["ses_newer000000000000000000001"] = `{"messages":[{"info":{"role":"user"},"parts":[{"type":"text","text":"` + marker + `demo."}]}]}`
	parts := fakebin.Install(t, t.TempDir(), "opencode", "opencode", exports)
	if id, err := OpencodeSession(parts, dir, time.UnixMilli(1790570400000), marker); err != nil || id != "ses_newer000000000000000000001" {
		t.Fatalf("parts-shaped export: session = %q, %v", id, err)
	}
	if id, err := OpencodeSession(bin, dir, time.UnixMilli(1790570700000), marker); err != nil || id != "" {
		t.Fatalf("nothing newer = %q, %v", id, err)
	}
}

func TestAgySupervisorArgv(t *testing.T) {
	spec := Spec{Harness: "agy", Model: "gemini-3.8-flash-low"}
	if got, err := SupervisorArgv("/bin/agy", spec, "", "You are supervisor s1", false); err != nil || !slices.Equal(got, []string{"/bin/agy", "--model", "gemini-3.8-flash-low", "--dangerously-skip-permissions", "-i", "You are supervisor s1"}) {
		t.Fatalf("new = %q, %v", got, err)
	}
	if got, err := SupervisorArgv("/bin/agy", spec, "c1", "", true); err != nil || !slices.Equal(got, []string{"/bin/agy", "--model", "gemini-3.8-flash-low", "--dangerously-skip-permissions", "--conversation", "c1"}) {
		t.Fatalf("resume = %q, %v", got, err)
	}
	if _, err := SupervisorArgv("/bin/agy", spec, "c1", "You are supervisor s1", false); err == nil || !strings.Contains(err.Error(), "picks its own session id") {
		t.Fatalf("new with a session = %v", err)
	}
}

func TestAgySessionPicksTheLaunchedConversation(t *testing.T) {
	dir, fleet := t.TempDir(), t.TempDir()
	since := time.Now().Add(-time.Minute)
	marker := "You are supervisor s2 of the Hand fleet "
	at := since.Add(time.Second)
	old := agytest.Conversation(t, dir, "old", fleet, 0, 0, agytest.Step{Type: 14, Text: marker + "demo.", At: at})
	if err := os.Chtimes(old, since.Add(-time.Hour), since.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	agytest.Conversation(t, dir, "other", fleet, 0, 0, agytest.Step{Type: 14, Text: "hello", At: at})
	agytest.Conversation(t, dir, "mine", fleet, 0, 0, agytest.Step{Type: 14, Text: marker + "demo.", At: at})
	if id, err := AgySession(dir, fleet, since, marker); err != nil || id != "mine" {
		t.Fatalf("session = %q, %v", id, err)
	}
	elsewhere := t.TempDir()
	agytest.Conversation(t, elsewhere, "mine", "/srv/elsewhere", 0, 0, agytest.Step{Type: 14, Text: marker + "demo.", At: at})
	if id, err := AgySession(elsewhere, fleet, since, marker); err != nil || id != "" {
		t.Fatalf("another folder = %q, %v", id, err)
	}
	if id, err := AgySession(filepath.Join(dir, "absent"), fleet, since, marker); err != nil || id != "" {
		t.Fatalf("no folder = %q, %v", id, err)
	}
}
