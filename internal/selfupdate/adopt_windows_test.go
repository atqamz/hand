//go:build windows

package selfupdate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdoptCanonicalizesWindowsCaseAndExeResolution(t *testing.T) {
	want := BuildInfo{Version: "1.2.3", Channel: ChannelStable, Commit: stableTestCommit, Distribution: DistributionGitHub}
	source := writeIdentityExecutable(t, want, "source")
	targetDir := t.TempDir()
	target := testHandPath(targetDir)
	writeIdentityExecutableAt(t, target, want, "existing")
	t.Setenv("PATH", strings.ToUpper(filepath.Clean(targetDir)))

	got, err := Adopt(context.Background(), source, target, want)
	if err != nil {
		t.Fatalf("Adopt() error = %v", err)
	}
	if got.Path != target || got.Result != "reused" {
		t.Fatalf("Adopt() = %#v, want same-path reuse through case-insensitive .exe PATH resolution", got)
	}
}
