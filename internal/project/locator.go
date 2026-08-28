package project

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/atqamz/hand/internal/pathdisplay"
)

func IsFileLocator(value string) bool {
	u, err := url.Parse(value)
	return err == nil && strings.EqualFold(u.Scheme, "file")
}

func CanonicalFileLocator(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("make path absolute: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve path %s: %w", pathdisplay.Context(path), err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("make resolved path absolute: %w", err)
	}

	slashPath := filepath.ToSlash(resolved)
	u := &url.URL{Scheme: "file"}
	if runtime.GOOS == "windows" && len(slashPath) >= 2 && slashPath[1] == ':' {
		u.Path = "/" + slashPath
	} else {
		u.Path = slashPath
	}
	return u.String(), nil
}

func FileLocatorPath(locator string) (string, error) {
	u, err := url.Parse(locator)
	if err != nil || !strings.EqualFold(u.Scheme, "file") {
		return "", fmt.Errorf("invalid file locator %q", locator)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("file locator %q has query or fragment", locator)
	}
	path := u.Path
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		path = "//" + u.Host + "/" + strings.TrimPrefix(path, "/")
	}
	if path == "" {
		return "", fmt.Errorf("file locator %q has no path", locator)
	}
	if len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.Clean(filepath.FromSlash(path)), nil
}

func IsManagedPath(homeDir, path string) (bool, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, fmt.Errorf("resolve managed-path candidate %s: %w", pathdisplay.Context(path), err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return false, fmt.Errorf("make managed-path candidate absolute: %w", err)
	}
	entries, err := os.ReadDir(filepath.Join(homeDir, "projects"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		managed := filepath.Join(homeDir, "projects", entry.Name())
		managed, err = filepath.EvalSymlinks(managed)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("resolve managed project %q: %w", entry.Name(), err)
		}
		managed, err = filepath.Abs(managed)
		if err != nil {
			return false, fmt.Errorf("make managed project %q absolute: %w", entry.Name(), err)
		}
		if managed == resolved {
			return true, nil
		}
	}
	return false, nil
}
