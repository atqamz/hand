//go:build !linux

package store

import "context"

func wakeCanonicalV19HerdrGuarded(
	context.Context, string, CanonicalV19WorkerWakePrepareInput, canonicalV19HerdrWorkerWakeDeps,
) (string, error) {
	return "", canonicalV19HerdrCapabilityUnsupported("WorkerWake", "exact live execution identity")
}
