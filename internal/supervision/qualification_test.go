package supervision

import (
	"context"
	"testing"

	"github.com/atqamz/hand/internal/harness"
)

func TestCapabilityMatrixRequiresEveryExactDimension(t *testing.T) {
	matrix := CapabilityMatrix{Rules: []QualificationRule{{
		Host:               harness.Claude,
		RuntimeVersion:     "2.1.238",
		APIGeneration:      "claude.stop.async-rewake.v1",
		Platform:           "linux/amd64",
		IntegrationVersion: "claude.stop.async-rewake.v1",
		Addressability:     "stop-hook-session",
		Capability:         "claude.async-rewake.v1",
		Evidence:           "live episodes",
	}}}
	base := QualificationEvidence{
		Host:               harness.Claude,
		RuntimeVersion:     "2.1.238",
		APIGeneration:      "claude.stop.async-rewake.v1",
		Platform:           "linux/amd64",
		IntegrationVersion: "claude.stop.async-rewake.v1",
		Addressability:     "stop-hook-session",
		Capability:         "claude.async-rewake.v1",
	}

	if got := matrix.Evaluate(base).State; got != CapabilitySupported {
		t.Fatalf("complete evidence state = %q, want %q", got, CapabilitySupported)
	}
	for name, change := range map[string]func(*QualificationEvidence){
		"wrong runtime":        func(e *QualificationEvidence) { e.RuntimeVersion = "2.1.237" },
		"wrong platform":       func(e *QualificationEvidence) { e.Platform = "darwin/arm64" },
		"wrong integration":    func(e *QualificationEvidence) { e.IntegrationVersion = "claude.stop.async-rewake.v0" },
		"wrong addressability": func(e *QualificationEvidence) { e.Addressability = "unknown" },
		"wrong capability":     func(e *QualificationEvidence) { e.Capability = "" },
	} {
		t.Run(name, func(t *testing.T) {
			evidence := base
			change(&evidence)
			result := matrix.Evaluate(evidence)
			if result.State == CapabilitySupported {
				t.Fatalf("evidence %+v overclaimed support", evidence)
			}
			if result.Reason == "" {
				t.Fatal("failed qualification has no reason")
			}
		})
	}
}

func TestCapabilityMatrixDoesNotUseHostNameAlone(t *testing.T) {
	result := SupervisorQualificationMatrix().Evaluate(QualificationEvidence{Host: harness.Claude})
	if result.State == CapabilitySupported {
		t.Fatal("host name alone overclaimed support")
	}
	if result.Reason == "" {
		t.Fatal("unqualified host has no reason")
	}
}

func TestClaudeQualificationRuleCarriesObservedEvidence(t *testing.T) {
	rules := SupervisorQualificationMatrix().Rules
	var found bool
	for _, rule := range rules {
		if rule.Host != harness.Claude {
			continue
		}
		found = true
		if rule.Evidence == "" || rule.RuntimeVersion == "" || rule.Platform == "" || rule.IntegrationVersion == "" {
			t.Fatalf("Claude rule lacks evidence-bearing dimensions: %+v", rule)
		}
	}
	if !found {
		t.Fatal("qualification matrix has no Claude rule")
	}
}

func TestIntegrationStatusReportsQualificationDimensions(t *testing.T) {
	home := t.TempDir()
	exe := "/opt/bin/hand"
	if _, err := InstallClaudeStopHook(home, exe); err != nil {
		t.Fatal(err)
	}
	status, err := IntegrationStatus(context.Background(), StatusInput{
		Home: home,
		Detection: harness.Detection{
			Name:           harness.Claude,
			Source:         "override",
			RuntimeVersion: "2.1.238",
			Platform:       "linux/amd64",
		},
		Exe: exe,
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.RuntimeVersion != "2.1.238" || status.RuntimePlatform != "linux/amd64" {
		t.Fatalf("runtime evidence = %#v, want exact version/platform", status)
	}
	if status.IntegrationVersion != "claude.stop.async-rewake.v1" {
		t.Fatalf("integration version = %q, want claude.stop.async-rewake.v1", status.IntegrationVersion)
	}
	if status.Addressability != "hook-session" {
		t.Fatalf("addressability = %q, want hook-session", status.Addressability)
	}
	if status.Qualification != CapabilitySupported || status.WakeDelivery != CapabilitySupported {
		t.Fatalf("qualification = %q (%s), wake = %q (%s), want supported", status.Qualification, status.QualificationReason, status.WakeDelivery, status.WakeDeliveryReason)
	}
}
