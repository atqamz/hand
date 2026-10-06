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
)

var (
	ErrNotLuvus   = errors.New("not a luvus binary")
	ErrPinChanged = errors.New("luvus pin")
)

type Pin struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Version  string `json:"version"`
	Source   string `json:"source"`
	PinnedAt string `json:"pinned_at"`
}

const Tested = "0.14.3"

var versionLine = regexp.MustCompile(`^luvus (\d+\.\d+\.\d+)$`)

func pinFile(root string) string { return filepath.Join(root, "luvus", "pin.json") }

func LoadPin(root string) (Pin, bool, error) {
	b, err := os.ReadFile(pinFile(root))
	if errors.Is(err, os.ErrNotExist) {
		return Pin{}, false, nil
	}
	var p Pin
	if err == nil {
		err = json.Unmarshal(b, &p)
	}
	if err != nil {
		return Pin{}, false, fmt.Errorf("%w: cannot read %s (%v); re-pin with `hand luvus pin`", ErrPinChanged, pinFile(root), err)
	}
	return p, true, nil
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

func Keep(ctx context.Context, root, bin string, env []string, now time.Time) (Pin, error) {
	if fi, err := os.Stat(bin); err != nil || !executable(fi) {
		return Pin{}, fmt.Errorf("%w: %s is not an executable file", ErrNotLuvus, bin)
	}
	store := filepath.Join(root, "luvus")
	if err := os.MkdirAll(store, 0o755); err != nil {
		return Pin{}, err
	}
	if err := syncDir(root); err != nil {
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
		if err := rename(tmp, dest); err != nil {
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
	if err == nil {
		err = out.Chmod(0o555)
	}
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
	if err := rename(tmp.Name(), filepath.Join(store, "pin.json")); err != nil {
		return err
	}
	return syncDir(store)
}

func rename(oldpath, newpath string) error {
	deadline := time.Now().Add(2 * time.Second)
	for delay := time.Millisecond; ; delay *= 2 {
		err := os.Rename(oldpath, newpath)
		if err == nil || !held(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(min(delay, 100*time.Millisecond))
	}
}
