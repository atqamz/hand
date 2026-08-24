package cmd

import "github.com/atqamz/hand/internal/selfupdate"

func directStableBuild(version string) selfupdate.BuildInfo {
	return selfupdate.BuildInfo{
		Version:      version,
		Channel:      selfupdate.ChannelStable,
		Distribution: selfupdate.DistributionGitHub,
	}
}
