package routing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/brief"
)

func TestResolveUsesEveryCanonicalRouteCell(t *testing.T) {
	profiles := []Profile{
		{Name: "scout-mechanical", Harness: "claude"},
		{Name: "scout-standard", Harness: "codex"},
		{Name: "scout-deep", Harness: "opencode"},
		{Name: "ship-mechanical", Harness: "claude"},
		{Name: "ship-standard", Harness: "codex"},
		{Name: "ship-deep", Harness: "opencode"},
	}
	config := Config{Profiles: profiles}
	for _, kind := range TaskKinds() {
		for _, class := range ExecutionClasses() {
			name := string(kind) + "-" + string(class)
			config.Routes = append(config.Routes, Route{Kind: kind, ExecutionClass: class, Profile: name})
			want := profileNamed(t, profiles, name)
			t.Run(name, func(t *testing.T) {
				resolved, err := Resolve(Request{
					Kind:        kind,
					Declaration: brief.Declaration{ExecutionClass: brief.ExecutionClass(class)},
				}, config, LegacyDefaults{}, testAvailability())
				if err != nil {
					t.Fatal(err)
				}
				if resolved.Profile != name || resolved.Source != RoutingSourceRoute || resolved.Harness != want.Harness {
					t.Fatalf("Resolve() = %+v, want route profile %q with harness %q", resolved, name, want.Harness)
				}
			})
		}
	}
}

func TestResolveExplicitProfileOverridesRoute(t *testing.T) {
	config := Config{
		Profiles: []Profile{{Name: "route", Harness: "claude"}, {Name: "explicit", Harness: "codex"}},
		Routes:   []Route{{Kind: TaskKindShip, ExecutionClass: ExecutionClassStandard, Profile: "route"}},
	}

	resolved, err := Resolve(Request{
		Kind:            TaskKindShip,
		Declaration:     brief.Declaration{ExecutionClass: brief.ExecutionClassStandard},
		Profile:         "explicit",
		ProfileFromFlag: true,
	}, config, LegacyDefaults{}, testAvailability())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Profile != "explicit" || resolved.Source != RoutingSourceExplicitProfile || resolved.Harness != "codex" {
		t.Fatalf("Resolve() = %+v, want explicit profile", resolved)
	}
}

func TestResolveRequiresRouteForClassifiedBrief(t *testing.T) {
	_, err := Resolve(Request{
		Kind:        TaskKindScout,
		Declaration: brief.Declaration{ExecutionClass: brief.ExecutionClassDeep},
	}, Config{}, LegacyDefaults{}, testAvailability())
	if err == nil || !strings.Contains(err.Error(), "route scout.deep is not configured") {
		t.Fatalf("Resolve() error = %v, want missing route", err)
	}
}

func TestResolveRejectsInvalidExecutionClass(t *testing.T) {
	_, err := Resolve(Request{
		Kind:        TaskKindScout,
		Declaration: brief.Declaration{ExecutionClass: "incorrect"},
	}, Config{}, LegacyDefaults{}, testAvailability())
	if err == nil || !strings.Contains(err.Error(), "invalid execution class") {
		t.Fatalf("Resolve() error = %v, want invalid execution class", err)
	}
}

func TestResolveRejectsDanglingRoute(t *testing.T) {
	_, err := Resolve(Request{
		Kind:        TaskKindScout,
		Declaration: brief.Declaration{ExecutionClass: brief.ExecutionClassDeep},
	}, Config{Routes: []Route{{Kind: TaskKindScout, ExecutionClass: ExecutionClassDeep, Profile: "missing"}}}, LegacyDefaults{}, testAvailability())
	if err == nil || !strings.Contains(err.Error(), "profile \"missing\" is not configured") {
		t.Fatalf("Resolve() error = %v, want missing profile", err)
	}
}

func TestResolvePrecedence(t *testing.T) {
	config := Config{Profiles: []Profile{{Name: "daily", Harness: "claude", Model: "profile-model", Effort: "profile-effort"}}}
	legacy := LegacyDefaults{Harness: "codex", Models: map[string]string{"claude": "legacy-model"}, Efforts: map[string]string{"claude": "legacy-effort"}}
	resolved, err := Resolve(Request{
		Kind:            TaskKindShip,
		Declaration:     brief.Declaration{Model: "brief-model", Effort: "brief-effort"},
		Profile:         "daily",
		ProfileFromFlag: true,
		Harness:         "claude",
		HarnessFromFlag: true,
		Model:           "flag-model",
		ModelFromFlag:   true,
		Effort:          "flag-effort",
		EffortFromFlag:  true,
	}, config, legacy, testAvailability())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Harness != "claude" || resolved.Model != "flag-model" || resolved.Effort != "flag-effort" {
		t.Fatalf("Resolve() = %+v, want explicit values", resolved)
	}
}

func TestResolveBriefValuesOverrideProfileWithOneCompatibilityWarning(t *testing.T) {
	resolved, err := Resolve(Request{
		Kind:            TaskKindShip,
		Declaration:     brief.Declaration{Model: "brief-model", Effort: "brief-effort"},
		Profile:         "daily",
		ProfileFromFlag: true,
	}, Config{Profiles: []Profile{{Name: "daily", Harness: "claude", Model: "profile-model", Effort: "profile-effort"}}}, LegacyDefaults{}, testAvailability())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Model != "brief-model" || resolved.Effort != "brief-effort" {
		t.Fatalf("Resolve() = %+v, want brief values", resolved)
	}
	if len(resolved.Warnings) != 1 || !strings.Contains(resolved.Warnings[0], "brief") || !strings.Contains(resolved.Warnings[0], "profile") {
		t.Fatalf("warnings = %q, want one profile compatibility warning", resolved.Warnings)
	}
}

func TestResolveProfileValuesOverrideLegacyDefaults(t *testing.T) {
	resolved, err := Resolve(Request{
		Kind:            TaskKindShip,
		Profile:         "daily",
		ProfileFromFlag: true,
	}, Config{Profiles: []Profile{{Name: "daily", Harness: "claude", Model: "profile-model", Effort: "profile-effort"}}}, LegacyDefaults{
		Harness: "codex",
		Models:  map[string]string{"claude": "legacy-model"},
		Efforts: map[string]string{"claude": "legacy-effort"},
	}, testAvailability())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Model != "profile-model" || resolved.Effort != "profile-effort" {
		t.Fatalf("Resolve() = %+v, want profile values", resolved)
	}
}

func TestResolveUsesLegacyDefaultsThenNativeDefaults(t *testing.T) {
	request := Request{Kind: TaskKindShip, Declaration: brief.Declaration{ExecutionClass: brief.ExecutionClassStandard}}
	config := Config{Profiles: []Profile{{Name: "daily", Harness: "claude"}}, Routes: []Route{{Kind: TaskKindShip, ExecutionClass: ExecutionClassStandard, Profile: "daily"}}}

	resolved, err := Resolve(request, config, LegacyDefaults{Models: map[string]string{"claude": "legacy-model"}, Efforts: map[string]string{"claude": "legacy-effort"}}, testAvailability())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Model != "legacy-model" || resolved.Effort != "legacy-effort" {
		t.Fatalf("Resolve() = %+v, want legacy defaults", resolved)
	}

	resolved, err = Resolve(request, config, LegacyDefaults{}, testAvailability())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Model != "" || resolved.Effort != "" {
		t.Fatalf("Resolve() = %+v, want native empty defaults", resolved)
	}
}

func TestResolveExplicitProfileWithoutExecutionClassUsesProfile(t *testing.T) {
	resolved, err := Resolve(Request{
		Kind:            TaskKindScout,
		Profile:         "daily",
		ProfileFromFlag: true,
	}, Config{Profiles: []Profile{{Name: "daily", Harness: "codex"}}}, LegacyDefaults{}, testAvailability())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source != RoutingSourceExplicitProfile || resolved.Profile != "daily" || resolved.ExecutionClass != "" {
		t.Fatalf("Resolve() = %+v, want unclassified explicit profile", resolved)
	}
}

func TestResolveNonExplicitValuesKeepUnclassifiedRequestLegacy(t *testing.T) {
	resolved, err := Resolve(Request{
		Kind:        TaskKindShip,
		Declaration: brief.Declaration{Model: "brief-model"},
		Profile:     "ignored-profile",
		Harness:     "codex",
		Model:       "ignored-model",
		Effort:      "ignored-effort",
	}, Config{}, LegacyDefaults{
		Harness: "claude",
		Models:  map[string]string{"claude": "legacy-model"},
		Efforts: map[string]string{"claude": "legacy-effort"},
	}, testAvailability())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source != RoutingSourceLegacy || resolved.Profile != "" || resolved.Harness != "claude" || resolved.Model != "brief-model" || resolved.Effort != "legacy-effort" {
		t.Fatalf("Resolve() = %+v, want legacy defaults and brief precedence", resolved)
	}
}

func TestResolveIgnoresNonExplicitExecutionValuesOnProfiledRequest(t *testing.T) {
	resolved, err := Resolve(Request{
		Kind:        TaskKindShip,
		Declaration: brief.Declaration{ExecutionClass: brief.ExecutionClassStandard},
		Harness:     "codex",
		Model:       "ignored-model",
		Effort:      "ignored-effort",
	}, Config{
		Profiles: []Profile{{Name: "daily", Harness: "claude", Model: "profile-model", Effort: "profile-effort"}},
		Routes:   []Route{{Kind: TaskKindShip, ExecutionClass: ExecutionClassStandard, Profile: "daily"}},
	}, LegacyDefaults{Harness: "codex"}, testAvailability())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source != RoutingSourceRoute || resolved.Harness != "claude" || resolved.Model != "profile-model" || resolved.Effort != "profile-effort" {
		t.Fatalf("Resolve() = %+v, want route profile values", resolved)
	}
}

func TestResolveRejectsInvalidLegacyTaskKind(t *testing.T) {
	_, err := Resolve(Request{Kind: "invalid"}, Config{}, LegacyDefaults{Harness: "claude"}, testAvailability())
	if err == nil || !strings.Contains(err.Error(), "invalid task kind") {
		t.Fatalf("Resolve() error = %v, want invalid task kind", err)
	}
}

func TestResolveRejectsExplicitEmptyProfileInsteadOfUsingRoute(t *testing.T) {
	_, err := Resolve(Request{
		Kind:            TaskKindScout,
		Declaration:     brief.Declaration{ExecutionClass: brief.ExecutionClassStandard},
		ProfileFromFlag: true,
	}, Config{Routes: []Route{{Kind: TaskKindScout, ExecutionClass: ExecutionClassStandard, Profile: "daily"}}}, LegacyDefaults{}, testAvailability())
	if err == nil || !strings.Contains(err.Error(), "profile name is required") {
		t.Fatalf("Resolve() error = %v, want explicit empty profile error", err)
	}
}

func TestResolveProfiledValidation(t *testing.T) {
	config := Config{Profiles: []Profile{{Name: "profile", Harness: "claude"}}}
	request := Request{Kind: TaskKindScout, Profile: "profile", ProfileFromFlag: true}

	for _, test := range []struct {
		name         string
		request      Request
		config       Config
		availability Availability
		want         string
	}{
		{name: "unsupported profile harness", config: Config{Profiles: []Profile{{Name: "profile", Harness: "unknown"}}}, request: request, availability: testAvailability(), want: "profile harness \"unknown\" not recognized"},
		{name: "unsupported final harness", request: Request{Kind: TaskKindScout, Profile: "profile", ProfileFromFlag: true, Harness: "unknown", HarnessFromFlag: true}, config: config, availability: testAvailability(), want: "harness \"unknown\" not recognized"},
		{name: "harness absent from PATH", request: request, config: config, availability: testAvailabilityWithoutPath("claude"), want: "harness \"claude\" is not installed on PATH"},
		{name: "model unsupported", request: Request{Kind: TaskKindScout, Profile: "profile", ProfileFromFlag: true, Harness: "grok", HarnessFromFlag: true, Model: "opaque", ModelFromFlag: true}, config: config, availability: testAvailability(), want: "harness \"grok\" takes no model"},
		{name: "effort unsupported", request: Request{Kind: TaskKindScout, Profile: "profile", ProfileFromFlag: true, Harness: "opencode", HarnessFromFlag: true, Effort: "high", EffortFromFlag: true}, config: config, availability: testAvailability(), want: "harness \"opencode\" takes no effort"},
		{name: "mechanical requires prompt", request: Request{Kind: TaskKindScout, Declaration: brief.Declaration{ExecutionClass: brief.ExecutionClassMechanical}, Profile: "profile", ProfileFromFlag: true}, config: config, availability: testAvailabilityWithoutPrompt("claude"), want: "mechanical execution requires a prompt-capable harness"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Resolve(test.request, test.config, LegacyDefaults{}, test.availability)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Resolve() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestResolveUnclassifiedBriefPreservesLegacyWarnAndLaunchBehavior(t *testing.T) {
	resolved, err := Resolve(Request{
		Kind:                TaskKindShip,
		Declaration:         brief.Declaration{Model: "brief-model", Effort: "brief-effort"},
		BriefHasFrontMatter: true,
	}, Config{}, LegacyDefaults{Harness: "grok"}, testAvailabilityWithoutPath("grok"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source != RoutingSourceLegacy || resolved.Harness != "grok" || resolved.Model != "brief-model" || resolved.Effort != "brief-effort" {
		t.Fatalf("Resolve() = %+v, want legacy resolution", resolved)
	}
	want := `warning: harness "grok" cannot carry model "brief-model", effort "brief-effort", the operator-decision rule, the front-matter disclaimer; launching anyway`
	if len(resolved.Warnings) != 1 || resolved.Warnings[0] != want {
		t.Fatalf("warnings = %q, want %q", resolved.Warnings, want)
	}
}

func TestLoadLegacyDefaultsUsesConfiguredHarnessAndPerHarnessSettings(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, "config")
	if err := os.Mkdir(config, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, file := range []struct {
		name  string
		value string
	}{
		{name: "harness", value: "claude\n"},
		{name: "model.claude", value: "opaque-model\n"},
		{name: "effort.claude", value: "high\n"},
	} {
		if err := os.WriteFile(filepath.Join(config, file.name), []byte(file.value), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	defaults, err := LoadLegacyDefaults(home, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Harness != "claude" || defaults.Models["claude"] != "opaque-model" || defaults.Efforts["claude"] != "high" {
		t.Fatalf("LoadLegacyDefaults() = %+v, want configured claude defaults", defaults)
	}
}

func profileNamed(t *testing.T, profiles []Profile, name string) Profile {
	t.Helper()
	for _, profile := range profiles {
		if profile.Name == name {
			return profile
		}
	}
	t.Fatalf("profile %q missing from fixture", name)
	return Profile{}
}

func testAvailability() Availability {
	return Availability{
		IsSupported: func(name string) bool {
			return name == "claude" || name == "codex" || name == "opencode" || name == "grok"
		},
		OnPath: func(string) bool { return true },
		SupportsModel: func(name string) bool {
			return name == "claude" || name == "codex" || name == "opencode"
		},
		SupportsEffort: func(name string) bool { return name == "claude" || name == "codex" },
		CarriesPrompt:  func(name string) bool { return name == "claude" || name == "codex" || name == "opencode" },
	}
}

func testAvailabilityWithoutPath(name string) Availability {
	availability := testAvailability()
	availability.OnPath = func(got string) bool { return got != name }
	return availability
}

func testAvailabilityWithoutPrompt(name string) Availability {
	availability := testAvailability()
	availability.CarriesPrompt = func(got string) bool { return got != name }
	return availability
}
