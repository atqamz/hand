//go:build !linux

package store

import "context"

// LaunchCanonicalV19Herdr refuses before any operation row: no exec guard proves execution
// identity and positive cessation on this platform yet.
func LaunchCanonicalV19Herdr(context.Context, string, CanonicalV19LaunchPrepareInput) (string, error) {
	return "", canonicalV19HerdrCapabilityUnsupported("Launch", "exact executable-object and never-reused execution-incarnation identity")
}

func reconcileCanonicalV19HerdrGuardLaunch(
	context.Context, string, canonicalV19HerdrLaunchCurrent, canonicalV19HerdrLaunchDeps,
) (string, bool, error) {
	return "", false, nil
}

func settleCanonicalV19ExecGuardFiles(string, string) error {
	return nil
}
