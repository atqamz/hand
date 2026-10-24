package e2e_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/e2e/mockllm"
)

const wakeHeader = "[hand v1 wake]"

func TestClaudeSupervisor(t *testing.T) { runSupervisor(t, claudeKit, false) }

func TestClaudeSupervisorWake(t *testing.T) { runSupervisor(t, claudeKit, true) }

func TestCodexSupervisor(t *testing.T) { runSupervisor(t, codexKit, false) }

func TestCodexSupervisorWake(t *testing.T) { runSupervisor(t, codexKit, true) }

func runSupervisor(t *testing.T, k kit, wake bool) {
	sb := newSandbox(t)
	orient := k.shell(handBin, "orient")
	sb.startMock(mockllm.Step{Shell: orient}, mockllm.Step{Text: "Oriented."}, mockllm.Step{Shell: orient}, mockllm.Step{Text: "Wake handled."})
	k.seed(sb, sb.mock.URL)
	sb.init()
	routing, err := json.Marshal(map[string]any{"profiles": map[string]any{"e2e": map[string]string{"harness": k.name, "model": k.model, "effort": k.effort}}})
	if err != nil {
		t.Fatal(err)
	}
	sb.write(filepath.Join(sb.fleet, "routing.json"), string(routing))
	sb.ok("project", "add", "app", sb.repo("app"))
	sb.ok("task", "add", "--goal", "Pick a colour", "app", "Colour")
	sb.ok("task", "start", "t1")

	start := sb.ok("supervisor", "start", "--profile", "e2e")
	if field(start, "status") != "running" {
		t.Fatalf("supervisor start:\n%s", start)
	}
	switch trust := field(start, "trust"); {
	case k.name == "claude" && trust != "accepted":
		t.Fatalf("trust = %q, want accepted\n%s", trust, start)
	case k.name == "codex" && trust != "accepted" && trust != "not asked":
		t.Fatalf("trust = %q, want accepted or not asked\n%s", trust, start)
	}

	home := evalLink(sb.fleet)
	var first mockllm.Request
	sb.until("the supervisor's first model request", 90*time.Second, func() bool {
		var ok bool
		first, ok = sb.turn(0)
		return ok
	})
	if got := k.cwd(first.Strings()); !sameDir(got, home) {
		t.Fatalf("the supervisor ran in %q, not in the fleet home %q", got, home)
	}
	sentence := "Your fleet home is `" + home + "`"
	var prompt string
	for _, text := range first.UserTexts() {
		if strings.Contains(text, sentence) {
			prompt = text
		}
	}
	if prompt == "" {
		t.Fatalf("no user message holds %q; messages: %q", sentence, clip(first.UserTexts()))
	}

	var second mockllm.Request
	sb.until("the supervisor's second model request", 60*time.Second, func() bool {
		var ok bool
		second, ok = sb.turn(1)
		return ok
	})
	outs := second.ToolOutputs()
	if len(outs) != 1 || !strings.Contains(outs[0], "supervisor: s1 "+k.name+" running") || strings.Contains(outs[0], "not inside a fleet home") {
		t.Fatalf("hand orient from the supervisor's tool call printed %q", outs)
	}

	if !wake {
		sb.ok("supervisor", "stop")
		return
	}
	sb.ok("decision", "ask", "t1", "Which colour?")
	sb.ok("decision", "answer", "--by", "e2e", "d1", "blue")
	var digest string
	sb.until("the wake to reach the model", 120*time.Second, func() bool {
		for _, r := range sb.mainRequests() {
			for _, text := range r.UserTexts() {
				if strings.HasPrefix(text, wakeHeader) {
					digest = text
				}
			}
		}
		return digest != ""
	})
	if !strings.Contains(digest, "decision.answered") {
		t.Fatalf("the wake does not name decision.answered:\n%s", digest)
	}
	var last mockllm.Request
	sb.until("the supervisor's answer to the wake", 60*time.Second, func() bool {
		var ok bool
		last, ok = sb.turn(3)
		return ok
	})
	if outs := last.ToolOutputs(); len(outs) != 2 || strings.Contains(outs[1], "not inside a fleet home") {
		t.Fatalf("hand orient after the wake printed %q", outs)
	}
	sb.until("the delivery of the wake", 30*time.Second, func() bool {
		for _, e := range sb.events() {
			if e.Kind == "supervisor.delivered" {
				return true
			}
		}
		return false
	})
	sb.ok("supervisor", "stop")
}
