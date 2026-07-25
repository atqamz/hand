package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const Repo = "atqamz/secondhand"

const binaryName = "hand"

func AssetName() string {
	return fmt.Sprintf("%s-%s-%s.tar.gz", binaryName, runtime.GOOS, runtime.GOARCH)
}

func LatestTag(repo string) (string, error) {
	return latestTag(context.Background(), repo)
}

func latestTag(ctx context.Context, repo string) (string, error) {
	out, err := runGH(ctx, "release", "view", "--repo", repo, "--json", "tagName", "--jq", ".tagName")
	if err != nil {
		return "", fmt.Errorf("query latest release: %w", err)
	}
	if out == "" {
		return "", fmt.Errorf("query latest release: empty tag name")
	}
	return out, nil
}

// IsNewer reports whether latest is newer than current. A current version that
// doesn't parse as semver (e.g. "dev", an unversioned local build) is always
// considered outdated, since there's no way to prove it already matches latest.
func IsNewer(latest, current string) (bool, error) {
	lMajor, lMinor, lPatch, err := parseSemver(latest)
	if err != nil {
		return false, fmt.Errorf("parse latest version %q: %w", latest, err)
	}
	cMajor, cMinor, cPatch, err := parseSemver(current)
	if err != nil {
		return true, nil
	}
	if lMajor != cMajor {
		return lMajor > cMajor, nil
	}
	if lMinor != cMinor {
		return lMinor > cMinor, nil
	}
	return lPatch > cPatch, nil
}

func parseSemver(s string) (major, minor, patch int, err error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("invalid version %q", s)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("invalid version %q", s)
		}
		nums[i] = n
	}
	return nums[0], nums[1], nums[2], nil
}

// Apply downloads the release tagged tag from repo, verifies its checksum, and
// replaces the running binary in place. The replacement is atomic: the new
// binary is written to a temp file in the same directory as the running
// binary, then renamed over it, so a crash mid-update never leaves a partial
// binary at the real path.
// executableOverride lets tests point Apply at a fake binary path instead of
// the real test binary produced by `go test`.
var executableOverride = os.Executable

func Apply(repo, tag string) error {
	execPath, err := executableOverride()
	if err != nil {
		return fmt.Errorf("locate running binary: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolve running binary path: %w", err)
	}

	assetName := AssetName()

	tmpDir, err := os.MkdirTemp("", "hand-update-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	if err := downloadAssets(context.Background(), repo, tag, tmpDir, assetName, "checksums.txt"); err != nil {
		return fmt.Errorf("download release assets: %w", err)
	}
	if err := verifyChecksum(tmpDir, assetName); err != nil {
		return err
	}

	staged, err := os.CreateTemp(filepath.Dir(execPath), ".hand-update-*")
	if err != nil {
		return fmt.Errorf("stage new binary: %w", err)
	}
	tmpBinary := staged.Name()
	defer func() { _ = os.Remove(tmpBinary) }()
	if err := staged.Close(); err != nil {
		return fmt.Errorf("stage new binary: %w", err)
	}

	if err := extractBinary(filepath.Join(tmpDir, assetName), tmpBinary); err != nil {
		return err
	}

	if err := os.Rename(tmpBinary, execPath); err != nil {
		return fmt.Errorf("replace running binary: %w", err)
	}
	return nil
}

func downloadAssets(ctx context.Context, repo, tag, dir string, patterns ...string) error {
	args := []string{"release", "download", tag, "--repo", repo, "--dir", dir, "--clobber"}
	for _, p := range patterns {
		args = append(args, "--pattern", p)
	}
	_, err := runGH(ctx, args...)
	return err
}

func verifyChecksum(dir, assetName string) error {
	data, err := os.ReadFile(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		return fmt.Errorf("read checksums.txt: %w", err)
	}

	want := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name == assetName {
			want = fields[0]
			break
		}
	}
	if want == "" {
		return fmt.Errorf("checksums.txt has no entry for %s", assetName)
	}

	f, err := os.Open(filepath.Join(dir, assetName))
	if err != nil {
		return fmt.Errorf("open %s: %w", assetName, err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash %s: %w", assetName, err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s: want %s, got %s", assetName, want, got)
	}
	return nil
}

func extractBinary(tarGzPath, destPath string) error {
	f, err := os.Open(tarGzPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", tarGzPath, err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open gzip stream in %s: %w", tarGzPath, err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("%s not found in %s", binaryName, tarGzPath)
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", tarGzPath, err)
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != binaryName {
			continue
		}

		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return fmt.Errorf("write %s: %w", destPath, err)
		}
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			return fmt.Errorf("write %s: %w", destPath, err)
		}
		if err := out.Close(); err != nil {
			return fmt.Errorf("write %s: %w", destPath, err)
		}
		// OpenFile's mode only applies when it creates the file, and the
		// staging path already exists.
		return os.Chmod(destPath, 0o755)
	}
}

func runGH(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
