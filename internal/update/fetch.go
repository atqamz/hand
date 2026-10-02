package update

import (
	"archive/tar"
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
)

type Build struct {
	Path, Version, Channel, Commit string
	Schema                         int
	Luvus                          string
}

var luvusTriples = map[string]string{
	"linux/amd64":  "x86_64-unknown-linux-musl",
	"linux/arm64":  "aarch64-unknown-linux-musl",
	"darwin/amd64": "x86_64-apple-darwin",
	"darwin/arm64": "aarch64-apple-darwin",
}

var client = &http.Client{Timeout: 5 * time.Minute}

const maxBinary = 512 << 20

func FetchHand(ctx context.Context, base, channel, goos, arch, dir string, env []string) (Build, error) {
	url := strings.TrimRight(base, "/") + "/download/edge"
	if channel == "stable" {
		url = strings.TrimRight(base, "/") + "/latest/download"
	}
	asset := "hand-" + goos + "-" + arch + ".tar.gz"
	if err := fetchChecked(ctx, url, dir, asset, "checksums.txt"); err != nil {
		return Build{}, err
	}
	bin := filepath.Join(dir, "hand")
	if err := extract(filepath.Join(dir, asset), "hand", bin); err != nil {
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
	if err := fetchChecked(ctx, strings.TrimRight(base, "/")+"/download/v"+version, dir, name+".tar.gz", name+".sha256"); err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "luvus")
	return bin, extract(filepath.Join(dir, name+".tar.gz"), "luvus", bin)
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
		}
	}
	return b
}

func fetchChecked(ctx context.Context, url, dir, asset, sums string) error {
	for _, name := range []string{asset, sums} {
		if err := download(ctx, url+"/"+name, filepath.Join(dir, name)); err != nil {
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
