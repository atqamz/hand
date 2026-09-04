package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func writeLegacyV18CutoverMarker(homeDir string, input legacyV18CutoverMarkerInput) (legacyV18CutoverMarkerArtifact, error) {
	marker, err := buildLegacyV18CutoverMarker(homeDir, input)
	if err != nil {
		return legacyV18CutoverMarkerArtifact{}, err
	}
	payload, err := json.Marshal(marker)
	if err != nil {
		return legacyV18CutoverMarkerArtifact{}, fmt.Errorf("encode v19 cutover advisory marker: %w", err)
	}
	payload = append(payload, '\n')
	artifact := legacyV18CutoverMarkerArtifact{
		MigrationID: marker.MigrationID,
		Phase:       marker.Phase,
		Path:        legacyV18CutoverMarkerPath(homeDir),
		SHA256:      canonicalV19SHA256(payload),
	}
	if err := os.MkdirAll(Dir(homeDir), 0o700); err != nil {
		return legacyV18CutoverMarkerArtifact{}, fmt.Errorf("create v19 cutover marker state directory: %w", err)
	}
	if err := rejectLegacyV18CutoverMarkerRegression(homeDir, marker); err != nil {
		return legacyV18CutoverMarkerArtifact{}, err
	}
	if exact, err := exactLegacyV18CutoverMarkerPayload(artifact.Path, payload); err != nil {
		return legacyV18CutoverMarkerArtifact{}, err
	} else if exact {
		if err := syncLegacyV18CutoverFile(artifact.Path); err != nil {
			return legacyV18CutoverMarkerArtifact{}, fmt.Errorf("flush existing v19 cutover advisory marker: %w", err)
		}
		if err := syncLegacyV18CutoverDirectoryParent(artifact.Path); err != nil {
			return legacyV18CutoverMarkerArtifact{}, fmt.Errorf("flush existing v19 cutover advisory marker directory: %w", err)
		}
		return artifact, nil
	}

	candidatePath := legacyV18CutoverMarkerCandidatePath(homeDir)
	if err := prepareLegacyV18CutoverMarkerCandidate(candidatePath, payload); err != nil {
		return legacyV18CutoverMarkerArtifact{}, err
	}
	if err := replaceLegacyV18CutoverMarker(candidatePath, artifact.Path); err != nil {
		return legacyV18CutoverMarkerArtifact{}, fmt.Errorf("publish v19 cutover advisory marker: %w", err)
	}
	if exact, err := exactLegacyV18CutoverMarkerPayload(artifact.Path, payload); err != nil {
		return legacyV18CutoverMarkerArtifact{}, err
	} else if !exact {
		return legacyV18CutoverMarkerArtifact{}, fmt.Errorf("published v19 cutover advisory marker differs from deterministic payload")
	}
	if digest, err := legacyV18CutoverFileSHA256(artifact.Path); err != nil {
		return legacyV18CutoverMarkerArtifact{}, fmt.Errorf("hash published v19 cutover advisory marker: %w", err)
	} else if digest != artifact.SHA256 {
		return legacyV18CutoverMarkerArtifact{}, fmt.Errorf("published v19 cutover advisory marker digest=%s, want %s", digest, artifact.SHA256)
	}
	if _, err := os.Lstat(candidatePath); !os.IsNotExist(err) {
		if err == nil {
			return legacyV18CutoverMarkerArtifact{}, fmt.Errorf("v19 cutover advisory marker candidate remained after publication")
		}
		return legacyV18CutoverMarkerArtifact{}, fmt.Errorf("inspect published v19 cutover advisory marker candidate: %w", err)
	}
	return artifact, nil
}

func rejectLegacyV18CutoverMarkerRegression(homeDir string, next legacyV18CutoverMarker) error {
	current, found, err := readLegacyV18CutoverMarkerIfValid(homeDir)
	if err != nil || !found || current.MigrationID != next.MigrationID {
		// Marker bytes are advisory. Corrupt/stale marker prose never blocks repair
		// from stronger typed evidence, and a different migration identity may replace it.
		return nil
	}
	currentOrdinal, _ := legacyV18CutoverMarkerPhaseOrdinal(current.Phase)
	nextOrdinal, _ := legacyV18CutoverMarkerPhaseOrdinal(next.Phase)
	if nextOrdinal < currentOrdinal {
		return fmt.Errorf("v19 cutover advisory marker phase regression %s -> %s", current.Phase, next.Phase)
	}
	return nil
}

func readLegacyV18CutoverMarker(homeDir string) (legacyV18CutoverMarker, legacyV18CutoverMarkerArtifact, error) {
	path := legacyV18CutoverMarkerPath(homeDir)
	if err := requireLegacyV18CutoverDirectRegularFile(path, "advisory marker"); err != nil {
		return legacyV18CutoverMarker{}, legacyV18CutoverMarkerArtifact{}, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return legacyV18CutoverMarker{}, legacyV18CutoverMarkerArtifact{}, fmt.Errorf("read v19 cutover advisory marker: %w", err)
	}
	marker, err := decodeLegacyV18CutoverMarkerPayload(homeDir, payload)
	if err != nil {
		return legacyV18CutoverMarker{}, legacyV18CutoverMarkerArtifact{}, err
	}
	return marker, legacyV18CutoverMarkerArtifact{
		MigrationID: marker.MigrationID,
		Phase:       marker.Phase,
		Path:        path,
		SHA256:      canonicalV19SHA256(payload),
	}, nil
}

func readLegacyV18CutoverMarkerIfValid(homeDir string) (legacyV18CutoverMarker, bool, error) {
	path := legacyV18CutoverMarkerPath(homeDir)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return legacyV18CutoverMarker{}, false, nil
	}
	if err != nil {
		return legacyV18CutoverMarker{}, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return legacyV18CutoverMarker{}, false, fmt.Errorf("v19 cutover advisory marker is not a direct regular file")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return legacyV18CutoverMarker{}, false, err
	}
	marker, err := decodeLegacyV18CutoverMarkerPayload(homeDir, payload)
	if err != nil {
		return legacyV18CutoverMarker{}, false, err
	}
	return marker, true, nil
}

func decodeLegacyV18CutoverMarkerPayload(homeDir string, payload []byte) (legacyV18CutoverMarker, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var marker legacyV18CutoverMarker
	if err := decoder.Decode(&marker); err != nil {
		return legacyV18CutoverMarker{}, fmt.Errorf("decode v19 cutover advisory marker: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return legacyV18CutoverMarker{}, fmt.Errorf("v19 cutover advisory marker has trailing JSON value")
		}
		return legacyV18CutoverMarker{}, fmt.Errorf("v19 cutover advisory marker trailing data: %w", err)
	}
	canonical, err := json.Marshal(marker)
	if err != nil {
		return legacyV18CutoverMarker{}, fmt.Errorf("re-encode v19 cutover advisory marker: %w", err)
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(payload, canonical) {
		return legacyV18CutoverMarker{}, fmt.Errorf("v19 cutover advisory marker bytes are not canonical deterministic JSON")
	}
	if err := validateLegacyV18CutoverMarker(homeDir, marker); err != nil {
		return legacyV18CutoverMarker{}, fmt.Errorf("validate v19 cutover advisory marker: %w", err)
	}
	return marker, nil
}

func exactLegacyV18CutoverMarkerPayload(path string, payload []byte) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect v19 cutover advisory marker: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("v19 cutover advisory marker %s is not a direct regular file", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read existing v19 cutover advisory marker: %w", err)
	}
	return bytes.Equal(got, payload), nil
}

func prepareLegacyV18CutoverMarkerCandidate(path string, payload []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("v19 cutover advisory marker candidate %s is not a direct regular file", path)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove stale v19 cutover advisory marker candidate: %w", err)
		}
		if err := syncLegacyV18CutoverDirectoryParent(path); err != nil {
			return fmt.Errorf("flush removed v19 cutover advisory marker candidate directory: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect v19 cutover advisory marker candidate: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create v19 cutover advisory marker candidate: %w", err)
	}
	written, writeErr := file.Write(payload)
	if writeErr == nil && written != len(payload) {
		writeErr = io.ErrShortWrite
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write v19 cutover advisory marker candidate: %w", writeErr)
	}
	if syncErr != nil {
		return fmt.Errorf("flush v19 cutover advisory marker candidate: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close v19 cutover advisory marker candidate: %w", closeErr)
	}
	if err := syncLegacyV18CutoverDirectoryParent(path); err != nil {
		return fmt.Errorf("flush v19 cutover advisory marker candidate directory: %w", err)
	}
	return nil
}
