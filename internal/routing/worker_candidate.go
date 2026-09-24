package routing

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type WorkerCandidateOverrides struct {
	ProfileOverride *string
	HarnessOverride *string
	ModelOverride   *string
	EffortOverride  *string
}

type WorkerCandidate struct {
	Profile       Profile
	PolicyWitness string
}

func ResolveWorkerCandidate(home, intent, judgment string, overrides WorkerCandidateOverrides) (WorkerCandidate, error) {
	policy, err := LoadWorkerPolicy(home)
	if err != nil {
		return WorkerCandidate{}, err
	}
	for _, override := range []struct {
		name     string
		value    *string
		nonempty bool
	}{
		{"profile", overrides.ProfileOverride, true},
		{"harness", overrides.HarnessOverride, true},
		{"model", overrides.ModelOverride, false},
		{"effort", overrides.EffortOverride, false},
	} {
		if override.value == nil {
			continue
		}
		if (override.nonempty && *override.value == "") || !validWorkerCandidateValue(*override.value) {
			return WorkerCandidate{}, fmt.Errorf("invalid %s override", override.name)
		}
	}
	profileName := ""
	for _, route := range policy.Routes {
		if route.Intent == intent && route.Judgment == judgment {
			profileName = route.Profile
			break
		}
	}
	if profileName == "" {
		return WorkerCandidate{}, fmt.Errorf("invalid Worker Route %q.%q", intent, judgment)
	}
	if overrides.ProfileOverride != nil {
		profileName = *overrides.ProfileOverride
	}
	for _, profile := range policy.Profiles {
		if profile.Name != profileName {
			continue
		}
		if overrides.HarnessOverride != nil {
			profile.Harness = *overrides.HarnessOverride
		}
		if overrides.ModelOverride != nil {
			profile.Model = *overrides.ModelOverride
		}
		if overrides.EffortOverride != nil {
			profile.Effort = *overrides.EffortOverride
		}
		for _, field := range []struct {
			name  string
			value string
		}{
			{"profile", profile.Name},
			{"model", profile.Model},
			{"effort", profile.Effort},
		} {
			if !validWorkerCandidateValue(field.value) {
				return WorkerCandidate{}, fmt.Errorf("invalid %s candidate value", field.name)
			}
		}
		if err := ValidateProfile(profile); err != nil {
			return WorkerCandidate{}, fmt.Errorf("invalid Worker candidate: %w", err)
		}
		return WorkerCandidate{Profile: profile, PolicyWitness: policy.Witness}, nil
	}
	return WorkerCandidate{}, fmt.Errorf("worker candidate names missing profile %q", profileName)
}

func validWorkerCandidateValue(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= 512 &&
		strings.IndexFunc(value, func(r rune) bool { return !unicode.IsPrint(r) }) < 0
}
