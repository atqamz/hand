package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/atqamz/hand/internal/toolchain"
)

func ResolveRoot(path string) (string, error) {
	out, err := run(path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("resolve Git repository root: %w", err)
	}
	root, err := filepath.Abs(filepath.FromSlash(strings.TrimSpace(out)))
	if err != nil {
		return "", fmt.Errorf("make Git repository root absolute: %w", err)
	}
	return filepath.Clean(root), nil
}

// CommonDir returns the repository's shared Git directory.
func CommonDir(path string) (string, error) {
	out, err := run(path, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("resolve Git common directory: %w", err)
	}
	common := filepath.FromSlash(strings.TrimSpace(out))
	if common == "" {
		return "", fmt.Errorf("resolve Git common directory: empty path")
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(path, common)
	}
	common, err = filepath.Abs(common)
	if err != nil {
		return "", fmt.Errorf("make Git common directory absolute: %w", err)
	}
	return filepath.Clean(common), nil
}

// SamePath reports whether two paths identify the same filesystem location.
func SamePath(left, right string) bool {
	absolute := func(path string) string {
		abs, err := filepath.Abs(path)
		if err != nil {
			return filepath.Clean(path)
		}
		return filepath.Clean(abs)
	}
	left, right = absolute(left), absolute(right)
	if runtime.GOOS == "windows" {
		if strings.EqualFold(left, right) {
			return true
		}
	} else if left == right {
		return true
	}
	canonical := func(path string) string {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return filepath.Clean(resolved)
		}
		return path
	}
	left, right = canonical(left), canonical(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func Run(dir string, args ...string) (string, error) {
	return run(dir, args...)
}

func IsBare(path string) (bool, error) {
	out, err := run(path, "rev-parse", "--is-bare-repository")
	if err != nil {
		return false, fmt.Errorf("inspect Git repository type: %w", err)
	}
	return strings.TrimSpace(out) == "true", nil
}

func HasUncommittedChanges(path string) (bool, error) {
	out, err := run(path, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("read Git status: %w", err)
	}
	return strings.TrimSpace(out) != "", nil
}

func CurrentBranch(path string) (string, error) {
	out, err := run(path, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git HEAD is detached or unreadable: %w", err)
	}
	branch := strings.TrimSpace(out)
	if branch == "" {
		return "", fmt.Errorf("git HEAD is detached")
	}
	return branch, nil
}

func HeadCommit(path string) (string, error) {
	out, err := run(path, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve Git HEAD commit: %w", err)
	}
	commit := strings.TrimSpace(out)
	if commit == "" {
		return "", fmt.Errorf("git HEAD commit is empty")
	}
	return commit, nil
}

func BranchCommit(path, branch string) (string, error) {
	out, err := run(path, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve Git branch %q commit: %w", branch, err)
	}
	commit := strings.TrimSpace(out)
	if commit == "" {
		return "", fmt.Errorf("git branch %q commit is empty", branch)
	}
	return commit, nil
}

func DefaultBranch(path string) (string, error) {
	if out, err := run(path, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if branch := strings.TrimPrefix(strings.TrimSpace(out), "origin/"); branch != "" {
			return branch, nil
		}
	}

	if out, err := run(path, "remote", "show", "origin"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == "HEAD" && fields[1] == "branch:" && fields[2] != "" {
				return fields[2], nil
			}
		}
	}

	for _, branch := range []string{"main", "master"} {
		if _, err := run(path, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
			return branch, nil
		}
	}
	return "", fmt.Errorf("resolve Git default branch failed")
}

func LocalDefaultBranch(path string) (string, error) {
	if out, err := run(path, "symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD"); err == nil {
		if branch := strings.TrimPrefix(strings.TrimSpace(out), "origin/"); branch != "" {
			return branch, nil
		}
	}
	for _, branch := range []string{"main", "master"} {
		if _, err := run(path, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
			return branch, nil
		}
	}
	if branch, err := CurrentBranch(path); err == nil {
		if _, err := BranchCommit(path, branch); err == nil {
			return branch, nil
		}
	}
	return "", fmt.Errorf("resolve local Git default branch failed")
}

func LocalDefaultBranchRef(path string) (string, error) {
	if out, err := run(path, "symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD"); err == nil {
		if ref := strings.TrimSpace(out); ref != "" {
			return ref, nil
		}
	}
	return LocalDefaultBranch(path)
}

func RemoveOrigin(path string) error {
	if _, err := run(path, "remote", "remove", "origin"); err != nil {
		return fmt.Errorf("remove Git origin: %w", err)
	}
	return nil
}

func CheckoutBranch(path, branch string) error {
	if _, err := run(path, "checkout", "--quiet", branch); err != nil {
		return fmt.Errorf("checkout Git branch %q: %w", branch, err)
	}
	return nil
}

func PreserveDefaultBranch(path, branch string) error {
	if _, err := BranchCommit(path, branch); err != nil {
		return err
	}
	if _, err := run(path, "update-ref", "refs/remotes/origin/"+branch, "refs/heads/"+branch); err != nil {
		return fmt.Errorf("preserve Git default branch %q: %w", branch, err)
	}
	if _, err := run(path, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+branch); err != nil {
		return fmt.Errorf("preserve Git default branch marker %q: %w", branch, err)
	}
	return nil
}

func HasAlternates(path string) (bool, error) {
	out, err := run(path, "rev-parse", "--git-path", "objects/info/alternates")
	if err != nil {
		return false, fmt.Errorf("resolve Git alternates path: %w", err)
	}
	alternatePath := strings.TrimSpace(out)
	if !filepath.IsAbs(alternatePath) {
		alternatePath = filepath.Join(path, alternatePath)
	}
	data, err := os.ReadFile(alternatePath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read Git alternates: %w", err)
	}
	return strings.TrimSpace(string(data)) != "", nil
}

func run(dir string, args ...string) (string, error) {
	managed, err := toolchain.Resolve()
	if err != nil {
		out, stderr, legacyErr := toolchain.RunLegacyForTests(context.Background(), "git", dir, args...)
		if legacyErr == nil {
			return string(out), nil
		}
		if len(stderr) > 0 {
			return "", fmt.Errorf("resolve managed Git: %w; legacy test Git: %s", err, strings.TrimSpace(string(stderr)))
		}
		return "", fmt.Errorf("resolve managed Git: %w", err)
	}
	spec, err := managed.Process(managed.GitPath, args...)
	if err != nil {
		return "", err
	}
	spec.Dir = dir
	var output, diagnostics strings.Builder
	spec.Stdout = &output
	spec.Stderr = &diagnostics
	err = spec.Run(context.Background())
	if err != nil {
		message := strings.TrimSpace(diagnostics.String())
		if message == "" {
			message = strings.TrimSpace(output.String())
		}
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), message, err)
	}
	return output.String(), nil
}
