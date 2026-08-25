package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const maxAncestorDepth = 8

// ps does not exist on native Windows, so the lookup fails without ever
// starting a process that is guaranteed to fail; markerHarness still applies.
var processLookup = func(pid int) ([]byte, error) {
	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf("process ancestry lookup is not supported on windows")
	}
	return exec.Command("ps", "-o", "ppid=,comm=,args=", "-p", strconv.Itoa(pid)).Output()
}

type Detection struct {
	Name           string
	Source         string
	RuntimeVersion string
	APIGeneration  string
	Platform       string
	Capability     string
}

var runtimeVersionCommand = func(name string) (string, error) {
	path, err := exec.LookPath(Executable(name))
	if err != nil {
		return "", err
	}
	output, err := exec.Command(path, "--version").Output()
	return string(output), err
}

type processInfo struct {
	name string
	args string
}

func DetectCurrent() (Detection, error) {
	override := os.Getenv("HAND_HARNESS")
	if override == "unknown" {
		return Detection{Source: "override"}, nil
	}
	ancestors := currentProcessAncestry(os.Getpid(), maxAncestorDepth)
	env := map[string]string{
		"CLAUDECODE":      os.Getenv("CLAUDECODE"),
		"CODEX_THREAD_ID": os.Getenv("CODEX_THREAD_ID"),
		"PI_CODING_AGENT": os.Getenv("PI_CODING_AGENT"),
		"GROK_AGENT":      os.Getenv("GROK_AGENT"),
	}
	detection, err := detectCurrent(override, ancestors, env)
	if err != nil {
		return Detection{}, err
	}
	return enrichDetection(detection), nil
}

func enrichDetection(detection Detection) Detection {
	if detection.Name == "" {
		return detection
	}
	detection.Platform = runtime.GOOS + "/" + runtime.GOARCH
	output, err := runtimeVersionCommand(detection.Name)
	if err == nil {
		detection.RuntimeVersion = parseRuntimeVersion(output)
	}
	return detection
}

func parseRuntimeVersion(output string) string {
	for _, token := range strings.Fields(output) {
		token = strings.Trim(token, "()[],;:")
		if token == "" || token[0] < '0' || token[0] > '9' {
			continue
		}
		if strings.Contains(token, ".") {
			return token
		}
	}
	return ""
}

func detectCurrent(override string, ancestors []processInfo, env map[string]string) (Detection, error) {
	if override == "unknown" {
		return Detection{Source: "override"}, nil
	}
	if override != "" {
		if !IsSupported(override) {
			return Detection{}, fmt.Errorf("unsupported harness override %q", override)
		}
		return Detection{Name: override, Source: "override"}, nil
	}
	for _, process := range ancestors {
		if name := processHarness(process); name != "" {
			return Detection{Name: name, Source: "process"}, nil
		}
	}
	if name := markerHarness(env); name != "" {
		return Detection{Name: name, Source: "environment"}, nil
	}
	return Detection{Source: "unknown"}, nil
}

func currentProcessAncestry(pid, depth int) []processInfo {
	ancestors := make([]processInfo, 0, depth)
	for range depth {
		if pid <= 0 {
			break
		}
		output, err := processLookup(pid)
		if err != nil {
			break
		}
		fields := strings.Fields(string(output))
		if len(fields) < 2 {
			break
		}
		parent, err := strconv.Atoi(fields[0])
		if err != nil {
			break
		}
		ancestors = append(ancestors, processInfo{
			name: filepath.Base(fields[1]),
			args: strings.Join(fields[2:], " "),
		})
		pid = parent
	}
	return ancestors
}

func processHarness(process processInfo) string {
	name := filepath.Base(process.name)
	switch {
	case name == Claude:
		return Claude
	case name == Codex || strings.HasPrefix(name, ".codex-"):
		return Codex
	case name == OpenCode:
		return OpenCode
	case name == Grok:
		return Grok
	case name == Pi || strings.HasPrefix(name, "pi-"):
		return Pi
	case name == "node" || name == "python" || name == "python3":
		for _, argument := range strings.Fields(process.args) {
			if filepath.Base(argument) == OpenCode {
				return OpenCode
			}
		}
	}
	return ""
}

func markerHarness(env map[string]string) string {
	switch {
	case env["CLAUDECODE"] == "1":
		return Claude
	case env["CODEX_THREAD_ID"] != "":
		return Codex
	case env["PI_CODING_AGENT"] == "true":
		return Pi
	case env["GROK_AGENT"] == "true":
		return Grok
	}
	return ""
}
