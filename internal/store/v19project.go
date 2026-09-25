package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	handgit "github.com/atqamz/hand/internal/git"
)

type canonicalV19ProjectRepository struct {
	Locator, CommonDir, RepositoryIdentity, PhysicalIdentity, Revision string
}

// RegisterCanonicalV19Project records an existing direct Git clone under projects.
// It creates no acquisition history or policy; absent policy remains absent.
func RegisterCanonicalV19Project(ctx context.Context, homeDir, name string) (string, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", "", fmt.Errorf("canonical Project name must be one path component")
	}
	db, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = db.Close() }()
	observed, err := observeCanonicalV19Project(homeDir, name)
	if err != nil {
		return "", "", err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return "", "", err
	}
	fleetID, err := validateCanonicalV19CutoverActiveFleet(tx)
	if err != nil {
		return "", "", err
	}
	var aliases int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_binding WHERE superseded_at='' AND
		(repository_locator=? OR repository_identity_digest=? OR physical_identity_digest=?)`,
		observed.Locator, observed.RepositoryIdentity, observed.PhysicalIdentity).Scan(&aliases); err != nil {
		return "", "", err
	}
	again, err := observeCanonicalV19Project(homeDir, name)
	if err != nil {
		return "", "", err
	}
	if again != observed {
		return "", "", fmt.Errorf("canonical Project repository changed during registration")
	}
	if aliases != 0 {
		var id, workspaceID string
		err := tx.QueryRowContext(ctx, `SELECT p.id,w.id FROM workspace_binding w JOIN project p ON p.id=w.project_id
			WHERE p.fleet_id=? AND p.retired_at='' AND p.display_name=? AND w.superseded_at=''
			AND w.repository_locator=? AND w.repository_identity_digest=? AND w.physical_identity_digest=?
			AND w.common_git_dir=? AND w.revision=?`, fleetID, name, observed.Locator, observed.RepositoryIdentity,
			observed.PhysicalIdentity, observed.CommonDir, observed.Revision).Scan(&id, &workspaceID)
		if aliases == 1 && err == nil {
			return id, workspaceID, nil
		}
		return "", "", fmt.Errorf("canonical Project repository is already bound with different evidence; refusing duplicate identity")
	}
	id, err := newProjectID()
	if err != nil {
		return "", "", err
	}
	workspaceID := "wb_" + id[2:]
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `INSERT INTO project(id,fleet_id,ordinal,display_name,created_at)
		VALUES(?,?,(SELECT COALESCE(MAX(ordinal),0)+1 FROM project WHERE fleet_id=?),?,?)`, id, fleetID, fleetID, name, now)
	if err != nil {
		return "", "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workspace_binding(id,project_id,ordinal,repository_locator,
		repository_identity_digest,common_git_dir,physical_identity_digest,revision,established_at)
		VALUES(?,?,1,?,?,?,?,?,?)`, workspaceID, id, observed.Locator, observed.RepositoryIdentity,
		observed.CommonDir, observed.PhysicalIdentity, observed.Revision, now)
	if err != nil {
		return "", "", err
	}
	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	return id, workspaceID, nil
}

func observeCanonicalV19Project(homeDir, name string) (canonicalV19ProjectRepository, error) {
	var result canonicalV19ProjectRepository
	result.Locator = "projects/" + name
	result.CommonDir = result.Locator + "/.git"
	root := filepath.Join(homeDir, filepath.FromSlash(result.Locator))
	common := filepath.Join(root, ".git")
	projects, err := os.Lstat(filepath.Join(homeDir, "projects"))
	if err != nil {
		return result, err
	}
	if !projects.IsDir() || projects.Mode()&os.ModeSymlink != 0 {
		return result, fmt.Errorf("canonical projects directory must be a direct directory")
	}
	identities := make([]string, 0, 2)
	files := make([]os.FileInfo, 0, 2)
	for _, path := range []string{root, common} {
		info, err := os.Lstat(path)
		if err != nil {
			return result, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return result, fmt.Errorf("canonical Project requires direct repository and .git directories")
		}
		physical, err := CanonicalV19WorktreePhysicalIdentity(path, info)
		if err != nil {
			return result, err
		}
		identities = append(identities, physical)
		files = append(files, info)
	}
	actualRoot, err := handgit.ResolveRoot(root)
	if err != nil {
		return result, err
	}
	actualCommon, err := handgit.CommonDir(root)
	if err != nil {
		return result, err
	}
	if !handgit.SamePath(root, actualRoot) || !handgit.SamePath(common, actualCommon) {
		return result, fmt.Errorf("canonical Project Git root/common directory differs from the captured clone")
	}
	result.Revision, err = handgit.HeadCommit(root)
	if err != nil {
		return result, err
	}
	if err := validateLegacyV18CutoverManifestRevision(result.Revision); err != nil {
		return result, err
	}
	for i, path := range []string{root, common} {
		after, err := os.Lstat(path)
		if err != nil || !os.SameFile(files[i], after) {
			return result, fmt.Errorf("canonical Project physical identity changed during Git observation")
		}
	}
	result.RepositoryIdentity = legacyV18CutoverManifestIdentitySHA256(legacyV18CutoverRepositoryIdentityDomain, identities[0])
	result.PhysicalIdentity = legacyV18CutoverManifestIdentitySHA256(legacyV18CutoverCommonGitDirIdentityDomain, identities[1])
	return result, nil
}
