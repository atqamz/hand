package store

import (
	"fmt"
	"strings"
)

const canonicalV19CutoverProjectIDDomain = "hand:v19-cutover:canonical-project:v1"
const canonicalV19CutoverWorkspaceBindingIDDomain = "hand:v19-cutover:canonical-workspace-binding:v1"
const canonicalV19CutoverPolicyRevisionIDDomain = "hand:v19-cutover:canonical-policy-revision:v1"
const canonicalV19CutoverLegacyImportIDDomain = "hand:v19-cutover:canonical-legacy-import:v1"
const canonicalV19CutoverImportProjectEvidenceDomain = "hand:v19-cutover:canonical-import-project-evidence:v1"

type canonicalV19CutoverMaterialization struct {
	MigrationID    string
	Path           string
	SHA256         string
	ManifestSHA256 string
	ImportID       string
	ProjectCount   int
}

type canonicalV19CutoverImportPlan struct {
	MigrationID    string
	FleetID        string
	ImportedAt     string
	SourceSHA256   string
	ManifestSHA256 string
	ImportID       string
	Projects       []canonicalV19CutoverProjectImport
}

type canonicalV19CutoverProjectImport struct {
	SourceProjectID    string
	ProjectID          string
	WorkspaceBindingID string
	PolicyRevisionID   string
	Ordinal            int
	DisplayName        string
	RepositoryLocator  string
	RepositoryIdentity string
	CommonGitDir       string
	PhysicalIdentity   string
	Revision           string
	PolicyDigest       string
	EvidenceDigest     string
}

func buildCanonicalV19CutoverImportPlan(manifest legacyV18CutoverManifest, manifestSHA256 string) canonicalV19CutoverImportPlan {
	plan := canonicalV19CutoverImportPlan{
		MigrationID:    manifest.MigrationID,
		FleetID:        manifest.Fleet.FleetID,
		ImportedAt:     manifest.ImportedAt,
		SourceSHA256:   manifest.Source.DBSHA256,
		ManifestSHA256: manifestSHA256,
	}
	plan.ImportID = canonicalV19CutoverOpaqueID("li_", canonicalV19CutoverLegacyImportIDDomain, manifest.MigrationID, manifestSHA256)
	plan.Projects = make([]canonicalV19CutoverProjectImport, 0, len(manifest.Projects))
	for i, project := range manifest.Projects {
		projectID := canonicalV19CutoverOpaqueID(
			"p_",
			canonicalV19CutoverProjectIDDomain,
			manifest.MigrationID,
			project.RepositoryLocator,
			project.RepositoryIdentitySHA256,
			project.CommonGitDir,
			project.PhysicalIdentitySHA256,
		)
		workspaceID := canonicalV19CutoverOpaqueID(
			"wb_",
			canonicalV19CutoverWorkspaceBindingIDDomain,
			projectID,
			project.RepositoryLocator,
			project.RepositoryIdentitySHA256,
			project.CommonGitDir,
			project.PhysicalIdentitySHA256,
			project.Revision,
		)
		policyID := canonicalV19CutoverOpaqueID("pr_", canonicalV19CutoverPolicyRevisionIDDomain, projectID, project.PolicyInputSHA256)
		importProject := canonicalV19CutoverProjectImport{
			SourceProjectID:    project.SourceProjectID,
			ProjectID:          projectID,
			WorkspaceBindingID: workspaceID,
			PolicyRevisionID:   policyID,
			Ordinal:            i + 1,
			DisplayName:        project.DisplayName,
			RepositoryLocator:  project.RepositoryLocator,
			RepositoryIdentity: project.RepositoryIdentitySHA256,
			CommonGitDir:       project.CommonGitDir,
			PhysicalIdentity:   project.PhysicalIdentitySHA256,
			Revision:           project.Revision,
			PolicyDigest:       project.PolicyInputSHA256,
		}
		importProject.EvidenceDigest = canonicalV19CutoverDigest(
			canonicalV19CutoverImportProjectEvidenceDomain,
			manifestSHA256,
			project.SourceProjectID,
			projectID,
			workspaceID,
			policyID,
			project.RepositoryLocator,
			project.RepositoryIdentitySHA256,
			project.CommonGitDir,
			project.PhysicalIdentitySHA256,
			project.Revision,
			project.PolicyInputSHA256,
		)
		plan.Projects = append(plan.Projects, importProject)
	}
	return plan
}

func canonicalV19CutoverOpaqueID(prefix, domain string, parts ...string) string {
	return prefix + canonicalV19CutoverDigest(domain, parts...)[:32]
}

func canonicalV19CutoverDigest(domain string, parts ...string) string {
	var payload strings.Builder
	payload.WriteString(domain)
	for _, part := range parts {
		fmt.Fprintf(&payload, "\x00%d:", len(part))
		payload.WriteString(part)
	}
	return canonicalV19SHA256([]byte(payload.String()))
}
