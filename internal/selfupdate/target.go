package selfupdate

import (
	"context"
	"fmt"
	"strings"
)

const (
	ChannelDev    = "dev"
	ChannelStable = "stable"
	ChannelEdge   = "edge"
)

type BuildInfo struct {
	Version      string
	Channel      string
	Commit       string
	Distribution string
}

type Target struct {
	Channel string
	Tag     string
	Version string
	Commit  string
}

func NormalizeBuildInfo(version, channel, commit, distribution string) BuildInfo {
	if channel != ChannelStable && channel != ChannelEdge {
		channel = ChannelDev
	}
	if distribution == "" {
		distribution = detectDistribution()
	}
	return BuildInfo{Version: version, Channel: channel, Commit: commit, Distribution: distribution}
}

func ResolveTarget(repo, channel string) (Target, error) {
	return resolveTarget(context.Background(), repo, channel)
}

// DisplayCommit renders an embedded or resolved commit for output, naming the
// absence of one rather than leaving an empty field.
func DisplayCommit(commit string) string {
	if commit == "" {
		return "unknown"
	}
	return commit
}

// Rebuilds a Target from remembered values. Only edge carries a tag that differs
// from its version, and it is always the rolling tag itself.
func cachedTarget(channel, version, commit string) Target {
	tag := version
	if channel == ChannelEdge {
		tag = ChannelEdge
	}
	return Target{Channel: channel, Tag: tag, Version: version, Commit: commit}
}

func resolveTarget(ctx context.Context, repo, channel string) (Target, error) {
	if err := validateChannel(channel); err != nil {
		return Target{}, err
	}

	switch channel {
	case ChannelStable:
		tag, err := latestTag(ctx, repo)
		if err != nil {
			return Target{}, err
		}
		return Target{Channel: ChannelStable, Tag: tag, Version: tag}, nil
	case ChannelEdge:
		commit, err := edgeCommit(ctx, repo)
		if err != nil {
			return Target{}, err
		}
		if !validCommit(commit) {
			return Target{}, fmt.Errorf("invalid edge commit %q", commit)
		}
		return Target{
			Channel: ChannelEdge,
			Tag:     ChannelEdge,
			Version: "edge." + shortCommit(commit),
			Commit:  commit,
		}, nil
	}
	return Target{}, fmt.Errorf("invalid release channel %q", channel)
}

func NeedsUpdate(current BuildInfo, target Target) (bool, error) {
	if err := validateChannel(target.Channel); err != nil {
		return false, err
	}
	current = NormalizeBuildInfo(current.Version, current.Channel, current.Commit, current.Distribution)
	if current.Channel != target.Channel {
		return true, nil
	}

	switch target.Channel {
	case ChannelStable:
		return IsNewer(target.Version, current.Version)
	case ChannelEdge:
		if !validCommit(target.Commit) {
			return false, fmt.Errorf("invalid edge commit %q", target.Commit)
		}
		return !validCommit(current.Commit) || !strings.EqualFold(current.Commit, target.Commit), nil
	default:
		return false, fmt.Errorf("invalid release channel %q", target.Channel)
	}
}

func validateChannel(channel string) error {
	if channel != ChannelStable && channel != ChannelEdge {
		return fmt.Errorf("invalid release channel %q", channel)
	}
	return nil
}

func edgeCommit(ctx context.Context, repo string) (string, error) {
	out, err := runGH(ctx, "api", "repos/"+repo+"/commits/edge", "--jq", ".sha")
	if err != nil {
		return "", fmt.Errorf("query edge commit: %w", err)
	}
	if out == "" {
		return "", fmt.Errorf("query edge commit: empty SHA")
	}
	return out, nil
}

func validCommit(commit string) bool {
	if len(commit) != 40 && len(commit) != 64 {
		return false
	}
	for _, r := range commit {
		hex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !hex {
			return false
		}
	}
	return true
}

func shortCommit(commit string) string {
	if len(commit) <= 12 {
		return commit
	}
	return commit[:12]
}
