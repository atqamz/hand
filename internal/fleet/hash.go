package fleet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var hashSkipDirs = map[string]bool{".git": true, "__pycache__": true, "__pypackages__": true}

func ComputedHash(dir string) (string, error) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	type file struct {
		rel  string
		data []byte
	}
	var files []file
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && hashSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "metadata.json" {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(path)
			if err != nil {
				return nil
			}
			if info, err := os.Stat(target); err != nil || !info.Mode().IsRegular() {
				return nil
			}
			if inside, err := filepath.Rel(root, target); err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
				return nil
			}
		} else if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Contains(b, []byte{0}) {
			b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
		}
		files = append(files, file{filepath.ToSlash(rel), b})
		return nil
	})
	if err != nil {
		return "", err
	}
	slices.SortFunc(files, func(a, b file) int { return strings.Compare(a.rel, b.rel) })
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%d\x00", f.rel, len(f.data))
		h.Write(f.data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
