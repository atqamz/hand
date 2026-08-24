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
	Contract func(string) error
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

	contract := probe.Contract
	if contract == nil {
		contract = probeAntigravityContract
	}
	if err := contract(path); err != nil {
		result.State = CapabilityUnknown
		result.Reason = "headless worker contract could not be verified"
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

// ValidateRuntime verifies the installed worker contract even when no explicit model is selected.
// Routing calls this before any Attempt/worktree/session side effect for Antigravity.
func ValidateRuntime(name string) error {
	return ValidateRuntimeWithProbe(name, CapabilityProbe{})
}

func ValidateRuntimeWithProbe(name string, probe CapabilityProbe) error {
	if name != Antigravity {
		return nil
	}
	return capabilityError(name, Inspect(name, probe))
}

func ValidateModel(name, model string) error {
	return ValidateModelWithProbe(name, model, CapabilityProbe{})
}

func ValidateModelWithProbe(name, model string, probe CapabilityProbe) error {
	if name != Antigravity || model == "" {
		return nil
	}
	capability := Inspect(name, probe)
	if err := capabilityError(name, capability); err != nil {
		return err
	}
	if !slices.Contains(capability.Models, model) {
		return fmt.Errorf("harness %q does not support model %q", name, model)
	}
	return nil
}

func capabilityError(name string, capability Capability) error {
	switch capability.State {
	case CapabilityUnavailable:
		return fmt.Errorf("harness %q is unavailable: %s", name, capability.Reason)
	case CapabilityUnsupported:
		return fmt.Errorf("harness %q is unsupported: %s", name, capability.Reason)
	case CapabilityUnknown:
		return fmt.Errorf("harness %q capability is unknown: %s", name, capability.Reason)
	case CapabilityReady:
		return nil
	default:
		return fmt.Errorf("harness %q capability is unknown", name)
	}
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

func probeAntigravityContract(executable string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, executable, "--help").CombinedOutput()
	if err != nil {
		return fmt.Errorf("agy --help failed: %s", strings.TrimSpace(string(output)))
	}
	text := string(output)
	for _, marker := range []string{"--print", "--model", "--effort", "--output-format", "stream-json", "--print-timeout", "--dangerously-skip-permissions"} {
		if !strings.Contains(text, marker) {
			return fmt.Errorf("agy --help does not advertise required worker capability %q", marker)
		}
	}
	return nil
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
		if len(fields) == 0 || fields[0] == "model" || !modelSlug.MatchString(fields[0]) || slices.Contains(models, fields[0]) {
			continue
		}
		models = append(models, fields[0])
	}
	return models
}

func authenticationUnavailable(err error) bool {
	text := strings.ToLower(err.Error())
	for _, marker := range []string{"authentication required", "not authenticated", "sign in", "sign-in", "credentials unavailable", "missing credentials", "configuration required"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
