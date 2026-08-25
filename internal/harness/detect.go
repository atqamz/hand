package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
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
	ExecutablePath string
}

const runtimeVersionTimeout = 2 * time.Second

var runtimeVersionCommand = func(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), runtimeVersionTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, "--version").Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return string(output), err
}

type processInfo struct {
	name       string
	args       string
	executable string
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
	path := detection.ExecutablePath
	if path == "" {
		path, _ = exec.LookPath(Executable(detection.Name))
	}
	if path == "" {
		return detection
	}
	output, err := runtimeVersionCommand(path)
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
			return Detection{Name: name, Source: "process", ExecutablePath: processExecutable(process, name)}, nil
		}
	}
	if name := markerHarness(env); name != "" {
		return Detection{Name: name, Source: "environment"}, nil
	}
	return Detection{Source: "unknown"}, nil
}

func processExecutable(process processInfo, name string) string {
	if process.executable != "" && filepath.Base(process.executable) == name {
		return process.executable
	}
	for _, argument := range strings.Fields(process.args) {
		if filepath.Base(argument) == name && strings.Contains(argument, string(os.PathSeparator)) {
			return argument
		}
	}
	return ""
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
			name:       filepath.Base(fields[1]),
			args:       strings.Join(fields[2:], " "),
			executable: processExecutablePath(pid),
		})
		pid = parent
	}
	return ancestors
}

func processExecutablePath(pid int) string {
	if runtime.GOOS != "linux" {
		return ""
	}
	path, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return ""
	}
	return path
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
