package harness

import (
	"context"
	"encoding/json"
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

func spacedDirs(t *testing.T) []string {
	t.Helper()
	base := t.TempDir()
	return []string{filepath.Join(base, "fleet"), filepath.Join(base, "my fleet"), filepath.Join(base, "flotte café")}
}

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
		{"codex new", "/bin/codex", Spec{"codex", "gpt-6-luna", "low"}, "", "go", false, []string{"/bin/codex", "--dangerously-bypass-approvals-and-sandbox", "--disable", "hooks", "--cd", "DIR", "-m", "gpt-6-luna", "-c", "model_reasoning_effort=low", "go"}},
		{"codex resume", "/bin/codex", Spec{"codex", "gpt-6-luna", "low"}, "abc", "", true, []string{"/bin/codex", "resume", "--dangerously-bypass-approvals-and-sandbox", "--disable", "hooks", "--cd", "DIR", "-m", "gpt-6-luna", "-c", "model_reasoning_effort=low", "abc"}},
		{"opencode new", "/bin/opencode", Spec{Harness: "opencode"}, opencodeSession, "go", false, []string{"/bin/opencode", "--standalone", "--auto", "--session", opencodeSession, "--prompt", "go"}},
		{"opencode resume", "/bin/opencode", Spec{Harness: "opencode"}, "ses_1", "", true, []string{"/bin/opencode", "--standalone", "--auto", "--session", "ses_1"}},
	}
	for _, dir := range spacedDirs(t) {
		for _, c := range cases {
			got, err := SupervisorArgv(c.bin, c.spec, c.session, c.prompt, dir, c.resume)
			want := slices.Clone(c.want)
			for i := range want {
				if want[i] == "DIR" {
					want[i] = dir
				}
			}
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("%s in %q: argv = %q, %v", c.name, dir, got, err)
			}
		}
	}
	for name, bad := range map[string]func() ([]string, error){
		"resume with a prompt": func() ([]string, error) {
			return SupervisorArgv("/bin/claude", Spec{Harness: "claude"}, uuid, "go", "", true)
		},
		"resume without session": func() ([]string, error) {
			return SupervisorArgv("/bin/codex", Spec{Harness: "codex"}, "", "", "/fleet", true)
		},
		"new without a prompt": func() ([]string, error) {
			return SupervisorArgv("/bin/codex", Spec{Harness: "codex"}, "", "", "/fleet", false)
		},
		"claude session not uuid": func() ([]string, error) {
			return SupervisorArgv("/bin/claude", Spec{Harness: "claude"}, "nope", "go", "", false)
		},
		"opencode without a session": func() ([]string, error) {
			return SupervisorArgv("/bin/opencode", Spec{Harness: "opencode"}, "", "go", "", false)
		},
		"opencode session not ours": func() ([]string, error) {
			return SupervisorArgv("/bin/opencode", Spec{Harness: "opencode"}, "ses_other", "go", "", false)
		},
		"codex with a session": func() ([]string, error) {
			return SupervisorArgv("/bin/codex", Spec{Harness: "codex"}, "abc", "go", "/fleet", false)
		},
		"codex relative dir": func() ([]string, error) {
			return SupervisorArgv("/bin/codex", Spec{Harness: "codex"}, "", "go", "fleet", false)
		},
		"unknown harness": func() ([]string, error) {
			return SupervisorArgv("/bin/x", Spec{Harness: "gemini"}, "", "go", "", false)
		},
	} {
		if _, err := bad(); !errors.Is(err, state.ErrInvalid) {
			t.Fatalf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

const opencodeSession = "ses_hand0123456789abcdef0123456789"

func TestNewOpencodeSessionIDIsAHandSessionID(t *testing.T) {
	a, b := NewOpencodeSessionID(), NewOpencodeSessionID()
	if !opencodeID.MatchString(a) || !opencodeID.MatchString(b) || a == b || !sessionPattern.MatchString(a) {
		t.Fatalf("ids %q %q", a, b)
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
	quoted, err := json.Marshal(cwd)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"timestamp":"` + at.UTC().Format(time.RFC3339Nano) + `","type":"session_meta","payload":{"id":"` + id + `","cwd":` + string(quoted) + `,"timestamp":"` + at.UTC().Format(time.RFC3339Nano) + `"}}` + "\n"
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

func TestCodexSessionFindsASessionStartedWithCd(t *testing.T) {
	for _, dir := range spacedDirs(t) {
		home := t.TempDir()
		since := time.Now().Add(-time.Minute)
		rollout(t, home, since.Add(time.Second), "mine", dir, "\\n"+marker+"x. Follow AGENTS.md")
		if id, err := CodexSession(home, dir, since, marker); err != nil || id != "mine" {
			t.Fatalf("session in %q = %q, %v", dir, id, err)
		}
		if id, err := CodexSession(home, filepath.Join(dir, "elsewhere"), since, marker); err != nil || id != "" {
			t.Fatalf("session for another folder = %q, %v", id, err)
		}
	}
}

func TestAgySupervisorArgv(t *testing.T) {
	spec := Spec{Harness: "agy", Model: "gemini-3.8-flash-low"}
	if got, err := SupervisorArgv("/bin/agy", spec, "", "You are supervisor s1", "", false); err != nil || !slices.Equal(got, []string{"/bin/agy", "--model", "gemini-3.8-flash-low", "--dangerously-skip-permissions", "-i", "You are supervisor s1"}) {
		t.Fatalf("new = %q, %v", got, err)
	}
	if got, err := SupervisorArgv("/bin/agy", spec, "c1", "", "", true); err != nil || !slices.Equal(got, []string{"/bin/agy", "--model", "gemini-3.8-flash-low", "--dangerously-skip-permissions", "--conversation", "c1"}) {
		t.Fatalf("resume = %q, %v", got, err)
	}
	if _, err := SupervisorArgv("/bin/agy", spec, "c1", "You are supervisor s1", "", false); err == nil || !strings.Contains(err.Error(), "picks its own session id") {
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

func TestOpencodeReturnsItsOutputWhileAChildHoldsStdout(t *testing.T) {
	bin := fakebin.Install(t, t.TempDir(), "opencode", "lister", map[string]string{"stdout": `{"messages":[]}`, "hold": "1", "holders": holders(t)})
	began := time.Now()
	out, err := Opencode(context.Background(), bin, t.TempDir(), "session", "export")
	if err != nil || string(out) != `{"messages":[]}` {
		t.Fatalf("output = %q, %v", out, err)
	}
	if took := time.Since(began); took > 4*time.Second {
		t.Fatalf("a child holding stdout kept the call waiting %s", took)
	}
}
