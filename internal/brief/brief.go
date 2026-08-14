// Package brief reads optional dispatch metadata a brief.md declares for itself.
package brief

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type Declaration struct {
	Model          string
	Effort         string
	ExecutionClass ExecutionClass
	PlannedAgainst string
}

type ValidationError struct {
	Field string
	Value string
	Want  string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid %s %q: want %s", e.Field, e.Value, e.Want)
}

type ExecutionClass string

const (
	ExecutionClassMechanical ExecutionClass = "mechanical"
	ExecutionClassStandard   ExecutionClass = "standard"
	ExecutionClassDeep       ExecutionClass = "deep"
)

func (c ExecutionClass) Valid() bool {
	switch c {
	case ExecutionClassMechanical, ExecutionClassStandard, ExecutionClassDeep:
		return true
	default:
		return false
	}
}

// Parse ignores unknown keys inside the block and reports "no declaration" for a brief it
// cannot scan (an unterminated fence, a line past bufio's token cap) by choice: a brief is
// prose carrying two optional settings, not a config file, so its shape may not fail a spawn.
func Parse(path string) (Declaration, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return Declaration{}, false, fmt.Errorf("open brief %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return Declaration{}, false, nil
	}

	var d Declaration
	var executionClassSet, plannedAgainstSet bool
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "---" {
			if err := validateDeclaration(d, executionClassSet, plannedAgainstSet); err != nil {
				return Declaration{}, false, err
			}
			return d, true, nil
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "model":
			d.Model = unquote(value)
		case "effort":
			d.Effort = unquote(value)
		case "execution_class":
			executionClassSet = true
			d.ExecutionClass = ExecutionClass(unquote(value))
		case "planned_against":
			plannedAgainstSet = true
			d.PlannedAgainst = unquote(value)
		}
	}
	return Declaration{}, false, nil
}

func validateDeclaration(d Declaration, executionClassSet, plannedAgainstSet bool) error {
	if executionClassSet && !d.ExecutionClass.Valid() {
		return &ValidationError{Field: "execution_class", Value: string(d.ExecutionClass), Want: "mechanical, standard, or deep"}
	}
	if plannedAgainstSet && !validObjectID(d.PlannedAgainst) {
		return &ValidationError{Field: "planned_against", Value: d.PlannedAgainst, Want: "a full hexadecimal Git object ID"}
	}
	return nil
}

func validObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func unquote(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		return value[1 : len(value)-1]
	}
	return value
}
