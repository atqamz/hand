package routing

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/atqamz/hand/internal/harness"
)

var windowsReservedNames = map[string]bool{
	"CON":  true,
	"PRN":  true,
	"AUX":  true,
	"NUL":  true,
	"COM1": true,
	"COM2": true,
	"COM3": true,
	"COM4": true,
	"COM5": true,
	"COM6": true,
	"COM7": true,
	"COM8": true,
	"COM9": true,
	"LPT1": true,
	"LPT2": true,
	"LPT3": true,
	"LPT4": true,
	"LPT5": true,
	"LPT6": true,
	"LPT7": true,
	"LPT8": true,
	"LPT9": true,
}

type profileValidationError struct {
	code ConfigProblemCode
	err  error
}

func (e *profileValidationError) Error() string {
	return e.err.Error()
}

func (e *profileValidationError) Unwrap() error {
	return e.err
}

func ValidateProfileName(name string) error {
	if name == "" {
		return fmt.Errorf("profile name is required")
	}
	if name == "." || name == ".." || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return fmt.Errorf("profile name %q is not filename-safe", name)
	}
	if strings.ContainsAny(name, `<>:"/\\|?*`) {
		return fmt.Errorf("profile name %q is not filename-safe", name)
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return fmt.Errorf("profile name %q is not filename-safe", name)
	}
	if filepath.Base(name) != name {
		return fmt.Errorf("profile name %q is not filename-safe", name)
	}
	device := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if windowsReservedNames[device] {
		return fmt.Errorf("profile name %q is not filename-safe", name)
	}
	return nil
}

func ValidateProfile(profile Profile) error {
	if err := ValidateProfileName(profile.Name); err != nil {
		return err
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "harness", value: profile.Harness},
		{name: "model", value: profile.Model},
		{name: "effort", value: profile.Effort},
	} {
		if strings.ContainsAny(field.value, "\r\n") {
			return fmt.Errorf("profile %s must be one line", field.name)
		}
	}
	if profile.Harness == "" {
		return fmt.Errorf("profile harness is required")
	}
	if !harness.IsSupported(profile.Harness) {
		return &profileValidationError{code: ConfigProblemUnsupportedHarness, err: fmt.Errorf("profile harness %q not recognized", profile.Harness)}
	}
	if profile.Model != "" && !harness.SupportsModel(profile.Harness) {
		return &profileValidationError{code: ConfigProblemUnsupportedModel, err: fmt.Errorf("harness %q takes no model", profile.Harness)}
	}
	if profile.Effort != "" && !harness.SupportsEffort(profile.Harness) {
		return &profileValidationError{code: ConfigProblemUnsupportedEffort, err: fmt.Errorf("harness %q takes no effort", profile.Harness)}
	}
	return nil
}

func ValidateRoute(route Route) error {
	if err := ValidateTaskKind(route.Kind); err != nil {
		return err
	}
	if err := ValidateExecutionClass(route.ExecutionClass); err != nil {
		return err
	}
	if err := ValidateProfileName(route.Profile); err != nil {
		return fmt.Errorf("route profile: %w", err)
	}
	return nil
}

func ValidateTaskKind(kind TaskKind) error {
	if !isTaskKind(kind) {
		return fmt.Errorf("invalid task kind %q: want scout or ship", kind)
	}
	return nil
}

func ValidateExecutionClass(class ExecutionClass) error {
	if !isExecutionClass(class) {
		return fmt.Errorf("invalid execution class %q: want mechanical, standard, or deep", class)
	}
	return nil
}

func isTaskKind(kind TaskKind) bool {
	return slices.Contains(taskKinds, kind)
}

func isExecutionClass(class ExecutionClass) bool {
	return slices.Contains(executionClasses, class)
}
