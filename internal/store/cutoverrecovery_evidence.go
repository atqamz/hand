package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type legacyV18CutoverArchiveEvidence struct {
	Archive  legacyV18CutoverOriginalArchive
	Artifact legacyV18CutoverManifestArtifact
	Manifest legacyV18CutoverManifest
}

func discoverLegacyV18CutoverFrozenBridge(homeDir string) (legacyV18CutoverFrozenBridge, error) {
	activePath := Path(homeDir)
	beforeDigest, err := legacyV18CutoverFileSHA256(activePath)
	if err != nil {
		return legacyV18CutoverFrozenBridge{}, fmt.Errorf("hash frozen bridge before validation: %w", err)
	}
	sqlDB, err := openLegacyV18CutoverSQLite(activePath, "ro", legacyV18CutoverGateTimeout, true)
	if err != nil {
		return legacyV18CutoverFrozenBridge{}, fmt.Errorf("open frozen bridge read-only: %w", err)
	}
	version, err := schemaUserVersion(sqlDB)
	if err != nil {
		_ = sqlDB.Close()
		return legacyV18CutoverFrozenBridge{}, err
	}
	if version != legacyV18CutoverFrozenUserVersion {
		_ = sqlDB.Close()
		return legacyV18CutoverFrozenBridge{}, fmt.Errorf("user_version=%d, want exact frozen sentinel %d", version, legacyV18CutoverFrozenUserVersion)
	}
	fleetID, err := legacyV18CutoverFleetID(sqlDB)
	if err != nil {
		_ = sqlDB.Close()
		return legacyV18CutoverFrozenBridge{}, err
	}
	var certificate string
	if err := sqlDB.QueryRow(`SELECT value FROM meta WHERE key = ?`, legacyV18CutoverFreezeCertificateKey).Scan(&certificate); err != nil {
		_ = sqlDB.Close()
		return legacyV18CutoverFrozenBridge{}, fmt.Errorf("read freeze certificate: %w", err)
	}
	prefix := legacyV18CutoverFreezeCertificateVersion + ":"
	if !strings.HasPrefix(certificate, prefix) {
		_ = sqlDB.Close()
		return legacyV18CutoverFrozenBridge{}, fmt.Errorf("freeze certificate=%q, want %s:<source-sha256>", certificate, legacyV18CutoverFreezeCertificateVersion)
	}
	sourceSHA256 := strings.TrimPrefix(certificate, prefix)
	if err := validateLegacyV18CutoverSHA256(sourceSHA256); err != nil {
		_ = sqlDB.Close()
		return legacyV18CutoverFrozenBridge{}, fmt.Errorf("freeze certificate source digest: %w", err)
	}
	migrationID, err := legacyV18CutoverMigrationIdentity(fleetID, sourceSHA256)
	if err != nil {
		_ = sqlDB.Close()
		return legacyV18CutoverFrozenBridge{}, fmt.Errorf("derive frozen bridge migration identity: %w", err)
	}
	validationErr := validateLegacyV18CutoverFrozenBridge(sqlDB, fleetID, sourceSHA256)
	closeErr := sqlDB.Close()
	if validationErr != nil {
		return legacyV18CutoverFrozenBridge{}, validationErr
	}
	if closeErr != nil {
		return legacyV18CutoverFrozenBridge{}, fmt.Errorf("close frozen bridge validation DB: %w", closeErr)
	}
	afterDigest, err := legacyV18CutoverFileSHA256(activePath)
	if err != nil {
		return legacyV18CutoverFrozenBridge{}, fmt.Errorf("hash frozen bridge after validation: %w", err)
	}
	if beforeDigest != afterDigest {
		return legacyV18CutoverFrozenBridge{}, fmt.Errorf("frozen bridge changed during read-only validation: before=%s after=%s", beforeDigest, afterDigest)
	}
	return legacyV18CutoverFrozenBridge{
		MigrationID:  migrationID,
		FleetID:      fleetID,
		SourceSHA256: sourceSHA256,
		BridgeSHA256: afterDigest,
		Certificate:  certificate,
		Committed:    true,
	}, nil
}

func discoverLegacyV18CutoverArchiveEvidence(homeDir string) (legacyV18CutoverArchiveEvidence, bool, bool, error) {
	entries, err := os.ReadDir(Dir(homeDir))
	if os.IsNotExist(err) {
		return legacyV18CutoverArchiveEvidence{}, false, false, nil
	}
	if err != nil {
		return legacyV18CutoverArchiveEvidence{}, false, false, fmt.Errorf("inspect v19 cutover recovery state directory: %w", err)
	}
	var migrationIDs []string
	looseArtifacts := false
	for _, entry := range entries {
		name := entry.Name()
		if !isLegacyV18CutoverLooseArtifact(name) {
			continue
		}
		looseArtifacts = true
		if !strings.HasPrefix(name, "v19-cutover-") {
			continue
		}
		migrationID := strings.TrimPrefix(name, "v19-cutover-")
		if err := validateLegacyV18CutoverMigrationID(migrationID); err != nil {
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return legacyV18CutoverArchiveEvidence{}, false, true, fmt.Errorf("ambiguous v19 cutover recovery directory %q: %w", name, err)
			}
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			return legacyV18CutoverArchiveEvidence{}, false, true, fmt.Errorf("v19 cutover recovery archive path %q is not a direct directory", name)
		}
		migrationIDs = append(migrationIDs, migrationID)
	}
	if len(migrationIDs) == 0 {
		return legacyV18CutoverArchiveEvidence{}, false, looseArtifacts, nil
	}
	if len(migrationIDs) != 1 {
		return legacyV18CutoverArchiveEvidence{}, false, true, fmt.Errorf("ambiguous v19 cutover recovery evidence: found %d deterministic archive directories", len(migrationIDs))
	}
	evidence, err := inspectLegacyV18CutoverArchiveEvidence(homeDir, migrationIDs[0])
	if err != nil {
		return legacyV18CutoverArchiveEvidence{}, false, true, err
	}
	return evidence, true, looseArtifacts, nil
}

func inspectLegacyV18CutoverArchiveEvidence(homeDir, migrationID string) (legacyV18CutoverArchiveEvidence, error) {
	if err := validateLegacyV18CutoverMigrationID(migrationID); err != nil {
		return legacyV18CutoverArchiveEvidence{}, fmt.Errorf("inspect immutable original archive evidence: %w", err)
	}
	manifest, artifact, err := inspectPersistedLegacyV18CutoverManifestReadOnly(homeDir, migrationID)
	if err != nil {
		return legacyV18CutoverArchiveEvidence{}, err
	}
	archive := legacyV18CutoverOriginalArchive{
		MigrationID: migrationID,
		Directory:   legacyV18CutoverOriginalArchiveDir(homeDir, migrationID),
		Path:        legacyV18CutoverOriginalArchivePath(homeDir, migrationID),
		SHA256:      manifest.Source.DBSHA256,
	}
	if err := requireLegacyV18CutoverDirectRegularFile(archive.Path, "original archive"); err != nil {
		return legacyV18CutoverArchiveEvidence{}, err
	}
	if err := requireLegacyV18CutoverNoSQLiteSidecars(archive.Path, "original archive"); err != nil {
		return legacyV18CutoverArchiveEvidence{}, err
	}
	beforeDigest, err := legacyV18CutoverFileSHA256(archive.Path)
	if err != nil {
		return legacyV18CutoverArchiveEvidence{}, fmt.Errorf("hash immutable original archive: %w", err)
	}
	if beforeDigest != archive.SHA256 {
		return legacyV18CutoverArchiveEvidence{}, fmt.Errorf("immutable original archive digest=%s, want manifest source=%s", beforeDigest, archive.SHA256)
	}
	sqlDB, err := openLegacyV18CutoverSQLite(archive.Path, "ro", legacyV18CutoverGateTimeout, true)
	if err != nil {
		return legacyV18CutoverArchiveEvidence{}, fmt.Errorf("open immutable original archive read-only: %w", err)
	}
	_, validationErr := validateLegacyV18CutoverSource(sqlDB)
	fleetID := ""
	if validationErr == nil {
		fleetID, validationErr = legacyV18CutoverFleetID(sqlDB)
	}
	closeErr := sqlDB.Close()
	if validationErr != nil {
		return legacyV18CutoverArchiveEvidence{}, fmt.Errorf("validate immutable original archive exact source: %w", validationErr)
	}
	if closeErr != nil {
		return legacyV18CutoverArchiveEvidence{}, fmt.Errorf("close immutable original archive validation DB: %w", closeErr)
	}
	if fleetID != manifest.Fleet.FleetID {
		return legacyV18CutoverArchiveEvidence{}, fmt.Errorf("immutable original archive Fleet ID=%s, want manifest=%s", fleetID, manifest.Fleet.FleetID)
	}
	afterDigest, err := legacyV18CutoverFileSHA256(archive.Path)
	if err != nil {
		return legacyV18CutoverArchiveEvidence{}, fmt.Errorf("rehash immutable original archive: %w", err)
	}
	if afterDigest != beforeDigest {
		return legacyV18CutoverArchiveEvidence{}, fmt.Errorf("immutable original archive changed during read-only validation: before=%s after=%s", beforeDigest, afterDigest)
	}
	return legacyV18CutoverArchiveEvidence{Archive: archive, Artifact: artifact, Manifest: manifest}, nil
}

func inspectPersistedLegacyV18CutoverManifestReadOnly(homeDir, migrationID string) (legacyV18CutoverManifest, legacyV18CutoverManifestArtifact, error) {
	path := legacyV18CutoverManifestPath(homeDir, migrationID)
	if err := requireLegacyV18CutoverDirectRegularFile(path, "manifest"); err != nil {
		return legacyV18CutoverManifest{}, legacyV18CutoverManifestArtifact{}, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return legacyV18CutoverManifest{}, legacyV18CutoverManifestArtifact{}, fmt.Errorf("read v19 cutover recovery manifest: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var manifest legacyV18CutoverManifest
	if err := decoder.Decode(&manifest); err != nil {
		return legacyV18CutoverManifest{}, legacyV18CutoverManifestArtifact{}, fmt.Errorf("decode v19 cutover recovery manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return legacyV18CutoverManifest{}, legacyV18CutoverManifestArtifact{}, fmt.Errorf("v19 cutover recovery manifest has trailing JSON value")
		}
		return legacyV18CutoverManifest{}, legacyV18CutoverManifestArtifact{}, fmt.Errorf("v19 cutover recovery manifest trailing data: %w", err)
	}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return legacyV18CutoverManifest{}, legacyV18CutoverManifestArtifact{}, fmt.Errorf("re-encode v19 cutover recovery manifest: %w", err)
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(payload, canonical) {
		return legacyV18CutoverManifest{}, legacyV18CutoverManifestArtifact{}, fmt.Errorf("v19 cutover recovery manifest bytes are not canonical deterministic JSON")
	}
	importedAt, err := validateLegacyV18CutoverManifestTimestamp(manifest.ImportedAt)
	if err != nil {
		return legacyV18CutoverManifest{}, legacyV18CutoverManifestArtifact{}, fmt.Errorf("v19 cutover recovery manifest timestamp: %w", err)
	}
	if err := validatePersistedLegacyV18CutoverManifest(manifest, migrationID, importedAt); err != nil {
		return legacyV18CutoverManifest{}, legacyV18CutoverManifestArtifact{}, err
	}
	artifact := legacyV18CutoverManifestArtifact{
		MigrationID: migrationID,
		Path:        filepath.Clean(path),
		SHA256:      canonicalV19SHA256(payload),
		ImportedAt:  importedAt,
	}
	return manifest, artifact, nil
}

func inspectCanonicalV19CutoverMaterializedTemp(homeDir string, evidence legacyV18CutoverArchiveEvidence) (canonicalV19CutoverMaterialization, bool, bool, error) {
	path := legacyV18CutoverCanonicalTargetPath(homeDir, evidence.Manifest.MigrationID)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return canonicalV19CutoverMaterialization{}, false, false, nil
	}
	if err != nil {
		return canonicalV19CutoverMaterialization{}, true, false, fmt.Errorf("inspect canonical temp: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return canonicalV19CutoverMaterialization{}, true, true, fmt.Errorf("canonical temp %s is not a direct regular file", path)
	}
	if err := requireLegacyV18CutoverNoSQLiteSidecars(path, "canonical temp"); err != nil {
		return canonicalV19CutoverMaterialization{}, true, false, err
	}
	beforeDigest, err := legacyV18CutoverFileSHA256(path)
	if err != nil {
		return canonicalV19CutoverMaterialization{}, true, false, fmt.Errorf("hash canonical temp: %w", err)
	}
	sqlDB, err := openLegacyV18CutoverSQLite(path, "ro", legacyV18CutoverGateTimeout, true)
	if err != nil {
		return canonicalV19CutoverMaterialization{}, true, false, fmt.Errorf("open canonical temp read-only: %w", err)
	}
	plan := buildCanonicalV19CutoverImportPlan(evidence.Manifest, evidence.Artifact.SHA256)
	validationErr := validateCanonicalV19Schema(sqlDB)
	if validationErr == nil {
		validationErr = validateCanonicalV19CutoverImportRows(sqlDB, plan)
	}
	closeErr := sqlDB.Close()
	if validationErr != nil {
		return canonicalV19CutoverMaterialization{}, true, false, fmt.Errorf("validate canonical temp exact #344/import evidence: %w", validationErr)
	}
	if closeErr != nil {
		return canonicalV19CutoverMaterialization{}, true, false, fmt.Errorf("close canonical temp validation DB: %w", closeErr)
	}
	afterDigest, err := legacyV18CutoverFileSHA256(path)
	if err != nil {
		return canonicalV19CutoverMaterialization{}, true, false, fmt.Errorf("rehash canonical temp: %w", err)
	}
	if beforeDigest != afterDigest {
		return canonicalV19CutoverMaterialization{}, true, false, fmt.Errorf("canonical temp changed during read-only validation: before=%s after=%s", beforeDigest, afterDigest)
	}
	return canonicalV19CutoverMaterialization{
		MigrationID:    evidence.Manifest.MigrationID,
		Path:           path,
		SHA256:         afterDigest,
		ManifestSHA256: evidence.Artifact.SHA256,
		ImportID:       plan.ImportID,
		ProjectCount:   len(plan.Projects),
	}, true, false, nil
}

func requireLegacyV18CutoverNoSQLiteSidecars(path, role string) error {
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		sidecar := path + suffix
		if _, err := os.Lstat(sidecar); err == nil {
			return fmt.Errorf("v19 cutover recovery %s has unexpected SQLite sidecar %s", role, sidecar)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect v19 cutover recovery %s sidecar %s: %w", role, sidecar, err)
		}
	}
	return nil
}
