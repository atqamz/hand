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

var (
	hashSkipFiles = map[string]bool{"metadata.json": true}
	hashSkipDirs  = map[string]bool{".git": true, "__pycache__": true, "__pypackages__": true}
)

func ComputedHash(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	type file struct {
		rel  string
		data []byte
	}
	var files []file
	var walk func(path, prefix string, ancestors map[string]bool) error
	walk = func(path, prefix string, ancestors map[string]bool) error {
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		if ancestors[real] {
			return nil
		}
		branch := make(map[string]bool, len(ancestors)+1)
		for p := range ancestors {
			branch[p] = true
		}
		branch[real] = true
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, e := range entries {
			name := e.Name()
			if hashSkipFiles[name] {
				continue
			}
			full := filepath.Join(path, name)
			isDir := e.IsDir()
			if e.Type()&fs.ModeSymlink != 0 {
				target, err := filepath.EvalSymlinks(full)
				if err != nil {
					continue
				}
				if inside, err := filepath.Rel(root, target); err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
					continue
				}
				info, err := os.Stat(full)
				if err != nil {
					continue
				}
				isDir = info.IsDir()
				if !isDir && !info.Mode().IsRegular() {
					continue
				}
			} else if !isDir && !e.Type().IsRegular() {
				continue
			}
			if isDir {
				if hashSkipDirs[name] {
					continue
				}
				if err := walk(full, prefix+name+"/", branch); err != nil {
					return err
				}
				continue
			}
			b, err := os.ReadFile(full)
			if err != nil {
				return err
			}
			if !bytes.Contains(b, []byte{0}) {
				b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
			}
			files = append(files, file{filepath.ToSlash(prefix + name), b})
		}
		return nil
	}
	if err := walk(abs, "", map[string]bool{}); err != nil {
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
