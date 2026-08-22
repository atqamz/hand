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

func TestIsOneShot(t *testing.T) {
	if !IsOneShot(Antigravity) {
		t.Fatal("IsOneShot(antigravity) = false, want true")
	}
	if IsOneShot(Claude) {
		t.Fatal("IsOneShot(claude) = true, want false")
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
			name: "authentication unavailable",
			probe: CapabilityProbe{
				Platform: "linux",
				LookPath: func(string) (string, error) { return "/bin/agy", nil },
				Models:   func(string) ([]string, error) { return nil, errors.New("authentication required") },
			},
			wantState:  CapabilityUnavailable,
			wantReason: "authentication/configuration unavailable",
		},
		{
			name: "unknown capability",
			probe: CapabilityProbe{
				Platform: "linux",
				LookPath: func(string) (string, error) { return "/bin/agy", nil },
				Models:   func(string) ([]string, error) { return nil, errors.New("provider changed output") },
			},
			wantState:  CapabilityUnknown,
			wantReason: "could not be verified",
		},
		{
			name: "ready capability",
			probe: CapabilityProbe{
				Platform: "linux",
				LookPath: func(string) (string, error) { return "/bin/agy", nil },
				Models:   func(string) ([]string, error) { return []string{"gemini-3.5-flash-medium"}, nil },
			},
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
			got := Inspect(Antigravity, CapabilityProbe{
				Platform: platform,
				LookPath: func(string) (string, error) { return "/bin/agy", nil },
				Models:   func(string) ([]string, error) { return models, nil },
			})
			if got.State != CapabilityReady {
				t.Fatalf("state = %q, want ready", got.State)
			}
			if !reflect.DeepEqual(got.Models, models) {
				t.Fatalf("models = %#v, want %#v", got.Models, models)
			}
		})
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
	probe := CapabilityProbe{
		Platform: "linux",
		LookPath: func(string) (string, error) { return "/bin/agy", nil },
		Models:   func(string) ([]string, error) { return []string{"gemini-3.5-flash-medium"}, nil },
	}
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

func TestAntigravitySupervisorSupportIsExplicitlyUnsupported(t *testing.T) {
	if SupportsSupervisor(Antigravity) {
		t.Fatal("SupportsSupervisor(antigravity) = true, want unsupported")
	}
	if !SupportsSupervisor(Claude) {
		t.Fatal("SupportsSupervisor(claude) = false, want supported")
	}
}
