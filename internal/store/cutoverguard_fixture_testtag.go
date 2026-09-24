//go:build test || e2e

package store

import "context"

func AcquireLegacyV18CutoverGuardFixture(ctx context.Context, homeDir string) (*LegacyV18CutoverGuard, error) {
	return acquireLegacyV18CutoverGuardForFixture(ctx, homeDir)
}
