package routing

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/brief"
	"github.com/atqamz/hand/internal/harness"
)

type Request struct {
	Kind                TaskKind
	Declaration         brief.Declaration
	BriefHasFrontMatter bool
	Profile             string
	ProfileFromFlag     bool
	Harness             string
	HarnessFromFlag     bool
	Model               string
	ModelFromFlag       bool
	Effort              string
	EffortFromFlag      bool
}

type LegacyDefaults struct {
	Harness string
	Models  map[string]string
	Efforts map[string]string
}

type Availability struct {
	IsSupported    func(string) bool
	OnPath         func(string) bool
	SupportsModel  func(string) bool
	SupportsEffort func(string) bool
	CarriesPrompt  func(string) bool
}

type ResolvedRoute struct {
	Profile             string
	Source              RoutingSource
	Harness             string
	Model               string
	Effort              string
	ExecutionClass      brief.ExecutionClass
	PlannedAgainst      string
	BriefHasFrontMatter bool
	Warnings            []string
}

func DefaultAvailability() Availability {
	return Availability{
		IsSupported:    harness.IsSupported,
		OnPath:         harnessOnPath,
		SupportsModel:  harness.SupportsModel,
		SupportsEffort: harness.SupportsEffort,
		CarriesPrompt:  harness.CarriesPrompt,
	}
}

func LoadLegacyDefaults(home, detectedHarness string) (LegacyDefaults, error) {
	configuredHarness, err := readLegacyDefault(home, "harness")
	if err != nil {
		return LegacyDefaults{}, err
	}
	defaults := LegacyDefaults{Harness: configuredHarness}
	if defaults.Harness == "" {
		defaults.Harness = detectedHarness
	}
	defaults.Models = make(map[string]string)
	defaults.Efforts = make(map[string]string)
	for _, name := range harness.Names() {
		model, err := readLegacyDefault(home, "model."+name)
		if err != nil {
			return LegacyDefaults{}, err
		}
		effort, err := readLegacyDefault(home, "effort."+name)
		if err != nil {
			return LegacyDefaults{}, err
		}
		defaults.Models[name] = model
		defaults.Efforts[name] = effort
	}
	return defaults, nil
}

func Resolve(request Request, config Config, legacy LegacyDefaults, availability Availability) (ResolvedRoute, error) {
	result := ResolvedRoute{
		ExecutionClass:      request.Declaration.ExecutionClass,
		PlannedAgainst:      request.Declaration.PlannedAgainst,
		BriefHasFrontMatter: request.BriefHasFrontMatter,
	}
	if request.Declaration.ExecutionClass == "" && request.Profile == "" && !request.ProfileFromFlag {
		return resolveLegacy(request, legacy, availability, result)
	}
	return resolveProfiled(request, config, legacy, availability, result)
}

func resolveProfiled(request Request, config Config, legacy LegacyDefaults, availability Availability, result ResolvedRoute) (ResolvedRoute, error) {
	if err := ValidateTaskKind(request.Kind); err != nil {
		return ResolvedRoute{}, err
	}
	if request.Declaration.ExecutionClass != "" {
		if err := ValidateExecutionClass(ExecutionClass(request.Declaration.ExecutionClass)); err != nil {
			return ResolvedRoute{}, err
		}
	}

	profile, source, err := selectedProfile(request, config)
	if err != nil {
		return ResolvedRoute{}, err
	}
	if !available(availability.IsSupported, profile.Harness) {
		return ResolvedRoute{}, fmt.Errorf("profile harness %q not recognized", profile.Harness)
	}

	result.Profile = profile.Name
	result.Source = source
	result.Harness = firstNonEmpty(request.Harness, profile.Harness, legacy.Harness)
	if result.Harness == "" {
		return ResolvedRoute{}, missingHarnessError()
	}
	if !available(availability.IsSupported, result.Harness) {
		return ResolvedRoute{}, fmt.Errorf("harness %q not recognized", result.Harness)
	}
	if !available(availability.OnPath, result.Harness) {
		return ResolvedRoute{}, fmt.Errorf("harness %q is not installed on PATH", result.Harness)
	}
	result.Model = firstNonEmpty(request.Model, request.Declaration.Model, profile.Model, legacy.Models[result.Harness])
	result.Effort = firstNonEmpty(request.Effort, request.Declaration.Effort, profile.Effort, legacy.Efforts[result.Harness])
	if result.Model != "" && !available(availability.SupportsModel, result.Harness) {
		return ResolvedRoute{}, fmt.Errorf("harness %q takes no model", result.Harness)
	}
	if result.Effort != "" && !available(availability.SupportsEffort, result.Harness) {
		return ResolvedRoute{}, fmt.Errorf("harness %q takes no effort", result.Harness)
	}
	if result.ExecutionClass == brief.ExecutionClassMechanical && !available(availability.CarriesPrompt, result.Harness) {
		return ResolvedRoute{}, fmt.Errorf("mechanical execution requires a prompt-capable harness: harness %q cannot carry the required mechanical worker guidance", result.Harness)
	}
	if warning := briefProfileOverrideWarning(request.Declaration, profile); warning != "" {
		result.Warnings = []string{warning}
	}
	return result, nil
}

func resolveLegacy(request Request, legacy LegacyDefaults, availability Availability, result ResolvedRoute) (ResolvedRoute, error) {
	result.Source = RoutingSourceLegacy
	result.Harness = firstNonEmpty(request.Harness, legacy.Harness)
	if result.Harness == "" {
		return ResolvedRoute{}, missingHarnessError()
	}
	if !available(availability.IsSupported, result.Harness) {
		return ResolvedRoute{}, fmt.Errorf("harness %q not recognized", result.Harness)
	}
	result.Model = firstNonEmpty(request.Model, request.Declaration.Model, legacy.Models[result.Harness])
	result.Effort = firstNonEmpty(request.Effort, request.Declaration.Effort, legacy.Efforts[result.Harness])
	result.Warnings = legacyWarnings(result, availability)
	return result, nil
}

func selectedProfile(request Request, config Config) (Profile, RoutingSource, error) {
	name := request.Profile
	source := RoutingSourceExplicitProfile
	if name == "" && !request.ProfileFromFlag {
		source = RoutingSourceRoute
		route, found := findRoute(config.Routes, request.Kind, ExecutionClass(request.Declaration.ExecutionClass))
		if !found {
			return Profile{}, "", fmt.Errorf("route %s.%s is not configured", request.Kind, request.Declaration.ExecutionClass)
		}
		name = route.Profile
	}
	if err := ValidateProfileName(name); err != nil {
		return Profile{}, "", err
	}
	profile, found := findProfile(config.Profiles, name)
	if !found {
		return Profile{}, "", fmt.Errorf("profile %q is not configured", name)
	}
	return profile, source, nil
}

func findRoute(routes []Route, kind TaskKind, class ExecutionClass) (Route, bool) {
	for _, route := range routes {
		if route.Kind == kind && route.ExecutionClass == class {
			return route, true
		}
	}
	return Route{}, false
}

func findProfile(profiles []Profile, name string) (Profile, bool) {
	for _, profile := range profiles {
		if profile.Name == name {
			return profile, true
		}
	}
	return Profile{}, false
}

func briefProfileOverrideWarning(declaration brief.Declaration, profile Profile) string {
	var fields []string
	if declaration.Model != "" && profile.Model != "" {
		fields = append(fields, "model")
	}
	if declaration.Effort != "" && profile.Effort != "" {
		fields = append(fields, "effort")
	}
	if len(fields) == 0 {
		return ""
	}
	return fmt.Sprintf("warning: brief %s override selected profile %q; use brief metadata only for task-specific overrides", strings.Join(fields, " and "), profile.Name)
}

func legacyWarnings(result ResolvedRoute, availability Availability) []string {
	var dropped []string
	if result.Model != "" && !available(availability.SupportsModel, result.Harness) {
		dropped = append(dropped, fmt.Sprintf("model %q", result.Model))
	}
	if result.Effort != "" && !available(availability.SupportsEffort, result.Harness) {
		dropped = append(dropped, fmt.Sprintf("effort %q", result.Effort))
	}
	if !available(availability.CarriesPrompt, result.Harness) {
		dropped = append(dropped, "the operator-decision rule")
		if result.BriefHasFrontMatter {
			dropped = append(dropped, "the front-matter disclaimer")
		}
	}
	if len(dropped) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("warning: harness %q cannot carry %s; launching anyway", result.Harness, strings.Join(dropped, ", "))}
}

func missingHarnessError() error {
	return fmt.Errorf("current supervisor harness is unknown and no worker harness override is configured; run hand config set harness <name>")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func available(check func(string) bool, name string) bool {
	return check != nil && check(name)
}

func harnessOnPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func readLegacyDefault(home, name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(home, configDirectory, name))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read config/%s: %w", name, err)
	}
	return strings.TrimSpace(string(data)), nil
}
