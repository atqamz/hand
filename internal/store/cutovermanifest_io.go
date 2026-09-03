package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func writeLegacyV18CutoverManifest(homeDir string, archive legacyV18CutoverOriginalArchive, input LegacyV18CutoverManifestInput) (legacyV18CutoverManifestArtifact, error) {
	manifest, err := buildLegacyV18CutoverManifest(homeDir, archive, input)
	if err != nil {
		return legacyV18CutoverManifestArtifact{}, err
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		return legacyV18CutoverManifestArtifact{}, fmt.Errorf("encode legacy v18 cutover manifest: %w", err)
	}
	payload = append(payload, '\n')
	artifact := legacyV18CutoverManifestArtifact{
		MigrationID: archive.MigrationID,
		Path:        legacyV18CutoverManifestPath(homeDir, archive.MigrationID),
		SHA256:      canonicalV19SHA256(payload),
		ImportedAt:  manifest.ImportedAt,
	}

	if reused, err := reuseExactLegacyV18CutoverManifest(artifact.Path, payload, artifact.SHA256); err != nil {
		return legacyV18CutoverManifestArtifact{}, err
	} else if reused {
		return artifact, nil
	}

	candidatePath := legacyV18CutoverManifestCandidatePath(homeDir, archive.MigrationID)
	if err := prepareLegacyV18CutoverManifestCandidate(candidatePath, payload, artifact.SHA256); err != nil {
		return legacyV18CutoverManifestArtifact{}, err
	}
	if err := publishLegacyV18CutoverManifest(candidatePath, artifact.Path); err != nil {
		return legacyV18CutoverManifestArtifact{}, fmt.Errorf("publish legacy v18 cutover manifest: %w", err)
	}
	if err := verifyExactLegacyV18CutoverManifest(artifact.Path, payload, artifact.SHA256); err != nil {
		return legacyV18CutoverManifestArtifact{}, err
	}
	if _, err := os.Lstat(candidatePath); !os.IsNotExist(err) {
		if err == nil {
			return legacyV18CutoverManifestArtifact{}, fmt.Errorf("legacy v18 cutover manifest candidate remained after publication")
		}
		return legacyV18CutoverManifestArtifact{}, fmt.Errorf("inspect published legacy v18 cutover manifest candidate: %w", err)
	}
	return artifact, nil
}

func reuseExactLegacyV18CutoverManifest(manifestPath string, payload []byte, digest string) (bool, error) {
	info, err := os.Lstat(manifestPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect legacy v18 cutover manifest: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("legacy v18 cutover manifest %s is not a direct regular file", manifestPath)
	}
	if err := verifyExactLegacyV18CutoverManifest(manifestPath, payload, digest); err != nil {
		return false, fmt.Errorf("existing legacy v18 cutover manifest differs from deterministic evidence: %w", err)
	}
	if err := syncLegacyV18CutoverFile(manifestPath); err != nil {
		return false, fmt.Errorf("flush existing legacy v18 cutover manifest: %w", err)
	}
	if err := syncLegacyV18CutoverDirectoryParent(manifestPath); err != nil {
		return false, fmt.Errorf("flush existing legacy v18 cutover manifest directory: %w", err)
	}
	if err := verifyExactLegacyV18CutoverManifest(manifestPath, payload, digest); err != nil {
		return false, fmt.Errorf("revalidate existing legacy v18 cutover manifest: %w", err)
	}
	return true, nil
}

func prepareLegacyV18CutoverManifestCandidate(candidatePath string, payload []byte, digest string) error {
	if info, err := os.Lstat(candidatePath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("legacy v18 cutover manifest candidate %s is not a direct regular file", candidatePath)
		}
		if err := verifyExactLegacyV18CutoverManifest(candidatePath, payload, digest); err == nil {
			if err := syncLegacyV18CutoverFile(candidatePath); err != nil {
				return fmt.Errorf("flush existing legacy v18 cutover manifest candidate: %w", err)
			}
			return nil
		}
		if err := os.Remove(candidatePath); err != nil {
			return fmt.Errorf("remove mismatched legacy v18 cutover manifest candidate: %w", err)
		}
		if err := syncLegacyV18CutoverDirectoryParent(candidatePath); err != nil {
			return fmt.Errorf("flush removed legacy v18 cutover manifest candidate directory: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect legacy v18 cutover manifest candidate: %w", err)
	}

	file, err := os.OpenFile(candidatePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create legacy v18 cutover manifest candidate: %w", err)
	}
	written, writeErr := file.Write(payload)
	if writeErr == nil && written != len(payload) {
		writeErr = io.ErrShortWrite
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write legacy v18 cutover manifest candidate: %w", writeErr)
	}
	if syncErr != nil {
		return fmt.Errorf("flush legacy v18 cutover manifest candidate: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close legacy v18 cutover manifest candidate: %w", closeErr)
	}
	if err := syncLegacyV18CutoverDirectoryParent(candidatePath); err != nil {
		return fmt.Errorf("flush legacy v18 cutover manifest candidate directory: %w", err)
	}
	return verifyExactLegacyV18CutoverManifest(candidatePath, payload, digest)
}

func verifyExactLegacyV18CutoverManifest(manifestPath string, payload []byte, digest string) error {
	if err := requireLegacyV18CutoverDirectRegularFile(manifestPath, "manifest"); err != nil {
		return err
	}
	got, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read legacy v18 cutover manifest: %w", err)
	}
	if !bytes.Equal(got, payload) {
		return fmt.Errorf("manifest bytes differ from deterministic payload")
	}
	if gotDigest := canonicalV19SHA256(got); gotDigest != digest {
		return fmt.Errorf("manifest digest=%s, want %s", gotDigest, digest)
	}
	return nil
}
