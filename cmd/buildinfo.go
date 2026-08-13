package cmd

import "github.com/atqamz/hand/internal/selfupdate"

func legacyBuildInfo(version string) selfupdate.BuildInfo {
	channel := selfupdate.ChannelStable
	if _, err := selfupdate.IsNewer(version, version); err != nil {
		channel = selfupdate.ChannelDev
	}
	return selfupdate.NormalizeBuildInfo(version, channel, "")
}

func buildInfo(version, channel, commit string) selfupdate.BuildInfo {
	return selfupdate.NormalizeBuildInfo(version, channel, commit)
}
