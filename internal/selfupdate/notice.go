package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const cacheFile = ".version-check"

const checkInterval = 24 * time.Hour

const checkTimeout = 2 * time.Second

type versionCache struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

// CheckNotice returns a one-line stderr notice when a newer hand release is
// available, or "" when up to date or when the check can't be completed. It
// is bounded by checkTimeout and never fails the caller: startup version
// checks must be non-blocking and non-fatal per SPECS.md.
func CheckNotice(home, repo, currentVersion string) string {
	stateDir := filepath.Join(home, "state")
	if _, err := os.Stat(stateDir); err != nil {
		return ""
	}
	cachePath := filepath.Join(stateDir, cacheFile)

	now := time.Now()
	latest := ""
	if cache, err := readCache(cachePath); err == nil && now.Sub(cache.CheckedAt) < checkInterval {
		latest = cache.Latest
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
		defer cancel()
		tag, err := latestTag(ctx, repo)
		if err != nil {
			return ""
		}
		latest = tag
		_ = writeCache(cachePath, versionCache{CheckedAt: now, Latest: tag})
	}

	newer, err := IsNewer(latest, currentVersion)
	if err != nil || !newer {
		return ""
	}
	return fmt.Sprintf("A new version of hand is available: %s -> %s\nRun \"hand update\" to update", currentVersion, latest)
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
	return os.WriteFile(path, data, 0o644)
}
