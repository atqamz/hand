package selfupdate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/atqamz/hand/internal/pathdisplay"
)

type AdoptionResult struct {
	Path   string
	Result string
}

// Adopt verifies source, makes an ownership decision for the existing direct Hand, and
// atomically selects the exact source at the selected path. It does not inspect Fleet state.
func Adopt(ctx context.Context, source, target string, want BuildInfo) (AdoptionResult, error) {
	source, err := absolutePath(source)
	if err != nil {
		return AdoptionResult{}, fmt.Errorf("resolve staged Hand path: %w", err)
	}
	target, err = absolutePath(target)
	if err != nil {
		return AdoptionResult{}, fmt.Errorf("resolve Hand install path: %w", err)
	}
	if want.Channel != ChannelStable || !CanSelfUpdate(want.Distribution) {
		return AdoptionResult{}, fmt.Errorf("bootstrap adoption requires a stable direct GitHub build")
	}
	if err := verifyExecutableBuildInfo(ctx, source, want); err != nil {
		return AdoptionResult{}, fmt.Errorf("verify staged build identity: %w", err)
	}

	authorities, err := inspectHandAuthorities(target)
	if err != nil {
		return AdoptionResult{}, err
	}
	if authorities.direct != "" && authorities.active != "" && !authorities.same {
		return AdoptionResult{}, fmt.Errorf("requested Hand target %s and active PATH Hand %s are different authorities; refusing to shadow or replace either", target, authorities.active)
	}
	if authorities.direct == "" && authorities.active != "" {
		if _, err := assessExistingHand(ctx, authorities.active, want); err != nil {
			return AdoptionResult{}, err
		}
		return AdoptionResult{}, fmt.Errorf("requested Hand target %s differs from active PATH Hand %s; refusing to create a shadow install", target, authorities.active)
	}

	existing := authorities.direct
	if existing != "" {
		result, err := assessExistingHand(ctx, existing, want)
		if err != nil {
			return AdoptionResult{}, err
		}
		if result != "" {
			return AdoptionResult{Path: existing, Result: result}, nil
		}
		target = existing
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return AdoptionResult{}, fmt.Errorf("create Hand install directory %s: %w", filepath.Dir(target), err)
	}
	staged, err := stageExecutable(source, filepath.Dir(target))
	if err != nil {
		return AdoptionResult{}, err
	}
	defer func() { _ = os.Remove(staged) }()

	replacement, err := replaceAdoptedExecutable(target, staged)
	if err != nil {
		return AdoptionResult{}, fmt.Errorf("select Hand at %s: %w", target, err)
	}
	if err := verifyExecutableBuildInfo(ctx, target, want); err != nil {
		if rollbackErr := replacement.rollback(); rollbackErr != nil {
			return AdoptionResult{}, fmt.Errorf("verify selected build identity: %w; rollback failed: %v", err, rollbackErr)
		}
		return AdoptionResult{}, fmt.Errorf("verify selected build identity: %w", err)
	}
	if err := replacement.commit(); err != nil {
		return AdoptionResult{}, fmt.Errorf("clean up previous Hand after selecting %s: %w", target, err)
	}
	return AdoptionResult{Path: target, Result: "installed"}, nil
}

func absolutePath(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be absolute: %s", pathdisplay.Context(path))
	}
	return filepath.Clean(path), nil
}

type handAuthorities struct {
	direct string
	active string
	same   bool
}

func inspectHandAuthorities(target string) (handAuthorities, error) {
	direct, err := existingExecutablePath(target)
	if err != nil {
		return handAuthorities{}, err
	}
	active, err := activeHandPath()
	if err != nil {
		return handAuthorities{}, err
	}
	authorities := handAuthorities{direct: direct, active: active}
	if direct != "" && active != "" {
		authorities.same = sameExecutableAuthority(direct, active)
	}
	return authorities, nil
}

func existingExecutablePath(path string) (string, error) {
	if _, err := os.Lstat(path); err == nil {
		return filepath.Clean(path), nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect existing Hand at %s: %w", path, err)
	}
	return "", nil
}

func activeHandPath() (string, error) {
	path, err := exec.LookPath(binaryName)
	if err != nil {
		return "", nil
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve Hand on PATH: %w", err)
	}
	return filepath.Clean(path), nil
}

func sameExecutableAuthority(first, second string) bool {
	firstInfo, firstErr := os.Stat(first)
	secondInfo, secondErr := os.Stat(second)
	if firstErr == nil && secondErr == nil && os.SameFile(firstInfo, secondInfo) {
		return true
	}
	firstCanonical, firstOK := canonicalExecutablePath(first)
	secondCanonical, secondOK := canonicalExecutablePath(second)
	return firstOK && secondOK && firstCanonical == secondCanonical
}

func canonicalExecutablePath(path string) (string, bool) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", false
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", false
	}
	resolved = filepath.Clean(resolved)
	if runtime.GOOS == "windows" {
		resolved = strings.ToLower(resolved)
	}
	return resolved, true
}

func assessExistingHand(ctx context.Context, path string, want BuildInfo) (string, error) {
	identity, err := ReadExecutableBuildInfo(ctx, path)
	if err != nil {
		return "", fmt.Errorf("hand at %s has no verifiable build identity; refusing to replace unknown ownership: %w", path, err)
	}
	if !knownDistribution(identity.Distribution) {
		return "", fmt.Errorf("hand ownership is unknown for distribution %s; refusing to replace it", identity.Distribution)
	}
	if !CanSelfUpdate(identity.Distribution) {
		return "", fmt.Errorf("hand will not replace a %s build; %s", identity.Distribution, UpgradeCommand(identity.Distribution))
	}
	if identity.Channel != ChannelStable {
		return "", fmt.Errorf("hand is %s, not the stable release %s; refusing a silent channel change", identity.Channel, want.Version)
	}
	relation, err := CompareVersions(identity.Version, want.Version)
	if err != nil {
		return "", fmt.Errorf("compare installed Hand version %q with %q: %w", identity.Version, want.Version, err)
	}
	if relation == VersionNewer {
		return "", fmt.Errorf("refusing to downgrade hand from %s to %s", identity.Version, want.Version)
	}
	if relation == VersionSame && SameBuild(identity, want) {
		return "reused", nil
	}
	return "", nil
}

func knownDistribution(distribution string) bool {
	switch distribution {
	case DistributionGitHub, DistributionInstallScript, DistributionBrew, DistributionWinget,
		DistributionNpm, DistributionNix, DistributionDeb, DistributionRpm, DistributionAur,
		DistributionGo, DistributionSource:
		return true
	default:
		return false
	}
}

func stageExecutable(source, dir string) (string, error) {
	input, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("open staged Hand %s: %w", source, err)
	}
	defer func() { _ = input.Close() }()
	info, err := input.Stat()
	if err != nil {
		return "", fmt.Errorf("stat staged Hand %s: %w", source, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("staged Hand %s is not a regular file", source)
	}
	file, err := os.CreateTemp(dir, ".hand-adopt-*")
	if err != nil {
		return "", fmt.Errorf("stage Hand in %s: %w", dir, err)
	}
	path := file.Name()
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.ReadFrom(input); err != nil {
		return "", fmt.Errorf("copy staged Hand: %w", err)
	}
	mode := info.Mode().Perm() | 0o111
	if err := file.Chmod(mode); err != nil {
		return "", fmt.Errorf("set staged Hand permissions: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close staged Hand: %w", err)
	}
	remove = false
	return path, nil
}
