package toolchain

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedLockHasCompleteTargets(t *testing.T) {
	lock, err := LoadLock()
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Validate(); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64"} {
		entry, ok := lock.Targets[target]
		if !ok {
			t.Fatalf("missing target %s", target)
		}
		if entry.Unsupported != "" {
			if len(entry.Components) != 0 {
				t.Fatalf("unsupported target %s also defines components", target)
			}
			continue
		}
		for _, name := range []string{"git", "treehouse", "herdr"} {
			component, ok := entry.Components[name]
			if !ok {
				t.Fatalf("target %s missing component %s", target, name)
			}
			if component.URL == "" || component.SHA256 == "" || component.Version == "" || component.Revision == "" {
				t.Fatalf("target %s component %s lacks immutable acquisition metadata", target, name)
			}
			if len(component.Files) == 0 {
				t.Fatalf("target %s component %s has no expected files", target, name)
			}
		}
	}
}

func TestLockValidateRejectsUnsafeComponentFormats(t *testing.T) {
	base, err := LoadLock()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		format string
		files  []ExpectedFile
	}{
		{name: "binary without exactly one file", format: "binary", files: []ExpectedFile{{Path: "git", Regular: true}, {Path: "git-extra", Regular: true}}},
		{name: "unsupported format", format: "msi", files: []ExpectedFile{{Path: "git", Regular: true}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lock := base
			target := lock.Targets["linux/amd64"]
			component := target.Components["git"]
			component.Format = test.format
			component.Files = test.files
			target.Components["git"] = component
			lock.Targets["linux/amd64"] = target
			lock.RuntimeID, err = lock.DeterministicID()
			if err != nil {
				t.Fatal(err)
			}
			if err := lock.Validate(); err == nil {
				t.Fatal("unsafe component format was accepted")
			}
		})
	}
}

func TestProcessSpecRequiresAbsoluteExecutable(t *testing.T) {
	if _, err := NewProcessSpec("git", nil, nil); err == nil {
		t.Fatal("bare executable was accepted")
	}
}

func TestManagedEnvironmentPrependsGitBinWithoutDroppingUserEnvironment(t *testing.T) {
	managed := filepath.Join(t.TempDir(), "private", "runtime", "git", "bin")
	oldPath := filepath.Join(t.TempDir(), "machine", "bin")
	env, err := ManagedEnvironment([]string{
		"HOME=/user/home",
		"USERPROFILE=C:\\Users\\user",
		"PATH=" + oldPath,
		"SSH_AUTH_SOCK=/tmp/agent.sock",
		"EMPTY=",
	}, managed)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\x00")
	for _, want := range []string{"HOME=/user/home", "USERPROFILE=C:\\Users\\user", "SSH_AUTH_SOCK=/tmp/agent.sock", "EMPTY="} {
		if !strings.Contains(joined, want) {
			t.Fatalf("managed environment dropped %q: %v", want, env)
		}
	}
	if got := valueFor(env, "PATH"); got != managed+string(filepath.ListSeparator)+oldPath {
		t.Fatalf("PATH = %q, want %q", got, managed+string(filepath.ListSeparator)+oldPath)
	}
}

func valueFor(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}
