package fsidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
)

// directoryDigestDomain intentionally matches the physical-identity digest
// already materialized into canonical v19 WorkspaceBinding rows by cutover.
// WorktreeBinding and WorkspaceBinding digests are compared directly by the
// locked v19 DDL, so they must stay in one digest domain.
const directoryDigestDomain = "hand:v19-cutover:common-git-dir-identity:v1"

// DirectoryDigest returns stable physical identity evidence for one direct
// directory. Paths are locators only; the digest is derived from platform file
// identity and is rechecked against the same object before returning.
func DirectoryDigest(path string) (string, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect directory identity: %w", err)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return "", fmt.Errorf("directory identity target is not a direct directory")
	}
	raw, err := rawIdentity(path, before)
	if err != nil {
		return "", err
	}
	after, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("reinspect directory identity: %w", err)
	}
	if !os.SameFile(before, after) {
		return "", fmt.Errorf("directory identity changed during capture")
	}
	sum := sha256.Sum256([]byte(directoryDigestDomain + "\x00" + raw))
	return hex.EncodeToString(sum[:]), nil
}
