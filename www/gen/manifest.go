package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var unpublished = map[string]bool{"DESIGN.md": true, "PRODUCT.md": true, "docs/spec.md": true, "docs/e2e.md": true}

var unpublishedPrefixes = []string{"docs/dogfood/", "docs/images/desktop/"}

func unpublishedPath(rel string) bool {
	rel = path.Clean(rel)
	if unpublished[rel] {
		return true
	}
	for _, prefix := range unpublishedPrefixes {
		if strings.HasPrefix(rel, prefix) {
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
	listed := map[string]bool{}
	for _, src := range out {
		listed[path.Clean(src)] = true
	}
	entries, err := os.ReadDir(filepath.Join(root, "docs"))
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		rel := "docs/" + e.Name()
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || listed[rel] || unpublishedPath(rel) {
			continue
		}
		return nil, fmt.Errorf("%s is in neither www/docs.txt nor the unpublished list", rel)
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
