package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var unpublished = []string{"DESIGN.md", "PRODUCT.md", "docs/spec.md", "docs/e2e.md", "docs/dogfood/", "docs/images/desktop/"}

func unpublishedPath(rel string) bool {
	rel = path.Clean(rel)
	for _, p := range unpublished {
		if rel == p || strings.HasSuffix(p, "/") && strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}

func manifest(root string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, "www", "docs.txt"))
	if err != nil {
		return nil, err
	}
	var out []string
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, err := source(root, line); err != nil {
			return nil, fmt.Errorf("www/docs.txt:%d: %w", i+1, err)
		}
		if unpublishedPath(line) {
			return nil, fmt.Errorf("www/docs.txt:%d: %q is unpublished", i+1, line)
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("www/docs.txt lists no docs")
	}
	return out, nil
}

func source(root, rel string) (string, error) {
	if strings.ContainsAny(rel, "\\\x00") || !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", fmt.Errorf("%q is not inside the repository", rel)
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", fmt.Errorf("%q is not a file in the repository", rel)
	}
	real, err := filepath.Rel(rootReal, full)
	if err != nil || !filepath.IsLocal(real) {
		return "", fmt.Errorf("%q leads outside the repository", rel)
	}
	fi, err := os.Stat(full)
	if err != nil || !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%q is not a file in the repository", rel)
	}
	return full, nil
}
