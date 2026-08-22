package herdr

import "testing"

func TestProcessInfoHasExecutableNormalizesWindowsSuffix(t *testing.T) {
	info := ProcessInfo{ForegroundProcesses: []Process{{
		Name:  "claude.exe",
		Argv0: `C:\Program Files\Claude\claude.exe`,
	}}}

	if !info.HasExecutable("claude") {
		t.Fatal("HasExecutable() = false, want true for extensionless executable")
	}
}
