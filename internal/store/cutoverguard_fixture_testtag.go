//go:build test || e2e

package store

import "context"

func AcquireLegacyV18CutoverGuardFixture(ctx context.Context, homeDir string) (*LegacyV18CutoverGuard, error) {
	evidence, err := preflightLegacyV18CutoverFreeze(homeDir, legacyV18CutoverPlatform())
	if err != nil {
		return nil, err
	}
	return acquireLegacyV18CutoverOfflineGuard(ctx, homeDir, evidence)
}
