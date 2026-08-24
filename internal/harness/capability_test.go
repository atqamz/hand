package harness

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestExecutableMapsAntigravityToAgy(t *testing.T) {
	if got := Executable(Antigravity); got != "agy" {
		t.Fatalf("Executable(%q) = %q, want agy", Antigravity, got)
	}
	if got := Executable(Claude); got != Claude {
		t.Fatalf("Executable(%q) = %q, want unchanged name", Claude, got)
	}
}

func antigravityProbe(models func(string) ([]string, error)) CapabilityProbe {
	return CapabilityProbe{
		Platform: "linux",
		LookPath: func(string) (string, error) { return "/bin/agy", nil },
		Contract: func(string) error { return nil },
		Models:   models,
	}
}

func TestInspectAntigravityCapabilityStates(t *testing.T) {
	tests := []struct {
		name       string
		probe      CapabilityProbe
		wantState  CapabilityState
		wantReason string
	}{
		{
			name:      "executable missing",
			probe:     CapabilityProbe{Platform: "linux", LookPath: func(string) (string, error) { return "", errors.New("missing") }},
			wantState: CapabilityUnavailable,
		},
		{
			name:       "platform unsupported",
			probe:      CapabilityProbe{Platform: "freebsd"},
			wantState:  CapabilityUnsupported,
			wantReason: "platform",
		},
		{
			name: "headless contract unknown",
			probe: CapabilityProbe{
				Platform: "linux",
				LookPath: func(string) (string, error) { return "/bin/agy", nil },
				Contract: func(string) error { return errors.New("missing stream-json") },
			},
			wantState:  CapabilityUnknown,
			wantReason: "headless worker contract",
		},
		{
			name:       "authentication unavailable",
			probe:      antigravityProbe(func(string) ([]string, error) { return nil, errors.New("authentication required") }),
			wantState:  CapabilityUnavailable,
			wantReason: "authentication/configuration unavailable",
		},
		{
			name:       "unknown capability",
			probe:      antigravityProbe(func(string) ([]string, error) { return nil, errors.New("provider changed output") }),
			wantState:  CapabilityUnknown,
			wantReason: "could not be verified",
		},
		{
			name:      "ready capability",
			probe:     antigravityProbe(func(string) ([]string, error) { return []string{"gemini-3.5-flash-medium"}, nil }),
			wantState: CapabilityReady,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Inspect(Antigravity, test.probe)
			if got.State != test.wantState {
				t.Fatalf("state = %q, want %q (%+v)", got.State, test.wantState, got)
			}
			if test.wantReason != "" && !strings.Contains(got.Reason, test.wantReason) {
				t.Fatalf("reason = %q, want %q", got.Reason, test.wantReason)
			}
		})
	}
}

func TestInspectReadyCapabilityPreservesModels(t *testing.T) {
	models := []string{"gemini-3.5-flash-medium", "claude-sonnet-4-6"}
	for _, platform := range []string{"linux", "darwin", "windows"} {
		t.Run(platform, func(t *testing.T) {
			probe := antigravityProbe(func(string) ([]string, error) { return models, nil })
			probe.Platform = platform
			got := Inspect(Antigravity, probe)
			if got.State != CapabilityReady {
				t.Fatalf("state = %q, want ready", got.State)
			}
			if !reflect.DeepEqual(got.Models, models) {
				t.Fatalf("models = %#v, want %#v", got.Models, models)
			}
		})
	}
}

func TestValidateRuntimeRequiresQualifiedAntigravityContract(t *testing.T) {
	ready := antigravityProbe(func(string) ([]string, error) { return []string{"gemini-3.5-flash-medium"}, nil })
	if err := ValidateRuntimeWithProbe(Antigravity, ready); err != nil {
		t.Fatalf("ready runtime rejected: %v", err)
	}
	unknown := ready
	unknown.Contract = func(string) error { return errors.New("old CLI") }
	if err := ValidateRuntimeWithProbe(Antigravity, unknown); err == nil || !strings.Contains(err.Error(), "capability is unknown") {
		t.Fatalf("old runtime error = %v, want unknown capability", err)
	}
}

func TestValidateAntigravityEffort(t *testing.T) {
	for _, effort := range []string{"low", "medium", "high"} {
		if err := ValidateEffort(Antigravity, effort); err != nil {
			t.Fatalf("ValidateEffort(%q) = %v", effort, err)
		}
	}
	if err := ValidateEffort(Antigravity, "x-high"); err == nil || !strings.Contains(err.Error(), "low, medium, or high") {
		t.Fatalf("ValidateEffort(%q) = %v, want unsupported effort error", "x-high", err)
	}
}

func TestValidateAntigravityModelAgainstProbedCapability(t *testing.T) {
	probe := antigravityProbe(func(string) ([]string, error) { return []string{"gemini-3.5-flash-medium"}, nil })
	if err := ValidateModelWithProbe(Antigravity, "gemini-3.5-flash-medium", probe); err != nil {
		t.Fatalf("supported model rejected: %v", err)
	}
	if err := ValidateModelWithProbe(Antigravity, "does-not-exist", probe); err == nil || !strings.Contains(err.Error(), "does not support model") {
		t.Fatalf("unsupported model error = %v", err)
	}
	unknown := probe
	unknown.Models = func(string) ([]string, error) { return nil, errors.New("unrecognized response") }
	if err := ValidateModelWithProbe(Antigravity, "gemini-3.5-flash-medium", unknown); err == nil || !strings.Contains(err.Error(), "capability is unknown") {
		t.Fatalf("unknown model capability error = %v", err)
	}
}

func TestParseModelsAcceptsDocumentedSlugDisplayRows(t *testing.T) {
	got := parseModels([]byte("gemini-3.7-flash-high     Gemini 3.7 Flash (High)\nclaude-sonnet-4-6         Claude Sonnet 4.6 (Thinking)\n"))
	want := []string{"gemini-3.7-flash-high", "claude-sonnet-4-6"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseModels() = %#v, want %#v", got, want)
	}
}
