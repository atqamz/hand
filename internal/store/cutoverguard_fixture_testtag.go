//go:build test || e2e

package store

import "context"

func AcquireLegacyV18CutoverGuardFixture(ctx context.Context, homeDir string) (*LegacyV18CutoverGuard, error) {
	evidence, err := readLegacyV18CutoverBootEvidence(legacyV18CutoverPlatform())
	if err != nil {
		return nil, err
	}
	return acquireLegacyV18CutoverGuardForFixture(ctx, homeDir, evidence)
}
