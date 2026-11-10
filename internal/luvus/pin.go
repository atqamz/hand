package luvus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/fsx"
)

var (
	ErrNotLuvus   = errors.New("not a luvus binary")
	ErrPinChanged = errors.New("luvus pin")
)

type Pin struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Version  string `json:"version"`
	Source   string `json:"-"`
	PinnedAt string `json:"pinned_at"`
}

const Tested = "0.14.3"

var versionLine = regexp.MustCompile(`^luvus (\d+\.\d+\.\d+)$`)

func pinFile(store string) string { return filepath.Join(store, "pin.json") }

func LoadPin(store string) (Pin, bool, error) { return loadPin(store, false) }

func LoadFleetPin(store string) (Pin, bool, error) { return loadPin(store, true) }

func loadPin(store string, inside bool) (Pin, bool, error) {
	b, err := os.ReadFile(pinFile(store))
	if errors.Is(err, os.ErrNotExist) {
		return Pin{}, false, nil
	}
	var p Pin
	if err == nil {
		err = json.Unmarshal(b, &p)
	}
	if err != nil {
		return Pin{}, false, fmt.Errorf("%w: cannot read %s (%v); re-pin with `hand luvus pin`", ErrPinChanged, pinFile(store), err)
	}
	if inside && filepath.IsAbs(p.Path) {
		return Pin{}, false, fmt.Errorf("%w: %s names the absolute path %q; a fleet's pin stays inside its folder; re-pin with `hand luvus pin`", ErrPinChanged, pinFile(store), p.Path)
	}
	if !filepath.IsAbs(p.Path) {
		rel := filepath.FromSlash(p.Path)
		if !filepath.IsLocal(rel) {
			return Pin{}, false, fmt.Errorf("%w: %s names %q, which is outside its folder; re-pin with `hand luvus pin`", ErrPinChanged, pinFile(store), p.Path)
		}
		p.Path = filepath.Join(store, rel)
	}
	if inside {
		if err := resolvesInside(store, p.Path); err != nil {
			return Pin{}, false, fmt.Errorf("%w: %s: %v; re-pin with `hand luvus pin`", ErrPinChanged, pinFile(store), err)
		}
	}
	return p, true, nil
}

func resolvesInside(store, path string) error {
	real, err := filepath.EvalSymlinks(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	base, err := filepath.EvalSymlinks(store)
	if err != nil {
		return err
	}
	if rel, err := filepath.Rel(base, real); err != nil || !filepath.IsLocal(rel) {
		return fmt.Errorf("%s resolves to %s, outside its folder", path, real)
	}
	return nil
}

func BinaryVersion(ctx context.Context, bin string, env []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := command(ctx, env, bin, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("%w: %s: --version failed: %v", ErrNotLuvus, bin, err)
	}
	first, _, _ := strings.Cut(string(out), "\n")
	m := versionLine.FindStringSubmatch(strings.TrimSpace(first))
	if m == nil {
		return "", fmt.Errorf("%w: %s printed %q, not `luvus X.Y.Z`", ErrNotLuvus, bin, strings.TrimSpace(first))
	}
	return m[1], nil
}

func Keep(ctx context.Context, store, bin string, env []string, now time.Time) (Pin, error) {
	if fi, err := os.Stat(bin); err != nil || !executable(fi) {
		return Pin{}, fmt.Errorf("%w: %s is not an executable file", ErrNotLuvus, bin)
	}
	if err := os.MkdirAll(store, 0o755); err != nil {
		return Pin{}, err
	}
	if err := syncDir(filepath.Dir(store)); err != nil {
		return Pin{}, err
	}
	ext := exeExt(bin)
	tmp, hash, err := copyHashed(store, bin, ext)
	if err != nil {
		return Pin{}, err
	}
	defer os.Remove(tmp)
	version, err := BinaryVersion(ctx, tmp, env)
	if err != nil {
		return Pin{}, fmt.Errorf("%w (copied from %s)", err, bin)
	}
	dest := filepath.Join(store, version+"-"+hash[:8], "luvus"+ext)
	if have, err := fileSum(dest); err != nil || have != hash {
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return Pin{}, err
		}
		if err := fsx.Rename(tmp, dest); err != nil {
			return Pin{}, err
		}
		if err := syncDir(filepath.Dir(dest)); err != nil {
			return Pin{}, err
		}
	}
	p := Pin{Path: dest, SHA256: hash, Version: version, Source: bin, PinnedAt: now.UTC().Format(time.RFC3339)}
	return p, writePin(store, p)
}

func (p Pin) Verify() error {
	if have, err := fileSum(p.Path); err != nil || have != p.SHA256 {
		return fmt.Errorf("%w: %s changed on disk; re-pin with `hand luvus pin`", ErrPinChanged, p.Path)
	}
	return nil
}

func copyHashed(dir, src, ext string) (string, string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", "", err
	}
	defer in.Close()
	out, err := os.CreateTemp(dir, ".luvus-*"+ext)
	if err != nil {
		return "", "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, h), in)
	if err == nil {
		err = out.Sync()
	}
	_ = out.Chmod(0o555)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(out.Name())
		return "", "", err
	}
	return out.Name(), hex.EncodeToString(h.Sum(nil)), nil
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writePin(store string, p Pin) error {
	rel, err := filepath.Rel(store, p.Path)
	if err != nil {
		return err
	}
	p.Path = filepath.ToSlash(rel)
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(store, ".pin-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(append(b, '\n'))
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := fsx.Rename(tmp.Name(), filepath.Join(store, "pin.json")); err != nil {
		return err
	}
	return syncDir(store)
}

func Match(pin Pin, pinned bool, version string, err error) (server, match string) {
	switch {
	case errors.Is(err, ErrUnreachable):
		return "not running", "not running"
	case err != nil || version == "":
		return "unknown", "unknown"
	case !pinned:
		return version, "unknown"
	case version == pin.Version:
		return version, "yes"
	}
	return version, "no"
}
