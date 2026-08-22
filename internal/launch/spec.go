// Package launch defines the transport-neutral process data used to start a worker.
package launch

import (
	"fmt"
	"unicode"
)

// LaunchSpec describes one executable invocation without embedding shell syntax or lifecycle data.
type LaunchSpec struct {
	Executable string
	Args       []string
	Env        map[string]string
	Cwd        string
}

// NewSpec validates and copies a launch description so callers can safely reuse their input buffers.
func NewSpec(spec LaunchSpec) (LaunchSpec, error) {
	if err := spec.Validate(); err != nil {
		return LaunchSpec{}, err
	}
	return spec.Clone(), nil
}

// Validate rejects data that cannot be carried safely through process and Herdr argument boundaries.
func (s LaunchSpec) Validate() error {
	if s.Executable == "" {
		return fmt.Errorf("launch executable is empty")
	}
	if err := validateText("executable", s.Executable); err != nil {
		return err
	}
	if err := validateText("cwd", s.Cwd); err != nil {
		return err
	}
	for i, arg := range s.Args {
		if err := validateText(fmt.Sprintf("argument %d", i), arg); err != nil {
			return err
		}
	}
	for key, value := range s.Env {
		if !validEnvKey(key) {
			return fmt.Errorf("invalid launch environment key %q", key)
		}
		if err := validateText("environment value for "+key, value); err != nil {
			return err
		}
	}
	return nil
}

// Clone returns an independent copy of the launch description.
func (s LaunchSpec) Clone() LaunchSpec {
	clone := LaunchSpec{
		Executable: s.Executable,
		Cwd:        s.Cwd,
		Args:       append([]string(nil), s.Args...),
	}
	if s.Env != nil {
		clone.Env = make(map[string]string, len(s.Env))
		for key, value := range s.Env {
			clone.Env[key] = value
		}
	}
	return clone
}

// MergeEnv applies overlays in order, with later layers taking precedence.
func (s LaunchSpec) MergeEnv(overlays ...map[string]string) (LaunchSpec, error) {
	merged := s.Clone()
	if merged.Env == nil && len(overlays) > 0 {
		merged.Env = make(map[string]string)
	}
	for _, overlay := range overlays {
		for key, value := range overlay {
			merged.Env[key] = value
		}
	}
	return NewSpec(merged)
}

func validateText(name, value string) error {
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("launch %s contains unsupported control character", name)
		}
	}
	return nil
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if i == 0 {
			if !isEnvLetter(r) && r != '_' {
				return false
			}
			continue
		}
		if !isEnvLetter(r) && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func isEnvLetter(r rune) bool {
	return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'
}
