package harness

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
)

type CapabilityState string

const (
	CapabilityUnavailable CapabilityState = "unavailable"
	CapabilityUnsupported CapabilityState = "unsupported"
	CapabilityUnknown     CapabilityState = "unknown"
	CapabilityReady       CapabilityState = "ready"
)

type Capability struct {
	Name       string
	Executable string
	State      CapabilityState
	Reason     string
	Models     []string
}

type CapabilityProbe struct {
	Platform string
	LookPath func(string) (string, error)
	Models   func(string) ([]string, error)
}

func Executable(name string) string {
	if name == Antigravity {
		return "agy"
	}
	return name
}

func PlatformSupported(name, platform string) bool {
	if name != Antigravity {
		return IsSupported(name)
	}
	switch platform {
	case "linux", "darwin", "windows":
		return true
	default:
		return false
	}
}

func SupportsSupervisor(name string) bool {
	return name != Antigravity && IsSupported(name)
}

func Inspect(name string, probe CapabilityProbe) Capability {
	result := Capability{Name: name, Executable: Executable(name)}
	if !IsSupported(name) {
		result.State = CapabilityUnsupported
		result.Reason = "harness is not registered"
		return result
	}
	platform := probe.Platform
	if platform == "" {
		platform = runtime.GOOS
	}
	if !PlatformSupported(name, platform) {
		result.State = CapabilityUnsupported
		result.Reason = fmt.Sprintf("platform %q is unsupported", platform)
		return result
	}
	lookPath := probe.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	path, err := lookPath(result.Executable)
	if err != nil {
		result.State = CapabilityUnavailable
		result.Reason = "executable is unavailable"
		return result
	}
	if name != Antigravity {
		result.State = CapabilityReady
		return result
	}
	models := probe.Models
	if models == nil {
		models = probeAntigravityModels
	}
	result.Models, err = models(path)
	if err != nil {
		result.State = CapabilityUnknown
		result.Reason = "model capability could not be verified"
		if authenticationUnavailable(err) {
			result.State = CapabilityUnavailable
			result.Reason = "authentication/configuration unavailable"
		}
		return result
	}
	if len(result.Models) == 0 {
		result.State = CapabilityUnknown
		result.Reason = "model capability could not be verified"
		return result
	}
	result.State = CapabilityReady
	return result
}

func ValidateModel(name, model string) error {
	return ValidateModelWithProbe(name, model, CapabilityProbe{})
}

func ValidateModelWithProbe(name, model string, probe CapabilityProbe) error {
	if model == "" || name != Antigravity {
		return nil
	}
	capability := Inspect(name, probe)
	switch capability.State {
	case CapabilityUnavailable:
		return fmt.Errorf("harness %q is unavailable: %s", name, capability.Reason)
	case CapabilityUnsupported:
		return fmt.Errorf("harness %q is unsupported: %s", name, capability.Reason)
	case CapabilityUnknown:
		return fmt.Errorf("harness %q model capability is unknown: %s", name, capability.Reason)
	case CapabilityReady:
		if !slices.Contains(capability.Models, model) {
			return fmt.Errorf("harness %q does not support model %q", name, model)
		}
	}
	return nil
}

func ValidateEffort(name, effort string) error {
	if name != Antigravity || effort == "" {
		return nil
	}
	if slices.Contains([]string{"low", "medium", "high"}, effort) {
		return nil
	}
	return fmt.Errorf("harness %q does not support effort %q; want low, medium, or high", name, effort)
}

func probeAntigravityModels(executable string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, executable, "models").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("agy models failed: %s", strings.TrimSpace(string(output)))
	}
	return parseModels(output), nil
}

var modelSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

func parseModels(output []byte) []string {
	models := make([]string, 0)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !modelSlug.MatchString(fields[0]) || slices.Contains(models, fields[0]) {
			continue
		}
		models = append(models, fields[0])
	}
	return models
}

func authenticationUnavailable(err error) bool {
	text := strings.ToLower(err.Error())
	for _, marker := range []string{"authentication required", "not authenticated", "sign in", "credentials", "configuration"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
