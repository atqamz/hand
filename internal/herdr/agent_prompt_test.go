package herdr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
)

func TestAgentPromptContextTargetsExactAgentPaneAndText(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "calls.log")
	bin := faketool.Bin(t)
	faketool.Herdr{
		Responses: []faketool.HerdrResponse{{
			Command: "agent prompt",
			Args:    []string{"wA:pB", "hand worker wake"},
			Stdout:  `{"id":"cli:1","result":{"agent":{"pane_id":"wA:pB","kind":"codex","status":"working"}}}`,
		}},
		Log: callLog,
	}.Install(t, bin)

	if err := NewSessionClient("hand-f-fleet").AgentPromptContext(context.Background(), "wA:pB", "hand worker wake"); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "--session hand-f-fleet agent prompt wA:pB hand worker wake") {
		t.Fatalf("calls = %q, want named session agent prompt", calls)
	}
}

func TestAgentPromptContextPreservesPreSideEffectRejections(t *testing.T) {
	for _, code := range []string{"agent_blocked", "agent_not_found", "agent_not_ready", "empty_agent_prompt"} {
		t.Run(code, func(t *testing.T) {
			writeFakeHerdr(t, faketool.HerdrResponse{
				Command: "agent prompt",
				Stderr:  `{"error":{"code":"` + code + `","message":"rejected"}}`,
				Exit:    1,
			})
			err := NewClient().AgentPromptContext(context.Background(), "wA:pB", "wake")
			if err == nil || !IsAgentPromptPreSideEffectRejection(err) {
				t.Fatalf("AgentPromptContext() = %v, want typed pre-side-effect rejection", err)
			}
		})
	}
}

func TestAgentPromptContextDoesNotClassifyAmbiguousProviderFailure(t *testing.T) {
	writeFakeHerdr(t, faketool.HerdrResponse{
		Command: "agent prompt",
		Stderr:  `{"error":{"code":"agent_prompt_failed","message":"pty actor closed"}}`,
		Exit:    1,
	})
	err := NewClient().AgentPromptContext(context.Background(), "wA:pB", "wake")
	if err == nil || IsAgentPromptPreSideEffectRejection(err) {
		t.Fatalf("AgentPromptContext() = %v, want ambiguous provider failure", err)
	}
}

func TestAgentPromptContextReportsProcessNotStarted(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := NewClient().AgentPromptContext(context.Background(), "wA:pB", "wake")
	if err == nil || !IsProcessNotStarted(err) {
		t.Fatalf("AgentPromptContext() = %v, want process-not-started evidence", err)
	}
	var execErr *ExecError
	if !errors.As(err, &execErr) || execErr.Started {
		t.Fatalf("AgentPromptContext() = %v, want Started=false", err)
	}
}
