package cmd

import (
	"bytes"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/selfupdate"
)

const edgeCommandCommit = "0123456789abcdef0123456789abcdef01234567"

func writeFakeGHChannels(t *testing.T, stable, edge string) {
	t.Helper()
	faketool.GH{Releases: []faketool.GHRelease{{StableTag: stable, EdgeCommit: edge}}}.Install(t, faketool.Bin(t))
}

func writeFakeGHEdgeUpdate(t *testing.T, commit, notes, fixtureDir string) {
	t.Helper()
	faketool.GH{Releases: []faketool.GHRelease{{EdgeCommit: commit, EdgeNotes: notes, EdgeAssetsDir: fixtureDir}}}.Install(t, faketool.Bin(t))
}

func TestUpdateCheckFollowsEmbeddedEdgeChannel(t *testing.T) {
	writeFakeGHChannels(t, "v0.5.0", edgeCommandCommit)

	cmd := newUpdateCmdWithBuildInfo(selfupdate.BuildInfo{
		Version: "edge.aaaaaaaaaaaa",
		Channel: selfupdate.ChannelEdge,
		Commit:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"current: edge.aaaaaaaaaaaa\n",
		"current_channel: edge\n",
		"current_commit: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n",
		"latest: edge.0123456789ab\n",
		"latest_channel: edge\n",
		"latest_commit: " + edgeCommandCommit + "\n",
		"update_available: true\n",
		"updated: false\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output = %q, want %q", out.String(), want)
		}
	}
}

func TestUpdateCheckExplicitlySwitchesStableToEdge(t *testing.T) {
	writeFakeGHChannels(t, "v0.5.0", edgeCommandCommit)

	cmd := newUpdateCmdWithBuildInfo(selfupdate.BuildInfo{Version: "v0.9.0", Channel: selfupdate.ChannelStable})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--check", "--channel", selfupdate.ChannelEdge})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "latest_channel: edge\n") || !strings.Contains(out.String(), "update_available: true\n") {
		t.Fatalf("output = %q, want explicit edge switch", out.String())
	}
}

func TestUpdateCheckEdgeWithMatchingCommitIsUpToDate(t *testing.T) {
	writeFakeGHChannels(t, "v0.5.0", edgeCommandCommit)

	cmd := newUpdateCmdWithBuildInfo(selfupdate.BuildInfo{
		Version: "edge.0123456789ab",
		Channel: selfupdate.ChannelEdge,
		Commit:  edgeCommandCommit,
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "update_available: false\n") {
		t.Fatalf("output = %q, want no edge update", out.String())
	}
}

func TestUpdateInstallsEdgeAssetThroughSharedApplyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake gh is a POSIX shell script")
	}
	execPath := setFakeExecutable(t)
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	fixture := buildUpdateFixture(t, []byte("new edge binary contents"))
	writeFakeGHEdgeUpdate(t, edgeCommandCommit, "edge notes", fixture)

	cmd := newUpdateCmdWithBuildInfo(selfupdate.BuildInfo{
		Version: "edge.aaaaaaaaaaaa",
		Channel: selfupdate.ChannelEdge,
		Commit:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "latest_channel: edge\n") || !strings.Contains(out.String(), "updated: true\n") {
		t.Fatalf("output = %q, want installed edge update", out.String())
	}
	installed, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(installed) != "new edge binary contents" {
		t.Fatalf("installed binary = %q, want edge asset contents", installed)
	}
}

func TestUpdateRejectsInvalidChannelAsUsageError(t *testing.T) {
	cmd := newUpdateCmdWithBuildInfo(selfupdate.BuildInfo{Version: "v0.1.0", Channel: selfupdate.ChannelStable})
	cmd.SetArgs([]string{"--check", "--channel", "nightly"})
	if code := exitCodeFor(t, cmd.Execute()); code != 2 {
		t.Fatalf("exit code = %d, want usage code 2", code)
	}
}
