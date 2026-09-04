package store

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	legacyV18CutoverMarkerVersion  = "v1"
	legacyV18CutoverMarkerFileName = "hand-v19-cutover-marker.json"
)

type legacyV18CutoverMarkerPhase string

const (
	legacyV18CutoverMarkerArchiveCandidate   legacyV18CutoverMarkerPhase = "archive-candidate"
	legacyV18CutoverMarkerOriginalArchived   legacyV18CutoverMarkerPhase = "original-archived"
	legacyV18CutoverMarkerFrozenBridge       legacyV18CutoverMarkerPhase = "frozen-bridge"
	legacyV18CutoverMarkerCanonicalTemp      legacyV18CutoverMarkerPhase = "canonical-temp"
	legacyV18CutoverMarkerCanonicalPublished legacyV18CutoverMarkerPhase = "canonical-published"
)

type legacyV18CutoverMarkerInput struct {
	Phase            legacyV18CutoverMarkerPhase
	MigrationID      string
	FleetID          string
	SourceSHA256     string
	ArchiveCandidate *legacyV18CutoverArchiveCandidate
	OriginalArchive  *legacyV18CutoverOriginalArchive
	FrozenBridge     *legacyV18CutoverFrozenBridge
	Manifest         *legacyV18CutoverManifestArtifact
	Materialized     *canonicalV19CutoverMaterialization
	Publication      *canonicalV19CutoverPublication
}

type legacyV18CutoverMarker struct {
	FormatVersion string                         `json:"format_version"`
	Phase         legacyV18CutoverMarkerPhase    `json:"phase"`
	MigrationID   string                         `json:"migration_id"`
	FleetID       string                         `json:"fleet_id"`
	SourceSHA256  string                         `json:"source_sha256"`
	Paths         legacyV18CutoverMarkerPaths    `json:"paths"`
	Evidence      legacyV18CutoverMarkerEvidence `json:"evidence"`
	Target        legacyV18CutoverMarkerTarget   `json:"target"`
}

type legacyV18CutoverMarkerPaths struct {
	ArchiveCandidate    string `json:"archive_candidate"`
	OriginalArchive     string `json:"original_archive"`
	FrozenBridge        string `json:"frozen_bridge"`
	RetiredFrozenBridge string `json:"retired_frozen_bridge"`
	Manifest            string `json:"manifest"`
	CanonicalTemp       string `json:"canonical_temp"`
	CanonicalTarget     string `json:"canonical_target"`
}

type legacyV18CutoverMarkerEvidence struct {
	ArchiveCandidateSHA256    string `json:"archive_candidate_sha256"`
	OriginalArchiveSHA256     string `json:"original_archive_sha256"`
	FrozenBridgeSHA256        string `json:"frozen_bridge_sha256"`
	FreezeCertificateVersion string `json:"freeze_certificate_version"`
	FreezeCertificateSHA256  string `json:"freeze_certificate_sha256"`
	ManifestSHA256           string `json:"manifest_sha256"`
	CanonicalTempSHA256      string `json:"canonical_temp_sha256"`
	CanonicalTargetSHA256    string `json:"canonical_target_sha256"`
	ImportID                 string `json:"import_id"`
	ProjectCount             int    `json:"project_count"`
}

type legacyV18CutoverMarkerTarget struct {
	AuthorityCommit   string `json:"authority_commit"`
	DDLPath           string `json:"ddl_path"`
	DDLGitBlobSHA1    string `json:"ddl_git_blob_sha1"`
	GzipSHA256        string `json:"gzip_sha256"`
	DDLSHA256         string `json:"ddl_sha256"`
	SchemaFingerprint string `json:"schema_fingerprint"`
	SQLiteUserVersion int    `json:"sqlite_user_version"`
	Tables            int    `json:"tables"`
	Indexes           int    `json:"indexes"`
	Triggers          int    `json:"triggers"`
}

type legacyV18CutoverMarkerArtifact struct {
	MigrationID string
	Phase       legacyV18CutoverMarkerPhase
	Path        string
	SHA256      string
}

func legacyV18CutoverMarkerPath(homeDir string) string {
	return filepath.Join(Dir(homeDir), legacyV18CutoverMarkerFileName)
}

func legacyV18CutoverMarkerCandidatePath(homeDir string) string {
	return legacyV18CutoverMarkerPath(homeDir) + ".candidate"
}

func buildLegacyV18CutoverMarker(homeDir string, input legacyV18CutoverMarkerInput) (legacyV18CutoverMarker, error) {
	if err := validateLegacyV18CutoverMarkerInput(homeDir, input); err != nil {
		return legacyV18CutoverMarker{}, err
	}
	paths, err := expectedLegacyV18CutoverMarkerPaths(homeDir, input.MigrationID)
	if err != nil {
		return legacyV18CutoverMarker{}, err
	}
	marker := legacyV18CutoverMarker{
		FormatVersion: legacyV18CutoverMarkerVersion,
		Phase:         input.Phase,
		MigrationID:   input.MigrationID,
		FleetID:       input.FleetID,
		SourceSHA256:  input.SourceSHA256,
		Paths:         paths,
		Evidence: legacyV18CutoverMarkerEvidence{
			ArchiveCandidateSHA256: input.SourceSHA256,
		},
		Target: exactLegacyV18CutoverMarkerTarget(),
	}
	if input.OriginalArchive != nil {
		marker.Evidence.OriginalArchiveSHA256 = input.OriginalArchive.SHA256
	}
	if input.FrozenBridge != nil {
		marker.Evidence.FrozenBridgeSHA256 = input.FrozenBridge.BridgeSHA256
		marker.Evidence.FreezeCertificateVersion = legacyV18CutoverFreezeCertificateVersion
		marker.Evidence.FreezeCertificateSHA256 = canonicalV19SHA256([]byte(input.FrozenBridge.Certificate))
	}
	if input.Manifest != nil {
		marker.Evidence.ManifestSHA256 = input.Manifest.SHA256
	}
	if input.Materialized != nil {
		marker.Evidence.CanonicalTempSHA256 = input.Materialized.SHA256
		marker.Evidence.ImportID = input.Materialized.ImportID
		marker.Evidence.ProjectCount = input.Materialized.ProjectCount
	}
	if input.Publication != nil {
		marker.Evidence.CanonicalTempSHA256 = input.Publication.TargetSHA256
		marker.Evidence.CanonicalTargetSHA256 = input.Publication.TargetSHA256
		marker.Evidence.ImportID = input.Publication.ImportID
		marker.Evidence.ProjectCount = input.Publication.ProjectCount
	}
	if err := validateLegacyV18CutoverMarker(homeDir, marker); err != nil {
		return legacyV18CutoverMarker{}, fmt.Errorf("build v19 cutover advisory marker: %w", err)
	}
	return marker, nil
}

func validateLegacyV18CutoverMarkerInput(homeDir string, input legacyV18CutoverMarkerInput) error {
	if err := validateLegacyV18CutoverMarkerIdentity(input.MigrationID, input.FleetID, input.SourceSHA256); err != nil {
		return fmt.Errorf("build v19 cutover advisory marker: %w", err)
	}
	if _, ok := legacyV18CutoverMarkerPhaseOrdinal(input.Phase); !ok {
		return fmt.Errorf("build v19 cutover advisory marker: unsupported phase %q", input.Phase)
	}
	if input.ArchiveCandidate != nil {
		if input.ArchiveCandidate.MigrationID != input.MigrationID || input.ArchiveCandidate.SHA256 != input.SourceSHA256 || filepath.Clean(input.ArchiveCandidate.Path) != filepath.Clean(legacyV18CutoverArchiveCandidatePath(homeDir, input.MigrationID)) {
			return fmt.Errorf("build v19 cutover advisory marker: archive candidate does not match exact migration/source/path identity")
		}
	}
	if input.OriginalArchive != nil {
		if input.OriginalArchive.MigrationID != input.MigrationID || input.OriginalArchive.SHA256 != input.SourceSHA256 || filepath.Clean(input.OriginalArchive.Directory) != filepath.Clean(legacyV18CutoverOriginalArchiveDir(homeDir, input.MigrationID)) || filepath.Clean(input.OriginalArchive.Path) != filepath.Clean(legacyV18CutoverOriginalArchivePath(homeDir, input.MigrationID)) {
			return fmt.Errorf("build v19 cutover advisory marker: original archive does not match exact migration/source/path identity")
		}
	}
	if input.FrozenBridge != nil {
		bridge := input.FrozenBridge
		expectedCertificate := legacyV18CutoverFreezeCertificateVersion + ":" + input.SourceSHA256
		if !bridge.Committed || bridge.MigrationID != input.MigrationID || bridge.FleetID != input.FleetID || bridge.SourceSHA256 != input.SourceSHA256 || bridge.Certificate != expectedCertificate {
			return fmt.Errorf("build v19 cutover advisory marker: frozen bridge does not match exact migration/Fleet/source certificate identity")
		}
		if err := validateLegacyV18CutoverSHA256(bridge.BridgeSHA256); err != nil {
			return fmt.Errorf("build v19 cutover advisory marker: frozen bridge digest: %w", err)
		}
	}
	if input.Manifest != nil {
		if input.Manifest.MigrationID != input.MigrationID || filepath.Clean(input.Manifest.Path) != filepath.Clean(legacyV18CutoverManifestPath(homeDir, input.MigrationID)) {
			return fmt.Errorf("build v19 cutover advisory marker: manifest does not match exact migration/path identity")
		}
		if err := validateLegacyV18CutoverSHA256(input.Manifest.SHA256); err != nil {
			return fmt.Errorf("build v19 cutover advisory marker: manifest digest: %w", err)
		}
	}
	if input.Materialized != nil {
		materialized := input.Materialized
		if materialized.MigrationID != input.MigrationID || filepath.Clean(materialized.Path) != filepath.Clean(legacyV18CutoverCanonicalTargetPath(homeDir, input.MigrationID)) {
			return fmt.Errorf("build v19 cutover advisory marker: canonical temp does not match exact migration/path identity")
		}
		if err := validateLegacyV18CutoverSHA256(materialized.SHA256); err != nil {
			return fmt.Errorf("build v19 cutover advisory marker: canonical temp digest: %w", err)
		}
		if input.Manifest == nil || materialized.ManifestSHA256 != input.Manifest.SHA256 {
			return fmt.Errorf("build v19 cutover advisory marker: canonical temp manifest digest is not exact")
		}
		if err := validateLegacyV18CutoverImportID(materialized.ImportID); err != nil {
			return fmt.Errorf("build v19 cutover advisory marker: canonical temp import ID: %w", err)
		}
		if materialized.ProjectCount < 0 {
			return fmt.Errorf("build v19 cutover advisory marker: canonical temp project count is negative")
		}
	}
	if input.Publication != nil {
		publication := input.Publication
		if publication.MigrationID != input.MigrationID || publication.FleetID != input.FleetID || publication.SourceSHA256 != input.SourceSHA256 {
			return fmt.Errorf("build v19 cutover advisory marker: publication does not match exact migration/Fleet/source identity")
		}
		if input.Manifest == nil || publication.ManifestSHA256 != input.Manifest.SHA256 {
			return fmt.Errorf("build v19 cutover advisory marker: publication manifest digest is not exact")
		}
		if err := validateLegacyV18CutoverSHA256(publication.TargetSHA256); err != nil {
			return fmt.Errorf("build v19 cutover advisory marker: published target digest: %w", err)
		}
		if err := validateLegacyV18CutoverImportID(publication.ImportID); err != nil {
			return fmt.Errorf("build v19 cutover advisory marker: publication import ID: %w", err)
		}
		if publication.ProjectCount < 0 || filepath.Clean(publication.RetiredBridgePath) != filepath.Clean(legacyV18CutoverRetiredBridgePath(homeDir, input.MigrationID)) {
			return fmt.Errorf("build v19 cutover advisory marker: publication project count or retired bridge path is invalid")
		}
	}

	switch input.Phase {
	case legacyV18CutoverMarkerArchiveCandidate:
		if input.ArchiveCandidate == nil || input.OriginalArchive != nil || input.FrozenBridge != nil || input.Manifest != nil || input.Materialized != nil || input.Publication != nil {
			return fmt.Errorf("build v19 cutover advisory marker: archive-candidate phase has inconsistent evidence")
		}
	case legacyV18CutoverMarkerOriginalArchived:
		if input.OriginalArchive == nil || input.FrozenBridge != nil || input.Manifest != nil || input.Materialized != nil || input.Publication != nil {
			return fmt.Errorf("build v19 cutover advisory marker: original-archived phase has inconsistent evidence")
		}
	case legacyV18CutoverMarkerFrozenBridge:
		if input.OriginalArchive == nil || input.FrozenBridge == nil || input.Materialized != nil || input.Publication != nil {
			return fmt.Errorf("build v19 cutover advisory marker: frozen-bridge phase has inconsistent evidence")
		}
	case legacyV18CutoverMarkerCanonicalTemp:
		if input.OriginalArchive == nil || input.FrozenBridge == nil || input.Manifest == nil || input.Materialized == nil || input.Publication != nil {
			return fmt.Errorf("build v19 cutover advisory marker: canonical-temp phase has inconsistent evidence")
		}
	case legacyV18CutoverMarkerCanonicalPublished:
		if input.OriginalArchive == nil || input.FrozenBridge == nil || input.Manifest == nil || input.Materialized != nil || input.Publication == nil {
			return fmt.Errorf("build v19 cutover advisory marker: canonical-published phase has inconsistent evidence")
		}
	}
	return nil
}

func validateLegacyV18CutoverMarker(homeDir string, marker legacyV18CutoverMarker) error {
	if marker.FormatVersion != legacyV18CutoverMarkerVersion {
		return fmt.Errorf("format_version=%q, want %q", marker.FormatVersion, legacyV18CutoverMarkerVersion)
	}
	ordinal, ok := legacyV18CutoverMarkerPhaseOrdinal(marker.Phase)
	if !ok {
		return fmt.Errorf("unsupported phase %q", marker.Phase)
	}
	if err := validateLegacyV18CutoverMarkerIdentity(marker.MigrationID, marker.FleetID, marker.SourceSHA256); err != nil {
		return err
	}
	expectedPaths, err := expectedLegacyV18CutoverMarkerPaths(homeDir, marker.MigrationID)
	if err != nil {
		return err
	}
	if marker.Paths != expectedPaths {
		return fmt.Errorf("paths do not match deterministic Fleet-relative cutover paths")
	}
	if marker.Target != exactLegacyV18CutoverMarkerTarget() {
		return fmt.Errorf("target contract does not match exact current #344 authority")
	}
	if marker.Evidence.ArchiveCandidateSHA256 != marker.SourceSHA256 {
		return fmt.Errorf("archive candidate digest=%q, want exact source digest", marker.Evidence.ArchiveCandidateSHA256)
	}
	if ordinal >= 2 {
		if marker.Evidence.OriginalArchiveSHA256 != marker.SourceSHA256 {
			return fmt.Errorf("original archive digest=%q, want exact source digest", marker.Evidence.OriginalArchiveSHA256)
		}
	} else if marker.Evidence.OriginalArchiveSHA256 != "" {
		return fmt.Errorf("archive-candidate phase cannot claim authoritative original archive digest")
	}
	if ordinal >= 3 {
		if err := validateLegacyV18CutoverSHA256(marker.Evidence.FrozenBridgeSHA256); err != nil {
			return fmt.Errorf("frozen bridge digest: %w", err)
		}
		if marker.Evidence.FreezeCertificateVersion != legacyV18CutoverFreezeCertificateVersion {
			return fmt.Errorf("freeze certificate version=%q, want %q", marker.Evidence.FreezeCertificateVersion, legacyV18CutoverFreezeCertificateVersion)
		}
		expectedCertificate := legacyV18CutoverFreezeCertificateVersion + ":" + marker.SourceSHA256
		if marker.Evidence.FreezeCertificateSHA256 != canonicalV19SHA256([]byte(expectedCertificate)) {
			return fmt.Errorf("freeze certificate digest does not bind exact original source digest")
		}
	} else if marker.Evidence.FrozenBridgeSHA256 != "" || marker.Evidence.FreezeCertificateVersion != "" || marker.Evidence.FreezeCertificateSHA256 != "" {
		return fmt.Errorf("pre-freeze marker cannot claim frozen bridge evidence")
	}
	if marker.Phase == legacyV18CutoverMarkerArchiveCandidate {
		if marker.Evidence.ManifestSHA256 != "" || marker.Evidence.CanonicalTempSHA256 != "" || marker.Evidence.CanonicalTargetSHA256 != "" || marker.Evidence.ImportID != "" || marker.Evidence.ProjectCount != 0 {
			return fmt.Errorf("archive-candidate phase cannot claim later cutover evidence")
		}
	}
	if marker.Phase == legacyV18CutoverMarkerOriginalArchived {
		if marker.Evidence.ManifestSHA256 != "" || marker.Evidence.CanonicalTempSHA256 != "" || marker.Evidence.CanonicalTargetSHA256 != "" || marker.Evidence.ImportID != "" || marker.Evidence.ProjectCount != 0 {
			return fmt.Errorf("original-archived phase cannot claim later cutover evidence")
		}
	}
	if marker.Phase == legacyV18CutoverMarkerFrozenBridge {
		if marker.Evidence.ManifestSHA256 != "" {
			if err := validateLegacyV18CutoverSHA256(marker.Evidence.ManifestSHA256); err != nil {
				return fmt.Errorf("manifest digest: %w", err)
			}
		}
		if marker.Evidence.CanonicalTempSHA256 != "" || marker.Evidence.CanonicalTargetSHA256 != "" || marker.Evidence.ImportID != "" || marker.Evidence.ProjectCount != 0 {
			return fmt.Errorf("frozen-bridge phase cannot claim canonical temp/publication evidence")
		}
	}
	if ordinal >= 4 {
		if err := validateLegacyV18CutoverSHA256(marker.Evidence.ManifestSHA256); err != nil {
			return fmt.Errorf("manifest digest: %w", err)
		}
		if err := validateLegacyV18CutoverSHA256(marker.Evidence.CanonicalTempSHA256); err != nil {
			return fmt.Errorf("canonical temp digest: %w", err)
		}
		if err := validateLegacyV18CutoverImportID(marker.Evidence.ImportID); err != nil {
			return fmt.Errorf("import ID: %w", err)
		}
		if marker.Evidence.ProjectCount < 0 {
			return fmt.Errorf("project count is negative")
		}
	}
	if marker.Phase == legacyV18CutoverMarkerCanonicalTemp {
		if marker.Evidence.CanonicalTargetSHA256 != "" {
			return fmt.Errorf("canonical-temp phase cannot claim published target digest")
		}
	}
	if marker.Phase == legacyV18CutoverMarkerCanonicalPublished {
		if marker.Evidence.CanonicalTargetSHA256 != marker.Evidence.CanonicalTempSHA256 {
			return fmt.Errorf("published target digest=%q, want exact canonical temp digest=%q", marker.Evidence.CanonicalTargetSHA256, marker.Evidence.CanonicalTempSHA256)
		}
	}
	return nil
}

func validateLegacyV18CutoverMarkerIdentity(migrationID, fleetID, sourceSHA256 string) error {
	if err := validateLegacyV18CutoverMigrationID(migrationID); err != nil {
		return fmt.Errorf("migration identity: %w", err)
	}
	if err := validateFleetID(fleetID); err != nil {
		return fmt.Errorf("Fleet identity: %w", err)
	}
	if err := validateLegacyV18CutoverSHA256(sourceSHA256); err != nil {
		return fmt.Errorf("source digest: %w", err)
	}
	expected, err := legacyV18CutoverMigrationIdentity(fleetID, sourceSHA256)
	if err != nil {
		return err
	}
	if migrationID != expected {
		return fmt.Errorf("migration identity=%s, want %s from exact Fleet/source evidence", migrationID, expected)
	}
	return nil
}

func validateLegacyV18CutoverImportID(value string) error {
	const prefix = "li_"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+32 {
		return fmt.Errorf("must be li_<32 lowercase hex>")
	}
	digest := strings.TrimPrefix(value, prefix)
	if digest != strings.ToLower(digest) {
		return fmt.Errorf("must be lowercase")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return fmt.Errorf("is not hexadecimal: %w", err)
	}
	return nil
}

func legacyV18CutoverMarkerPhaseOrdinal(phase legacyV18CutoverMarkerPhase) (int, bool) {
	switch phase {
	case legacyV18CutoverMarkerArchiveCandidate:
		return 1, true
	case legacyV18CutoverMarkerOriginalArchived:
		return 2, true
	case legacyV18CutoverMarkerFrozenBridge:
		return 3, true
	case legacyV18CutoverMarkerCanonicalTemp:
		return 4, true
	case legacyV18CutoverMarkerCanonicalPublished:
		return 5, true
	default:
		return 0, false
	}
}

func expectedLegacyV18CutoverMarkerPaths(homeDir, migrationID string) (legacyV18CutoverMarkerPaths, error) {
	archiveCandidate, err := legacyV18CutoverFleetRelativePath(homeDir, legacyV18CutoverArchiveCandidatePath(homeDir, migrationID))
	if err != nil {
		return legacyV18CutoverMarkerPaths{}, err
	}
	originalArchive, err := legacyV18CutoverFleetRelativePath(homeDir, legacyV18CutoverOriginalArchivePath(homeDir, migrationID))
	if err != nil {
		return legacyV18CutoverMarkerPaths{}, err
	}
	frozenBridge, err := legacyV18CutoverFleetRelativePath(homeDir, Path(homeDir))
	if err != nil {
		return legacyV18CutoverMarkerPaths{}, err
	}
	retiredBridge, err := legacyV18CutoverFleetRelativePath(homeDir, legacyV18CutoverRetiredBridgePath(homeDir, migrationID))
	if err != nil {
		return legacyV18CutoverMarkerPaths{}, err
	}
	manifest, err := legacyV18CutoverFleetRelativePath(homeDir, legacyV18CutoverManifestPath(homeDir, migrationID))
	if err != nil {
		return legacyV18CutoverMarkerPaths{}, err
	}
	canonicalTemp, err := legacyV18CutoverFleetRelativePath(homeDir, legacyV18CutoverCanonicalTargetPath(homeDir, migrationID))
	if err != nil {
		return legacyV18CutoverMarkerPaths{}, err
	}
	canonicalTarget, err := legacyV18CutoverFleetRelativePath(homeDir, Path(homeDir))
	if err != nil {
		return legacyV18CutoverMarkerPaths{}, err
	}
	return legacyV18CutoverMarkerPaths{
		ArchiveCandidate:    archiveCandidate,
		OriginalArchive:     originalArchive,
		FrozenBridge:        frozenBridge,
		RetiredFrozenBridge: retiredBridge,
		Manifest:            manifest,
		CanonicalTemp:       canonicalTemp,
		CanonicalTarget:     canonicalTarget,
	}, nil
}

func legacyV18CutoverFleetRelativePath(homeDir, absolutePath string) (string, error) {
	relative, err := filepath.Rel(homeDir, absolutePath)
	if err != nil {
		return "", fmt.Errorf("derive Fleet-relative v19 cutover marker path: %w", err)
	}
	relative = filepath.Clean(relative)
	if relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("v19 cutover marker path %q escapes Fleet home", absolutePath)
	}
	return filepath.ToSlash(relative), nil
}

func exactLegacyV18CutoverMarkerTarget() legacyV18CutoverMarkerTarget {
	return legacyV18CutoverMarkerTarget{
		AuthorityCommit:   canonicalV19AuthorityCommit,
		DDLPath:           canonicalV19AuthorityDDLPath,
		DDLGitBlobSHA1:    canonicalV19AuthorityDDLGitBlobSHA1,
		GzipSHA256:        canonicalV19GzipSHA256,
		DDLSHA256:         canonicalV19DDLSHA256,
		SchemaFingerprint: canonicalV19SchemaFingerprint,
		SQLiteUserVersion: canonicalV19SchemaVersion,
		Tables:            canonicalV19TableCount,
		Indexes:           canonicalV19IndexCount,
		Triggers:          canonicalV19TriggerCount,
	}
}
