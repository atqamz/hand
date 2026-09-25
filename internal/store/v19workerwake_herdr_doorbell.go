package store

import (
	"crypto/sha256"
	"encoding/hex"
)

const canonicalV19HerdrWorkerWakeDoorbell = "| hand wake - canonical worker input is pending - run hand runtime worker-input drain and acknowledge each input you observed"

// CanonicalV19HerdrWorkerWakeDoorbellDigest returns the doorbell_digest every Herdr WorkerWake
// persists: the doorbell is one constant, so the digest is too.
func CanonicalV19HerdrWorkerWakeDoorbellDigest() string {
	digest := sha256.Sum256([]byte(canonicalV19HerdrWorkerWakeDoorbell))
	return hex.EncodeToString(digest[:])
}
