package herdr

import (
	"fmt"
	"strings"
)

type shellFamily string

const (
	shellPOSIX      shellFamily = "POSIX"
	shellPowerShell shellFamily = "PowerShell"
)

func shellForProcess(info ProcessInfo) (shellFamily, error) {
	if info.ShellPID <= 0 {
		return "", fmt.Errorf("unknown shell: pane process info has no shell pid")
	}
	for _, process := range info.ForegroundProcesses {
		if process.PID != info.ShellPID {
			continue
		}
		name := process.Name
		if name == "" {
			name = process.Argv0
		}
		name = shellProcessBase(name)
		switch name {
		case "sh", "bash", "zsh":
			return shellPOSIX, nil
		case "powershell", "powershell.exe", "pwsh", "pwsh.exe":
			return shellPowerShell, nil
		default:
			return "", fmt.Errorf("unsupported shell %q", name)
		}
	}
	return "", fmt.Errorf("unknown shell: shell pid %d has no matching foreground process", info.ShellPID)
}

func shellProcessBase(name string) string {
	return strings.ToLower(processBase(name))
}
