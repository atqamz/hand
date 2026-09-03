package store

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	legacyV18CutoverManifestVersion              = "v1"
	legacyV18CutoverManifestFileName             = "manifest.json"
	legacyV18CutoverManifestPolicyInputDomain    = "hand:v19-cutover:policy-input:v1"
	legacyV18CutoverRepositoryIdentityDomain     = "hand:v19-cutover:repository-identity:v1"
	legacyV18CutoverCommonGitDirIdentityDomain   = "hand:v19-cutover:common-git-dir-identity:v1"
	legacyV18CutoverSemanticSourceVersion        = 18
	legacyV18CutoverSourceContract               = "v0.7.2"
	canonicalV19AuthorityCommit                  = "67ca4b35b773ef25ac9ff88cd1b16213153ed498"
	canonicalV19AuthorityDDLPath                 = "docs/architecture/v19.sql.gz"
	canonicalV19AuthorityDDLGitBlobSHA1          = "d529d7a687db8d0266d5592ade9520c04766bf43"
)

// LegacyV18CutoverManifestInput is the positively verified evidence that may be
// recorded in the deterministic archive manifest after the one-way source freeze.
type LegacyV18CutoverManifestInput struct {
	FleetID    string
	ImportedAt string
	Projects   []LegacyV18CutoverManifestProjectInput
}

// LegacyV18CutoverManifestProjectInput is one verified current legacy Project
// repository binding plus bounded provenance inputs. Policy inputs are digested
// into the manifest rather than copied as raw values.
type LegacyV18CutoverManifestProjectInput struct {
	SourceProjectID     string
	Locator             string
	RepositoryPhysicalID string
	CommonDirPhysicalID string
	Revision            string
	LegacyName          string
	LegacyURL           string
	LegacyMode          string
	LegacyUpstream      string
}

type legacyV18CutoverManifest struct {
	FormatVersion   string                              `json:"format_version"`
	MigrationID     string                              `json:"migration_id"`
	ImportedAt      string                              `json:"imported_at"`
	Fleet           legacyV18CutoverManifestFleet      `json:"fleet"`
	Source          legacyV18CutoverManifestSource     `json:"source"`
	OriginalArchive legacyV18CutoverManifestArchive    `json:"original_archive"`
	Freeze          legacyV18CutoverManifestFreeze     `json:"freeze"`
	Target          legacyV18CutoverManifestTarget     `json:"target"`
	Projects        []legacyV18CutoverManifestProject  `json:"projects"`
	Sidecars        []string                            `json:"sidecars"`
}

type legacyV18CutoverManifestFleet struct {
	FleetID string `json:"fleet_id"`
}

type legacyV18CutoverManifestSource struct {
	Contract          string `json:"contract"`
	SemanticVersion   int    `json:"semantic_version"`
	SQLiteUserVersion int    `json:"sqlite_user_version"`
	LayoutFingerprint string `json:"layout_fingerprint"`
	Tables            int    `json:"tables"`
	Indexes           int    `json:"indexes"`
	Triggers          int    `json:"triggers"`
	DBSHA256          string `json:"db_sha256"`
}

type legacyV18CutoverManifestArchive struct {
	RelativePath string `json:"relative_path"`
	DBSHA256     string `json:"db_sha256"`
}

type legacyV18CutoverManifestFreeze struct {
	CertificateVersion string `json:"certificate_version"`
	CertificateValue   string `json:"certificate_value"`
	CertificateSHA256  string `json:"certificate_sha256"`
	BridgeUserVersion  int    `json:"bridge_user_version"`
}

type legacyV18CutoverManifestTarget struct {
	AuthorityCommit         string `json:"authority_commit"`
	DDLPath                 string `json:"ddl_path"`
	DDLGitBlobSHA1          string `json:"ddl_git_blob_sha1"`
	GzipSHA256              string `json:"gzip_sha256"`
	DDLSHA256               string `json:"ddl_sha256"`
	SchemaFingerprint       string `json:"schema_fingerprint"`
	SQLiteUserVersion       int    `json:"sqlite_user_version"`
	Tables                  int    `json:"tables"`
	Indexes                 int    `json:"indexes"`
	Triggers                int    `json:"triggers"`
}

type legacyV18CutoverManifestProject struct {
	SourceProjectID          string `json:"source_project_id"`
	DisplayName              string `json:"display_name"`
	RepositoryLocator        string `json:"repository_locator"`
	RepositoryPhysicalID     string `json:"repository_physical_id"`
	RepositoryIdentitySHA256 string `json:"repository_identity_sha256"`
	CommonGitDir             string `json:"common_git_dir"`
	CommonDirPhysicalID      string `json:"common_dir_physical_id"`
	PhysicalIdentitySHA256   string `json:"physical_identity_sha256"`
	Revision                 string `json:"revision"`
	PolicyInputSHA256        string `json:"policy_input_sha256"`
}

type legacyV18CutoverManifestArtifact struct {
	MigrationID string
	Path        string
	SHA256      string
	ImportedAt  string
}

func legacyV18CutoverManifestPath(homeDir, migrationID string) string {
	return filepath.Join(legacyV18CutoverOriginalArchiveDir(homeDir, migrationID), legacyV18CutoverManifestFileName)
}

func legacyV18CutoverManifestCandidatePath(homeDir, migrationID string) string {
	return legacyV18CutoverManifestPath(homeDir, migrationID) + ".candidate"
}

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
		Fleet: legacyV18CutoverManifestFleet{
			FleetID: input.FleetID,
		},
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
		OriginalArchive: legacyV18CutoverManifestArchive{
			RelativePath: "hand.db",
			DBSHA256:     archive.SHA256,
		},
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
