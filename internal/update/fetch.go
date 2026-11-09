package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/desktopasset"
)

type Build struct {
	Path, Version, Channel, Commit string
	Schema                         int
	Luvus, Iroh                    string
}

var luvusTriples = map[string]string{
	"linux/amd64":  "x86_64-unknown-linux-musl",
	"linux/arm64":  "aarch64-unknown-linux-musl",
	"darwin/amd64": "x86_64-apple-darwin",
	"darwin/arm64": "aarch64-apple-darwin",

	"windows/amd64": "x86_64-pc-windows-msvc",
}

var client = &http.Client{Timeout: 5 * time.Minute}

var ErrMissing = errors.New("not published")

const maxBinary = 512 << 20

func releaseURL(base, channel string) string {
	if channel == "stable" {
		return strings.TrimRight(base, "/") + "/latest/download"
	}
	return strings.TrimRight(base, "/") + "/download/edge"
}

func FetchHand(ctx context.Context, base, channel, goos, arch, dir string, env []string) (Build, error) {
	url := releaseURL(base, channel)
	stem := "hand-" + goos + "-" + arch
	ext, exe := packaging(goos)
	sums := "checksums.txt"
	if goos == "windows" {
		sums = stem + ".sha256"
	}
	if err := fetchChecked(ctx, url, dir, stem+ext, sums); err != nil {
		return Build{}, err
	}
	bin := filepath.Join(dir, "hand"+exe)
	if err := extract(filepath.Join(dir, stem+ext), "hand"+exe, bin); err != nil {
		return Build{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "version")
	cmd.Env, cmd.WaitDelay = env, time.Second
	out, err := cmd.Output()
	if err != nil {
		return Build{}, fmt.Errorf("update: %s version: %w", bin, err)
	}
	b := parseBuild(string(out))
	b.Path = bin
	return b, nil
}

func FetchLuvus(ctx context.Context, base, version, goos, arch, dir string) (string, error) {
	triple, ok := luvusTriples[goos+"/"+arch]
	if !ok {
		return "", fmt.Errorf("update: no Luvus build for %s/%s", goos, arch)
	}
	name := "luvus-v" + version + "-" + triple
	ext, exe := packaging(goos)
	if err := fetchChecked(ctx, strings.TrimRight(base, "/")+"/download/v"+version, dir, name+ext, name+".sha256"); err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "luvus"+exe)
	return bin, extract(filepath.Join(dir, name+ext), "luvus"+exe, bin)
}

func FetchIroh(ctx context.Context, base, channel, goos, arch, dir string) (string, error) {
	stem := "hand-iroh-" + goos + "-" + arch
	ext, exe := packaging(goos)
	if err := fetchChecked(ctx, releaseURL(base, channel), dir, stem+ext, stem+".sha256"); err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "hand-iroh"+exe)
	return bin, extract(filepath.Join(dir, stem+ext), "hand-iroh"+exe, bin)
}

func FetchDesktop(ctx context.Context, base, channel, goos, arch, dir string) (string, error) {
	stem := desktopasset.Name + "-" + goos + "-" + arch
	ext, exe := packaging(goos)
	if err := fetchChecked(ctx, releaseURL(base, channel), dir, stem+ext, stem+".sha256"); err != nil {
		return "", err
	}
	bin := filepath.Join(dir, desktopasset.Name+exe)
	return bin, extract(filepath.Join(dir, stem+ext), desktopasset.Name+exe, bin)
}

func probeDesktop(ctx context.Context, base, channel, goos, arch string) (bool, error) {
	return probeAsset(ctx, base, channel, desktopasset.Name, goos, arch)
}

func probeIroh(ctx context.Context, base, channel, goos, arch string) (bool, error) {
	return probeAsset(ctx, base, channel, "hand-iroh", goos, arch)
}

func probeAsset(ctx context.Context, base, channel, name, goos, arch string) (bool, error) {
	ext, _ := packaging(goos)
	url := releaseURL(base, channel) + "/" + name + "-" + goos + "-" + arch + ext
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false, err
	}
	res, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("update: %w", err)
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	}
	return false, fmt.Errorf("update: %s: %s", url, res.Status)
}

func packaging(goos string) (ext, exe string) {
	if goos == "windows" {
		return ".zip", ".exe"
	}
	return ".tar.gz", ""
}

func Newer(a, b string) bool {
	x, ok := semver(a)
	if !ok {
		return false
	}
	y, ok := semver(b)
	if !ok {
		return true
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

func semver(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func parseBuild(out string) Build {
	var b Build
	for line := range strings.Lines(out) {
		key, value, _ := strings.Cut(strings.TrimSpace(line), ": ")
		switch key {
		case "version":
			b.Version = value
		case "channel":
			b.Channel = value
		case "commit":
			b.Commit = value
		case "schema":
			b.Schema, _ = strconv.Atoi(value)
		case "luvus":
			b.Luvus = value
		case "iroh":
			b.Iroh = value
		}
	}
	return b
}

func fetchChecked(ctx context.Context, url, dir, asset, sums string) error {
	for i, name := range []string{asset, sums} {
		if err := download(ctx, url+"/"+name, filepath.Join(dir, name)); err != nil {
			if i > 0 && errors.Is(err, ErrMissing) {
				err = errors.New(err.Error())
			}
			return err
		}
	}
	want, err := listed(filepath.Join(dir, sums), asset)
	if err != nil {
		return err
	}
	f, err := os.Open(filepath.Join(dir, asset))
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if want == "" || hex.EncodeToString(h.Sum(nil)) != want {
		return fmt.Errorf("checksum mismatch for %s", asset)
	}
	return nil
}

func listed(path, asset string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if fields := strings.Fields(sc.Text()); len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == asset {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", sc.Err()
}

func download(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return fmt.Errorf("update: %s: %s: %w", url, res.Status, ErrMissing)
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("update: %s: %s", url, res.Status)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, res.Body); err != nil {
		_ = f.Close()
		return fmt.Errorf("update: %s: %w", url, err)
	}
	return f.Close()
}

func extract(archive, name, dest string) error {
	if strings.HasSuffix(archive, ".zip") {
		return unzip(archive, name, dest)
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("update: %s: %w", archive, err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("update: %s holds no %s", filepath.Base(archive), name)
		}
		if err != nil {
			return fmt.Errorf("update: %s: %w", archive, err)
		}
		if hdr.Typeflag != tar.TypeReg || strings.TrimPrefix(hdr.Name, "./") != name {
			continue
		}
		if hdr.Size > maxBinary {
			return fmt.Errorf("update: %s in %s is %d bytes, more than %d", name, filepath.Base(archive), hdr.Size, maxBinary)
		}
		out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, io.LimitReader(tr, maxBinary)); err != nil {
			_ = out.Close()
			_ = os.Remove(dest)
			return fmt.Errorf("update: %s: %w", archive, err)
		}
		return out.Close()
	}
}

func unzip(archive, name, dest string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("update: %s: %w", archive, err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		if f.UncompressedSize64 > maxBinary {
			return fmt.Errorf("update: %s in %s is %d bytes, more than %d", name, filepath.Base(archive), f.UncompressedSize64, maxBinary)
		}
		in, err := f.Open()
		if err != nil {
			return fmt.Errorf("update: %s: %w", archive, err)
		}
		defer in.Close()
		out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, io.LimitReader(in, maxBinary)); err != nil {
			_ = out.Close()
			_ = os.Remove(dest)
			return fmt.Errorf("update: %s: %w", archive, err)
		}
		return out.Close()
	}
	return fmt.Errorf("update: %s holds no %s", filepath.Base(archive), name)
}
