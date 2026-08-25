package supervision

import (
	"fmt"
	"strings"

	"github.com/atqamz/hand/internal/harness"
)

const (
	ClaudeIntegrationVersion   = "claude.stop.async-rewake.v1"
	CodexIntegrationVersion    = "codex.stop.queue.v1"
	OpenCodeIntegrationVersion = "opencode.session-prompt.v1"
	PiIntegrationVersion       = "pi.follow-up-trigger.v1"
	GrokIntegrationVersion     = "grok.background-monitor.v1"
)

// QualificationEvidence is the evidence for one exact host/runtime path. It is
// release metadata, not Fleet workflow state.
type QualificationEvidence struct {
	Host               string
	RuntimeVersion     string
	APIGeneration      string
	Platform           string
	IntegrationVersion string
	Addressability     string
	Capability         string
}

// QualificationRule names one live-qualified host/runtime/platform combination.
// Every non-empty field is an exact match requirement.
type QualificationRule struct {
	Host               string
	RuntimeVersion     string
	APIGeneration      string
	Platform           string
	IntegrationVersion string
	Addressability     string
	Capability         string
	Evidence           string
}

type QualificationResult struct {
	State  string
	Reason string
	Rule   *QualificationRule
}

type CapabilityMatrix struct {
	Rules []QualificationRule
}

func (m CapabilityMatrix) Evaluate(evidence QualificationEvidence) QualificationResult {
	if !IsSupervisorHost(evidence.Host) {
		return QualificationResult{
			State:  CapabilityUnsupported,
			Reason: fmt.Sprintf("host %q has no Supervisor turn-delivery path", evidence.Host),
		}
	}
	if strings.TrimSpace(evidence.RuntimeVersion) == "" && strings.TrimSpace(evidence.APIGeneration) == "" && strings.TrimSpace(evidence.Capability) == "" {
		return QualificationResult{
			State:  CapabilityUnqualified,
			Reason: "runtime version, API generation, or positive capability evidence is unavailable",
		}
	}
	for _, missing := range []struct {
		name  string
		value string
	}{
		{name: "platform", value: evidence.Platform},
		{name: "integration version", value: evidence.IntegrationVersion},
		{name: "addressability capability", value: evidence.Addressability},
	} {
		if strings.TrimSpace(missing.value) == "" {
			return QualificationResult{
				State:  CapabilityUnqualified,
				Reason: fmt.Sprintf("%s evidence is unavailable", missing.name),
			}
		}
	}
	for _, rule := range m.Rules {
		if rule.matches(evidence) {
			qualified := rule
			return QualificationResult{
				State:  CapabilitySupported,
				Reason: fmt.Sprintf("exact runtime path is live-qualified by %s", rule.Evidence),
				Rule:   &qualified,
			}
		}
	}
	return QualificationResult{
		State:  CapabilityUnqualified,
		Reason: fmt.Sprintf("no live qualification record matches host %q runtime %q on %q with integration %q", evidence.Host, evidence.RuntimeVersion, evidence.Platform, evidence.IntegrationVersion),
	}
}

func (r QualificationRule) matches(e QualificationEvidence) bool {
	return r.Host == e.Host &&
		r.RuntimeVersion == e.RuntimeVersion &&
		r.APIGeneration == e.APIGeneration &&
		r.Platform == e.Platform &&
		r.IntegrationVersion == e.IntegrationVersion &&
		r.Addressability == e.Addressability &&
		r.Capability == e.Capability &&
		r.Evidence != ""
}

// SupervisorQualificationMatrix returns release evidence only for exact paths
// already proven without operator input. Other hosts stay unqualified until
// their own live record is added.
func SupervisorQualificationMatrix() CapabilityMatrix {
	return CapabilityMatrix{Rules: []QualificationRule{
		{
			Host:               harness.Claude,
			RuntimeVersion:     "2.1.238",
			Platform:           "linux/amd64",
			IntegrationVersion: ClaudeIntegrationVersion,
			Addressability:     "hook-session",
			Capability:         "claude.async-rewake.v1",
			Evidence:           "docs/qualification/supervision-v0.7.md",
		},
	}}
}

func requiredIntegrationVersion(host string) string {
	switch host {
	case harness.Claude:
		return ClaudeIntegrationVersion
	case harness.Codex:
		return CodexIntegrationVersion
	case harness.OpenCode:
		return OpenCodeIntegrationVersion
	case harness.Pi:
		return PiIntegrationVersion
	case harness.Grok:
		return GrokIntegrationVersion
	default:
		return ""
	}
}
