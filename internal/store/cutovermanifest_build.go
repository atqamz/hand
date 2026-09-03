package store

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func buildLegacyV18CutoverManifest(homeDir string, archive legacyV18CutoverOriginalArchive, input LegacyV18CutoverManifestInput) (legacyV18CutoverManifest, error) {
	if err := validateLegacyV18CutoverOriginalArchiveForManifest(homeDir, archive, input.FleetID); err != nil {
		return legacyV18CutoverManifest{}, err
	}
	importedAt, err := validateLegacyV18CutoverManifestTimestamp(input.ImportedAt)
	if err != nil {
		return legacyV18CutoverManifest{}, err
	}
	projects, err := buildLegacyV18CutoverManifestProjects(input.Projects)
	if err != nil {
		return legacyV18CutoverManifest{}, err
	}
	certificate := legacyV18CutoverFreezeCertificateVersion + ":" + archive.SHA256
	return legacyV18CutoverManifest{
		FormatVersion: legacyV18CutoverManifestVersion,
		MigrationID:   archive.MigrationID,
		ImportedAt:    importedAt,
		Fleet:         legacyV18CutoverManifestFleet{FleetID: input.FleetID},
		Source: legacyV18CutoverManifestSource{
			Contract:          legacyV18CutoverSourceContract,
			SemanticVersion:   legacyV18CutoverSemanticSourceVersion,
			SQLiteUserVersion: legacyV072SchemaVersion,
			LayoutFingerprint: legacyV072LayoutFingerprint,
			Tables:            legacyV072TableCount,
			Indexes:           legacyV072IndexCount,
			Triggers:          legacyV072TriggerCount,
			DBSHA256:          archive.SHA256,
		},
		OriginalArchive: legacyV18CutoverManifestArchive{RelativePath: "hand.db", DBSHA256: archive.SHA256},
		Freeze: legacyV18CutoverManifestFreeze{
			CertificateVersion: legacyV18CutoverFreezeCertificateVersion,
			CertificateValue:   certificate,
			CertificateSHA256:  canonicalV19SHA256([]byte(certificate)),
			BridgeUserVersion:  legacyV18CutoverFrozenUserVersion,
		},
		Target: legacyV18CutoverManifestTarget{
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
		},
		Projects: projects,
		Sidecars: []string{},
	}, nil
}

func validateLegacyV18CutoverOriginalArchiveForManifest(homeDir string, archive legacyV18CutoverOriginalArchive, fleetID string) error {
	if err := validateFleetID(fleetID); err != nil {
		return fmt.Errorf("build legacy v18 cutover manifest: Fleet ID: %w", err)
	}
	if err := validateLegacyV18CutoverMigrationID(archive.MigrationID); err != nil {
		return fmt.Errorf("build legacy v18 cutover manifest: %w", err)
	}
	if err := validateLegacyV18CutoverSHA256(archive.SHA256); err != nil {
		return fmt.Errorf("build legacy v18 cutover manifest: %w", err)
	}
	expectedMigrationID, err := legacyV18CutoverMigrationIdentity(fleetID, archive.SHA256)
	if err != nil {
		return fmt.Errorf("build legacy v18 cutover manifest: derive migration identity: %w", err)
	}
	if archive.MigrationID != expectedMigrationID {
		return fmt.Errorf("build legacy v18 cutover manifest: migration identity=%s, want %s from exact Fleet/source evidence", archive.MigrationID, expectedMigrationID)
	}
	expectedDirectory := legacyV18CutoverOriginalArchiveDir(homeDir, archive.MigrationID)
	expectedPath := legacyV18CutoverOriginalArchivePath(homeDir, archive.MigrationID)
	if filepath.Clean(archive.Directory) != filepath.Clean(expectedDirectory) || filepath.Clean(archive.Path) != filepath.Clean(expectedPath) {
		return fmt.Errorf("build legacy v18 cutover manifest: original archive path is not deterministic")
	}
	directoryInfo, err := os.Lstat(archive.Directory)
	if err != nil {
		return fmt.Errorf("build legacy v18 cutover manifest: inspect archive directory: %w", err)
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return fmt.Errorf("build legacy v18 cutover manifest: archive directory is not a direct directory")
	}
	if err := requireLegacyV18CutoverDirectRegularFile(archive.Path, "original archive"); err != nil {
		return err
	}
	if err := syncLegacyV18CutoverFile(archive.Path); err != nil {
		return fmt.Errorf("build legacy v18 cutover manifest: flush original archive: %w", err)
	}
	digest, err := legacyV18CutoverFileSHA256(archive.Path)
	if err != nil {
		return fmt.Errorf("build legacy v18 cutover manifest: hash original archive: %w", err)
	}
	if digest != archive.SHA256 {
		return fmt.Errorf("build legacy v18 cutover manifest: original archive digest=%s, want %s", digest, archive.SHA256)
	}
	return nil
}

func validateLegacyV18CutoverManifestTimestamp(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("build legacy v18 cutover manifest: imported_at is empty")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", fmt.Errorf("build legacy v18 cutover manifest: imported_at is not RFC3339Nano: %w", err)
	}
	canonical := parsed.UTC().Format(time.RFC3339Nano)
	if value != canonical || !strings.HasSuffix(value, "Z") {
		return "", fmt.Errorf("build legacy v18 cutover manifest: imported_at=%q, want exact canonical UTC %q", value, canonical)
	}
	return canonical, nil
}

func buildLegacyV18CutoverManifestProjects(input []LegacyV18CutoverManifestProjectInput) ([]legacyV18CutoverManifestProject, error) {
	projects := append([]LegacyV18CutoverManifestProjectInput(nil), input...)
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].Locator != projects[j].Locator {
			return projects[i].Locator < projects[j].Locator
		}
		return projects[i].SourceProjectID < projects[j].SourceProjectID
	})

	seenSource := make(map[string]struct{}, len(projects))
	seenLocator := make(map[string]struct{}, len(projects))
	seenPhysical := make(map[string]string, len(projects)*2)
	out := make([]legacyV18CutoverManifestProject, 0, len(projects))
	for _, project := range projects {
		if project.SourceProjectID == "" {
			return nil, fmt.Errorf("build legacy v18 cutover manifest: source Project ID is empty")
		}
		if _, exists := seenSource[project.SourceProjectID]; exists {
			return nil, fmt.Errorf("build legacy v18 cutover manifest: duplicate source Project ID %q", project.SourceProjectID)
		}
		seenSource[project.SourceProjectID] = struct{}{}
		if err := validateLegacyV18CutoverManifestLocator(project.Locator, project.LegacyName); err != nil {
			return nil, err
		}
		if _, exists := seenLocator[project.Locator]; exists {
			return nil, fmt.Errorf("build legacy v18 cutover manifest: duplicate repository locator %q", project.Locator)
		}
		seenLocator[project.Locator] = struct{}{}
		if project.RepositoryPhysicalID == "" || project.CommonDirPhysicalID == "" {
			return nil, fmt.Errorf("build legacy v18 cutover manifest: Project %q physical identity evidence is incomplete", project.SourceProjectID)
		}
		if project.RepositoryPhysicalID == project.CommonDirPhysicalID {
			return nil, fmt.Errorf("build legacy v18 cutover manifest: Project %q repository and common-dir physical identities alias", project.SourceProjectID)
		}
		for role, physicalID := range map[string]string{
			"repository": project.RepositoryPhysicalID,
			"common-dir": project.CommonDirPhysicalID,
		} {
			if previous, exists := seenPhysical[physicalID]; exists {
				return nil, fmt.Errorf("build legacy v18 cutover manifest: Project %q %s physical identity aliases %s", project.SourceProjectID, role, previous)
			}
			seenPhysical[physicalID] = "Project " + project.SourceProjectID + " " + role
		}
		if err := validateLegacyV18CutoverManifestRevision(project.Revision); err != nil {
			return nil, fmt.Errorf("build legacy v18 cutover manifest: Project %q: %w", project.SourceProjectID, err)
		}

		out = append(out, legacyV18CutoverManifestProject{
			SourceProjectID:          project.SourceProjectID,
			DisplayName:              project.LegacyName,
			RepositoryLocator:        project.Locator,
			RepositoryPhysicalID:     project.RepositoryPhysicalID,
			RepositoryIdentitySHA256: legacyV18CutoverManifestIdentitySHA256(legacyV18CutoverRepositoryIdentityDomain, project.RepositoryPhysicalID),
			CommonGitDir:             project.Locator + "/.git",
			CommonDirPhysicalID:      project.CommonDirPhysicalID,
			PhysicalIdentitySHA256:   legacyV18CutoverManifestIdentitySHA256(legacyV18CutoverCommonGitDirIdentityDomain, project.CommonDirPhysicalID),
			Revision:                 project.Revision,
			PolicyInputSHA256:        legacyV18CutoverManifestPolicyInputSHA256(project),
		})
	}
	return out, nil
}

func validateLegacyV18CutoverManifestLocator(locator, legacyName string) error {
	if locator == "" || strings.Contains(locator, "\\") || path.IsAbs(locator) || path.Clean(locator) != locator {
		return fmt.Errorf("build legacy v18 cutover manifest: repository locator %q is not canonical", locator)
	}
	parts := strings.Split(locator, "/")
	if len(parts) != 2 || parts[0] != "projects" || parts[1] == "" || parts[1] == "." || parts[1] == ".." {
		return fmt.Errorf("build legacy v18 cutover manifest: repository locator %q is outside canonical projects/<name>", locator)
	}
	if legacyName == "" {
		return fmt.Errorf("build legacy v18 cutover manifest: Project display name is empty")
	}
	if parts[1] != legacyName {
		return fmt.Errorf("build legacy v18 cutover manifest: locator %q and verified legacy display name %q disagree", locator, legacyName)
	}
	return nil
}

func validateLegacyV18CutoverManifestRevision(value string) error {
	if (len(value) != 40 && len(value) != 64) || value != strings.ToLower(value) {
		return fmt.Errorf("revision must be exactly 40 or 64 lowercase hex characters")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("revision is not hexadecimal: %w", err)
	}
	return nil
}

func legacyV18CutoverManifestIdentitySHA256(domain, value string) string {
	return canonicalV19SHA256([]byte(domain + "\x00" + value))
}

func legacyV18CutoverManifestPolicyInputSHA256(project LegacyV18CutoverManifestProjectInput) string {
	payload, err := json.Marshal(struct {
		LegacyName     string `json:"legacy_name"`
		LegacyURL      string `json:"legacy_url"`
		LegacyMode     string `json:"legacy_mode"`
		LegacyUpstream string `json:"legacy_upstream"`
	}{
		LegacyName:     project.LegacyName,
		LegacyURL:      project.LegacyURL,
		LegacyMode:     project.LegacyMode,
		LegacyUpstream: project.LegacyUpstream,
	})
	if err != nil {
		panic(fmt.Sprintf("encode bounded legacy v18 policy inputs: %v", err))
	}
	return canonicalV19SHA256(append([]byte(legacyV18CutoverManifestPolicyInputDomain+"\x00"), payload...))
}
