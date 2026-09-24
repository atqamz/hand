package routing

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const validWorkerPolicy = `{"schema":"hand.worker-policy.v1","profiles":[{"name":"worker","harness":"codex"}],"routes":[{"intent":"execute","judgment":"substantial","profile":"worker"},{"intent":"explore","judgment":"bounded","profile":"worker"},{"intent":"execute","judgment":"mechanical","profile":"worker"},{"intent":"explore","judgment":"substantial","profile":"worker"},{"intent":"execute","judgment":"bounded","profile":"worker"},{"intent":"explore","judgment":"mechanical","profile":"worker"}]}`

func writeWorkerPolicy(t *testing.T, home, data string) string {
	t.Helper()
	path := filepath.Join(home, "config", "worker-policy.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadWorkerPolicyOrdersAllSixRoutesAndWitnessesExactBytes(t *testing.T) {
	home := t.TempDir()
	path := writeWorkerPolicy(t, home, validWorkerPolicy)
	first, err := LoadWorkerPolicy(home)
	if err != nil {
		t.Fatal(err)
	}
	want := []WorkerRoute{
		{Intent: "explore", Judgment: "mechanical", Profile: "worker"},
		{Intent: "explore", Judgment: "bounded", Profile: "worker"},
		{Intent: "explore", Judgment: "substantial", Profile: "worker"},
		{Intent: "execute", Judgment: "mechanical", Profile: "worker"},
		{Intent: "execute", Judgment: "bounded", Profile: "worker"},
		{Intent: "execute", Judgment: "substantial", Profile: "worker"},
	}
	if !slices.Equal(first.Routes, want) {
		t.Fatalf("Worker Routes = %#v, want %#v", first.Routes, want)
	}
	if digest := sha256.Sum256([]byte(validWorkerPolicy)); first.Witness != fmt.Sprintf("sha256:%x", digest) {
		t.Fatalf("Worker policy witness = %q", first.Witness)
	}
	if err := os.WriteFile(path, []byte(validWorkerPolicy+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := LoadWorkerPolicy(home)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(second.Routes, want) || second.Witness == first.Witness {
		t.Fatalf("Worker policy edit did not update exact witness: first=%q second=%q", first.Witness, second.Witness)
	}
}

func TestLoadWorkerPolicyRejectsAmbiguousAndLegacyFields(t *testing.T) {
	for name, data := range map[string]string{
		"duplicate schema":          strings.Replace(validWorkerPolicy, `"schema":"hand.worker-policy.v1"`, `"schema":"hand.worker-policy.v1","schema":"hand.worker-policy.v1"`, 1),
		"duplicate profile field":   strings.Replace(validWorkerPolicy, `"harness":"codex"`, `"harness":"codex","harness":"codex"`, 1),
		"duplicate route field":     strings.Replace(validWorkerPolicy, `"intent":"execute"`, `"intent":"execute","intent":"execute"`, 1),
		"wrong case":                strings.Replace(validWorkerPolicy, `"schema"`, `"Schema"`, 1),
		"newer schema":              strings.Replace(validWorkerPolicy, `hand.worker-policy.v1`, `hand.worker-policy.v2`, 1),
		"legacy Task kind":          strings.Replace(validWorkerPolicy, `"intent":"execute"`, `"kind":"ship","intent":"execute"`, 1),
		"unknown worktree provider": strings.Replace(validWorkerPolicy, `"profiles":`, `"worktree_provider":"treehouse","profiles":`, 1),
		"missing route":             strings.Replace(validWorkerPolicy, `,{"intent":"explore","judgment":"mechanical","profile":"worker"}`, ``, 1),
		"dangling profile":          strings.Replace(validWorkerPolicy, `"profile":"worker"`, `"profile":"missing"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeWorkerPolicy(t, home, data)
			if got, err := LoadWorkerPolicy(home); err == nil {
				t.Fatalf("ambiguous or unsupported Worker policy accepted: %#v", got)
			}
		})
	}
}

func TestLoadWorkerPolicyRejectsUnsafeUnusedProfiles(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  string
	}{
		{"bidi name", `{"name":"unused\u202e","harness":"codex"}`, "name"},
		{"bidi model", `{"name":"unused","harness":"codex","model":"\u202e"}`, "model"},
		{"bidi effort", `{"name":"unused","harness":"codex","effort":"\u202e"}`, "effort"},
		{"oversized model", `{"name":"unused","harness":"codex","model":"` + strings.Repeat("x", 513) + `"}`, "model"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			policy := strings.Replace(validWorkerPolicy, `{"name":"worker","harness":"codex"}`, `{"name":"worker","harness":"codex"},`+test.value, 1)
			writeWorkerPolicy(t, home, policy)
			if _, err := LoadWorkerPolicy(home); err == nil || !strings.Contains(err.Error(), "invalid worker profile "+test.want+" value") {
				t.Fatalf("unsafe unused profile accepted or lacked field diagnosis: %v", err)
			}
		})
	}
}

func TestLoadWorkerPolicyNeverUsesLegacyTaskRoutes(t *testing.T) {
	home := t.TempDir()
	if err := WriteProfile(home, Profile{Name: "daily", Harness: "codex"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteRoute(home, Route{Kind: TaskKindScout, ExecutionClass: ExecutionClassMechanical, Profile: "daily"}); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadWorkerPolicy(home); err == nil {
		t.Fatalf("legacy Task route became canonical Worker policy: %#v", got)
	}
}

func TestResolveWorkerCandidateUsesSixRoutesAndExactOverrides(t *testing.T) {
	home := t.TempDir()
	data := strings.Replace(validWorkerPolicy, `{"name":"worker","harness":"codex"}`, `{"name":"worker","harness":"codex","model":"gpt-6","effort":"high"},{"name":"alternate","harness":"claude","model":"sonnet","effort":"medium"}`, 1)
	writeWorkerPolicy(t, home, data)
	policy, err := LoadWorkerPolicy(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range policy.Routes {
		candidate, err := ResolveWorkerCandidate(home, route.Intent, route.Judgment, WorkerCandidateOverrides{})
		if err != nil {
			t.Fatal(err)
		}
		if candidate.Profile != (Profile{Name: "worker", Harness: "codex", Model: "gpt-6", Effort: "high"}) || candidate.PolicyWitness != policy.Witness {
			t.Fatalf("%s.%s candidate = %#v", route.Intent, route.Judgment, candidate)
		}
	}
	selected, cleared := "alternate", ""
	candidate, err := ResolveWorkerCandidate(home, "execute", "bounded", WorkerCandidateOverrides{
		ProfileOverride: &selected, ModelOverride: &cleared, EffortOverride: &cleared,
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Profile != (Profile{Name: "alternate", Harness: "claude"}) {
		t.Fatalf("explicitly cleared model and effort = %#v", candidate)
	}
	harness := "codex"
	candidate, err = ResolveWorkerCandidate(home, "explore", "mechanical", WorkerCandidateOverrides{
		ProfileOverride: &selected, HarnessOverride: &harness, ModelOverride: &cleared,
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Profile.Name != "alternate" || candidate.Profile.Harness != "codex" || candidate.Profile.Model != "" || candidate.Profile.Effort != "medium" {
		t.Fatalf("field-specific override = %#v", candidate)
	}
	changed := strings.Replace(data, `"intent":"execute","judgment":"bounded","profile":"worker"`, `"intent":"execute","judgment":"bounded","profile":"alternate"`, 1)
	writeWorkerPolicy(t, home, changed)
	next, err := ResolveWorkerCandidate(home, "execute", "bounded", WorkerCandidateOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if next.Profile != (Profile{Name: "alternate", Harness: "claude", Model: "sonnet", Effort: "medium"}) || next.PolicyWitness == policy.Witness {
		t.Fatalf("edited route candidate = %#v", next)
	}
}

func TestResolveWorkerCandidateRejectsUnknownAndStaticConflicts(t *testing.T) {
	home := t.TempDir()
	data := strings.Replace(validWorkerPolicy, `{"name":"worker","harness":"codex"}`, `{"name":"worker","harness":"codex","model":"gpt-6"}`, 1)
	writeWorkerPolicy(t, home, data)
	missing, pi, empty := "missing", "pi", ""
	for _, test := range []struct {
		name      string
		intent    string
		judgment  string
		overrides WorkerCandidateOverrides
		want      string
	}{
		{name: "unknown route", intent: "ship", judgment: "bounded", want: "invalid Worker Route"},
		{name: "missing profile override", intent: "execute", judgment: "bounded", overrides: WorkerCandidateOverrides{ProfileOverride: &missing}, want: "missing profile"},
		{name: "empty profile override", intent: "execute", judgment: "bounded", overrides: WorkerCandidateOverrides{ProfileOverride: &empty}, want: "invalid profile override"},
		{name: "inherited model unsupported by harness override", intent: "execute", judgment: "bounded", overrides: WorkerCandidateOverrides{HarnessOverride: &pi}, want: "takes no model"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ResolveWorkerCandidate(home, test.intent, test.judgment, test.overrides); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("candidate error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadWorkerPolicyRefusesUnrenderableSelectedValues(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
		want string
	}{
		{"NUL model", strings.Replace(validWorkerPolicy, `"harness":"codex"`, `"harness":"codex","model":"\u0000"`, 1), "invalid worker profile model value"},
		{"bidi model", strings.Replace(validWorkerPolicy, `"harness":"codex"`, `"harness":"codex","model":"\u202e"`, 1), "invalid worker profile model value"},
		{"bidi effort", strings.Replace(validWorkerPolicy, `"harness":"codex"`, `"harness":"codex","effort":"\u202e"`, 1), "invalid worker profile effort value"},
		{"bidi profile", strings.ReplaceAll(validWorkerPolicy, `"worker"`, `"worker\u202e"`), "invalid worker profile name value"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			writeWorkerPolicy(t, home, test.data)
			if _, err := LoadWorkerPolicy(home); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("unsafe selected policy value = %v, want %q", err, test.want)
			}
			if _, err := ResolveWorkerCandidate(home, "execute", "bounded", WorkerCandidateOverrides{}); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("unsafe selected candidate = %v, want %q", err, test.want)
			}
		})
	}
}

func TestResolveWorkerCandidateRejectsUnrenderableOverrides(t *testing.T) {
	home := t.TempDir()
	writeWorkerPolicy(t, home, validWorkerPolicy)
	value := "gpt-6\u202e"
	for _, test := range []struct {
		name      string
		overrides WorkerCandidateOverrides
	}{
		{"model", WorkerCandidateOverrides{ModelOverride: &value}},
		{"effort", WorkerCandidateOverrides{EffortOverride: &value}},
		{"profile", WorkerCandidateOverrides{ProfileOverride: &value}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ResolveWorkerCandidate(home, "execute", "bounded", test.overrides); err == nil || !strings.Contains(err.Error(), "invalid "+test.name+" override") {
				t.Fatalf("unrenderable %s override = %v", test.name, err)
			}
		})
	}
}
