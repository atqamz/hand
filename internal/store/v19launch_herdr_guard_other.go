//go:build !linux

package store

import "context"

func reconcileCanonicalV19HerdrGuardLaunch(
	context.Context, string, canonicalV19HerdrLaunchCurrent, canonicalV19HerdrLaunchDeps,
) (string, bool, error) {
	return "", false, nil
}

func reconcileCanonicalV19HerdrGuardInterrupt(
	context.Context, string, canonicalV19HerdrInterruptCurrent, canonicalV19HerdrInterruptDeps,
) (string, bool, error) {
	return "", false, nil
}

func settleCanonicalV19ExecGuardFiles(string, string) error {
	return nil
}
