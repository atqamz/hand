package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/atqamz/hand/internal/atomicfile"
)

const cacheFile = ".version-check"

const checkInterval = 24 * time.Hour

const checkTimeout = 2 * time.Second

type versionCache struct {
	CheckedAt time.Time `json:"checked_at"`
	Channel   string    `json:"channel,omitempty"`
	Latest    string    `json:"latest"`
	Commit    string    `json:"commit,omitempty"`
}

// CheckNoticeForBuild returns a one-line stderr notice when a newer hand release is available on the
// build's channel, or "" when up to date or when the check can't be completed. Bounded by checkTimeout
// and never fails the caller: startup version checks are non-blocking and non-fatal.
func CheckNoticeForBuild(home, repo string, info BuildInfo) string {
	info = NormalizeBuildInfo(info.Version, info.Channel, info.Commit)
	if info.Channel == ChannelDev {
		return ""
	}
	// A version the comparison can never accept costs a gh call per interval otherwise.
	if info.Channel == ChannelStable {
		if _, _, _, err := parseSemver(info.Version); err != nil {
			return ""
		}
	}

	stateDir := filepath.Join(home, "state")
	if _, err := os.Stat(stateDir); err != nil {
		return ""
	}
	cachePath := filepath.Join(stateDir, cacheFile)

	now := time.Now()
	var target Target
	if cache, err := readCache(cachePath); err == nil && now.Sub(cache.CheckedAt) < checkInterval && cacheChannel(cache) == info.Channel {
		target = cachedTarget(info.Channel, cache.Latest, cache.Commit)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
		defer cancel()
		resolved, err := resolveTarget(ctx, repo, info.Channel)
		// Cache failures too, so an unreachable or black-holed network costs
		// one checkTimeout stall per interval instead of one per command.
		_ = writeCache(cachePath, versionCache{CheckedAt: now, Channel: info.Channel, Latest: resolved.Version, Commit: resolved.Commit})
		if err != nil {
			return ""
		}
		target = resolved
	}

	newer, err := NeedsUpdate(info, target)
	if err != nil || !newer {
		return ""
	}
	if info.Channel == ChannelEdge {
		return fmt.Sprintf("A new edge build of hand is available: %s -> %s\nRun \"hand update\" to update", DisplayCommit(shortCommit(info.Commit)), DisplayCommit(shortCommit(target.Commit)))
	}
	return fmt.Sprintf("A new version of hand is available: %s -> %s\nRun \"hand update\" to update", info.Version, target.Version)
}

// ReconcileNotice rewrites the notice cache for home from an authoritative live
// resolution, so a startup notice can never keep advertising a target a live
// check has already superseded.
func ReconcileNotice(home string, target Target) error {
	stateDir := filepath.Join(home, "state")
	if _, err := os.Stat(stateDir); err != nil {
		return nil
	}
	cachePath := filepath.Join(stateDir, cacheFile)
	return writeCache(cachePath, versionCache{
		CheckedAt: time.Now(),
		Channel:   target.Channel,
		Latest:    target.Version,
		Commit:    target.Commit,
	})
}

func cacheChannel(cache versionCache) string {
	if cache.Channel == "" {
		return ChannelStable
	}
	return cache.Channel
}

func readCache(path string) (versionCache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return versionCache{}, err
	}
	var cache versionCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return versionCache{}, err
	}
	return cache, nil
}

func writeCache(path string, cache versionCache) error {
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	return atomicfile.Write(path, ".version-check-", data, 0o644)
}
