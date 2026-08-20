package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/selfupdate"
)

func TestUpdateRefusesToMutatePackageManagerOwnedBuilds(t *testing.T) {
	cases := []struct {
		distribution string
		command      string
	}{
		{selfupdate.DistributionBrew, "brew upgrade atqamz/tap/hand"},
		{selfupdate.DistributionWinget, "winget upgrade Atqamz.Hand"},
		{selfupdate.DistributionNpm, "npm update -g @atqamz/hand"},
		{selfupdate.DistributionNix, "nix profile upgrade hand"},
		{selfupdate.DistributionDeb, selfupdate.UpgradeCommand(selfupdate.DistributionDeb)},
		{selfupdate.DistributionRpm, selfupdate.UpgradeCommand(selfupdate.DistributionRpm)},
		{selfupdate.DistributionAur, selfupdate.UpgradeCommand(selfupdate.DistributionAur)},
	}

	for _, test := range cases {
		t.Run(test.distribution, func(t *testing.T) {
			t.Setenv("HAND_HOME", "")
			execPath := setFakeExecutable(t)
			writeFakeGHReleaseView(t, "v0.5.0")

			cmd := newUpdateCmd(selfupdate.BuildInfo{Version: "v0.1.0", Channel: selfupdate.ChannelStable, Distribution: test.distribution})
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs(nil)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}

			got := out.String()
			for _, want := range []string{
				"distribution: " + test.distribution + "\n",
				"update_available: true\n",
				"updated: false\n",
				"hand will not replace a package-manager-owned build; " + test.command + "\n",
			} {
				if !strings.Contains(got, want) {
					t.Fatalf("output = %q, want it to contain %q", got, want)
				}
			}

			installed, err := os.ReadFile(execPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(installed) != "old binary contents" {
				t.Fatalf("executable = %q, want it left untouched", installed)
			}
		})
	}
}

func TestUpdateRefusesGoAndSourceBuildsWithTheirOwnUpgradeCommand(t *testing.T) {
	cases := []struct {
		distribution string
		want         string
	}{
		{selfupdate.DistributionGo, "hand will not replace a go build; go install github.com/atqamz/hand@latest"},
		{selfupdate.DistributionSource, "hand will not replace a source build; rebuild from source: git pull && make build"},
	}

	for _, test := range cases {
		t.Run(test.distribution, func(t *testing.T) {
			t.Setenv("HAND_HOME", "")
			execPath := setFakeExecutable(t)
			writeFakeGHReleaseView(t, "v0.5.0")

			cmd := newUpdateCmd(selfupdate.BuildInfo{Version: "v0.1.0", Channel: selfupdate.ChannelStable, Distribution: test.distribution})
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs(nil)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(out.String(), test.want+"\n") {
				t.Fatalf("output = %q, want it to contain %q", out.String(), test.want)
			}
			installed, err := os.ReadFile(execPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(installed) != "old binary contents" {
				t.Fatalf("executable = %q, want it left untouched", installed)
			}
		})
	}
}

func TestUpdateStillSelfUpdatesExplicitlyHandOwnedDistributions(t *testing.T) {
	for _, distribution := range []string{selfupdate.DistributionGitHub, selfupdate.DistributionInstallScript} {
		t.Run(distribution, func(t *testing.T) {
			execPath := setFakeExecutable(t)
			home := t.TempDir()
			t.Chdir(home)
			mkFleetDirs(t, home)

			fixture := buildUpdateFixture(t, []byte("new binary contents"))
			writeFakeGHUpdate(t, "v0.5.0", "notes", fixture)

			cmd := newUpdateCmd(selfupdate.BuildInfo{Version: "v0.1.0", Channel: selfupdate.ChannelStable, Distribution: distribution})
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs(nil)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "updated: true\n") {
				t.Fatalf("output = %q, want a completed update", out.String())
			}
			installed, err := os.ReadFile(execPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(installed) != "new binary contents" {
				t.Fatalf("executable = %q, want the new release installed", installed)
			}
		})
	}
}

func TestUpdateCheckReportsAvailabilityWithoutRefusingAPackageManagerOwnedBuild(t *testing.T) {
	t.Setenv("HAND_HOME", "")
	writeFakeGHReleaseView(t, "v0.5.0")

	cmd := newUpdateCmd(selfupdate.BuildInfo{Version: "v0.1.0", Channel: selfupdate.ChannelStable, Distribution: selfupdate.DistributionBrew})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if !strings.Contains(got, "update_available: true\n") || !strings.Contains(got, "distribution: brew\n") {
		t.Fatalf("output = %q, want an available-update report naming the distribution", got)
	}
	if strings.Contains(got, "hand will not replace") {
		t.Fatalf("output = %q, want --check to report availability without an ownership refusal", got)
	}
}
