//go:build !windows

package selfupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdoptPreservesDirectInstallWhenHardLinksAreUnavailable(t *testing.T) {
	isolateHandPath(t)
	want := BuildInfo{Version: "1.2.3", Channel: ChannelStable, Commit: stableTestCommit, Distribution: DistributionGitHub}
	source := writeIdentityExecutable(t, want, "new-source")
	target := testHandPath(t.TempDir())
	writeIdentityExecutableAt(t, target, BuildInfo{Version: "1.0.0", Channel: ChannelStable, Commit: stableTestCommit, Distribution: DistributionGitHub}, "old-target")

	originalLink := linkFile
	linkFile = func(string, string) error { return errors.New("hard links unavailable") }
	t.Cleanup(func() { linkFile = originalLink })

	if _, err := Adopt(context.Background(), source, target, want); err != nil {
		t.Fatalf("Adopt() error = %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("selected Hand missing: %v", err)
	}
	if err := verifyExecutableBuildInfoDefault(context.Background(), target, want); err != nil {
		t.Fatalf("selected Hand identity: %v", err)
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(target), ".hand-adopt-backup-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Fatalf("rollback backups = %v, want none after commit", backups)
	}
}

func TestAdoptRefusesAParseableLegacyVersionWithoutBuildInfo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	target := filepath.Join(home, ".local", "bin", "hand")
	legacy := "#!/bin/sh\ncase \"$1\" in\n  --version) printf 'hand 1.2.3\\n' ;;\n  *) exit 1 ;;\nesac\n"
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	want := BuildInfo{Version: "1.2.3", Channel: ChannelStable, Commit: stableTestCommit, Distribution: DistributionGitHub}
	source := writeIdentityExecutable(t, want, "source")
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Adopt(context.Background(), source, target, want)
	if err == nil || !strings.Contains(err.Error(), "no verifiable build identity") {
		t.Fatalf("Adopt() error = %v, want legacy identity refusal", err)
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("legacy executable changed after identity refusal")
	}
}

func TestAdoptCanonicalizesRelativeSymlinkedPathToTheSameExecutable(t *testing.T) {
	want := BuildInfo{Version: "1.2.3", Channel: ChannelStable, Commit: stableTestCommit, Distribution: DistributionGitHub}
	source := writeIdentityExecutable(t, want, "source")
	root := t.TempDir()
	targetDir := filepath.Join(root, "bin with spaces", "unicode-é")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(targetDir, "hand")
	writeIdentityExecutableAt(t, target, want, "existing")
	if err := os.Symlink(targetDir, filepath.Join(root, "relative-link")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("PATH", filepath.Join(".", "relative-link"))

	got, err := Adopt(context.Background(), source, target, want)
	if err != nil {
		t.Fatalf("Adopt() error = %v", err)
	}
	if got.Path != target || got.Result != "reused" {
		t.Fatalf("Adopt() = %#v, want canonical same-file reuse", got)
	}
}
