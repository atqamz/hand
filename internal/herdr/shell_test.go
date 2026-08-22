package herdr

import (
	"strings"
	"testing"
)

func TestRenderPOSIXPreservesLiteralArguments(t *testing.T) {
	got, err := renderPOSIX("worker name", []string{"spaces and 'quotes'", "$HOME; echo unsafe", "--leading", "日本語"})
	if err != nil {
		t.Fatal(err)
	}
	want := `'worker name' 'spaces and '\''quotes'\''' '$HOME; echo unsafe' '--leading' '日本語'`
	if got != want {
		t.Fatalf("renderPOSIX() = %q, want %q", got, want)
	}
}

func TestRenderPowerShellPreservesLiteralArguments(t *testing.T) {
	got, err := renderPowerShell("worker name", []string{"spaces and 'quotes'", "$HOME; echo unsafe", "--leading", "日本語"})
	if err != nil {
		t.Fatal(err)
	}
	want := `& 'worker name' 'spaces and ''quotes''' '$HOME; echo unsafe' '--leading' '日本語'`
	if got != want {
		t.Fatalf("renderPowerShell() = %q, want %q", got, want)
	}
}

func TestRenderersRejectControlCharacters(t *testing.T) {
	for _, render := range []struct {
		name string
		fn   func(string, []string) (string, error)
	}{
		{name: "posix", fn: renderPOSIX},
		{name: "powershell", fn: renderPowerShell},
	} {
		t.Run(render.name, func(t *testing.T) {
			for _, input := range []struct {
				name string
				exec string
				args []string
			}{
				{name: "executable", exec: "worker\n"},
				{name: "argument", exec: "worker", args: []string{"value\x00"}},
			} {
				t.Run(input.name, func(t *testing.T) {
					if _, err := render.fn(input.exec, input.args); err == nil {
						t.Fatal("renderer accepted a control character")
					}
				})
			}
		})
	}
}

func TestRenderersDoNotRenderCwdOrEnvironment(t *testing.T) {
	for _, render := range []struct {
		name string
		fn   func(string, []string) (string, error)
	}{
		{name: "posix", fn: renderPOSIX},
		{name: "powershell", fn: renderPowerShell},
	} {
		got, err := render.fn("worker", []string{"arg"})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "HAND_HOME") || strings.Contains(got, "PATH=") || strings.Contains(got, "cwd") {
			t.Fatalf("%s renderer included transport metadata: %q", render.name, got)
		}
	}
}

func TestShellForProcessRequiresAuthoritativeShellEvidence(t *testing.T) {
	tests := []struct {
		name string
		info ProcessInfo
		want shellFamily
		fail string
	}{
		{
			name: "posix basename",
			info: ProcessInfo{ShellPID: 7, ForegroundProcesses: []Process{{PID: 7, Name: "/bin/bash"}}},
			want: shellPOSIX,
		},
		{
			name: "login shell",
			info: ProcessInfo{ShellPID: 7, ForegroundProcesses: []Process{{PID: 7, Name: "-zsh"}}},
			want: shellPOSIX,
		},
		{
			name: "powershell argv0 fallback",
			info: ProcessInfo{ShellPID: 7, ForegroundProcesses: []Process{{PID: 7, Argv0: `C:\\Windows\\System32\\pwsh.exe`}}},
			want: shellPowerShell,
		},
		{
			name: "missing shell pid",
			info: ProcessInfo{ForegroundProcesses: []Process{{PID: 7, Name: "bash"}}},
			fail: "no shell pid",
		},
		{
			name: "shell pid not observed",
			info: ProcessInfo{ShellPID: 8, ForegroundProcesses: []Process{{PID: 7, Name: "bash"}}},
			fail: "no matching foreground process",
		},
		{
			name: "unsupported shell",
			info: ProcessInfo{ShellPID: 7, ForegroundProcesses: []Process{{PID: 7, Name: "cmd.exe"}}},
			fail: "unsupported shell",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := shellForProcess(test.info)
			if test.fail != "" {
				if err == nil || !strings.Contains(err.Error(), test.fail) {
					t.Fatalf("shellForProcess() = %v, want error containing %q", err, test.fail)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("shellForProcess() = %q, %v, want %q", got, err, test.want)
			}
		})
	}
}
