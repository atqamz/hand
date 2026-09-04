package store

import "path/filepath"

const legacyV18CutoverManifestVersion = "v1"
const legacyV18CutoverManifestFileName = "manifest.json"
const legacyV18CutoverManifestPolicyInputDomain = "hand:v19-cutover:policy-input:v1"
const legacyV18CutoverRepositoryIdentityDomain = "hand:v19-cutover:repository-identity:v1"
const legacyV18CutoverCommonGitDirIdentityDomain = "hand:v19-cutover:common-git-dir-identity:v1"
const legacyV18CutoverSemanticSourceVersion = 18
const legacyV18CutoverSourceContract = "v0.7.2"
const canonicalV19AuthorityCommit = "67ca4b35b773ef25ac9ff88cd1b16213153ed498"
const canonicalV19AuthorityDDLPath = "docs/architecture/v19.sql.gz"
const canonicalV19AuthorityDDLGitBlobSHA1 = "d529d7a687db8d0266d5592ade9520c04766bf43"

// LegacyV18CutoverManifestInput is positive import evidence that may be made
// durable after final quiescence and before the one-way source freeze.
type LegacyV18CutoverManifestInput struct {
	FleetID    string
	ImportedAt string
	Projects   []LegacyV18CutoverManifestProjectInput
}

// LegacyV18CutoverManifestProjectInput is one verified current legacy Project
// repository binding plus bounded provenance inputs. Policy inputs are digested
// into the manifest rather than copied as raw values.
type LegacyV18CutoverManifestProjectInput struct {
	SourceProjectID      string
	Locator              string
	RepositoryPhysicalID string
	CommonDirPhysicalID  string
	Revision             string
	LegacyName           string
	LegacyURL            string
	LegacyMode           string
	LegacyUpstream       string
}

type legacyV18CutoverManifest struct {
	FormatVersion   string                            `json:"format_version"`
	MigrationID     string                            `json:"migration_id"`
	ImportedAt      string                            `json:"imported_at"`
	Fleet           legacyV18CutoverManifestFleet     `json:"fleet"`
	Source          legacyV18CutoverManifestSource    `json:"source"`
	OriginalArchive legacyV18CutoverManifestArchive   `json:"original_archive"`
	Freeze          legacyV18CutoverManifestFreeze    `json:"freeze"`
	Target          legacyV18CutoverManifestTarget    `json:"target"`
	Projects        []legacyV18CutoverManifestProject `json:"projects"`
	Sidecars        []string                          `json:"sidecars"`
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
