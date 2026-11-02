package retention

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

var Folders = []string{"reports", "shots"}

type File struct {
	Path string
	Size int64
	Mod  time.Time
}

type Group struct {
	Path   string
	Files  int
	Bytes  int64
	Oldest time.Time
}

type Failure struct {
	Path string
	Err  error
}

func Scan(home string) ([]File, error) {
	var out []File
	for _, name := range Folders {
		real := filepath.Join(home, name)
		info, err := os.Lstat(real)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%s is not a real folder; Hand does not follow a link or a file there", name)
		}
		err = filepath.WalkDir(real, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if !e.Type().IsRegular() {
				return nil
			}
			info, err := e.Info()
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(real, p)
			if err != nil {
				return err
			}
			out = append(out, File{Path: path.Join(name, filepath.ToSlash(rel)), Size: info.Size(), Mod: info.ModTime()})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func Older(files []File, now time.Time, age time.Duration) []File {
	return slices.DeleteFunc(slices.Clone(files), func(f File) bool { return now.Sub(f.Mod) < age })
}

func Bytes(files []File) int64 {
	var n int64
	for _, f := range files {
		n += f.Size
	}
	return n
}

func Scope(arg string) (string, error) {
	clean := path.Clean(filepath.ToSlash(arg))
	for _, name := range Folders {
		if clean == name || strings.HasPrefix(clean, name+"/") {
			return clean, nil
		}
	}
	return "", fmt.Errorf("%q is not inside %s", arg, strings.Join(Folders, " or "))
}

func Within(files []File, scopes []string) []File {
	if len(scopes) == 0 {
		return files
	}
	return slices.DeleteFunc(slices.Clone(files), func(f File) bool {
		return !slices.ContainsFunc(scopes, func(s string) bool { return f.Path == s || strings.HasPrefix(f.Path, s+"/") })
	})
}

func Groups(files []File) []Group {
	by := map[string]*Group{}
	for _, f := range files {
		key := f.Path
		if parts := strings.SplitN(f.Path, "/", 3); len(parts) == 3 {
			key = parts[0] + "/" + parts[1]
		}
		g := by[key]
		if g == nil {
			g = &Group{Path: key, Oldest: f.Mod}
			by[key] = g
		}
		g.Files++
		g.Bytes += f.Size
		if f.Mod.Before(g.Oldest) {
			g.Oldest = f.Mod
		}
	}
	out := make([]Group, 0, len(by))
	for _, g := range by {
		out = append(out, *g)
	}
	slices.SortFunc(out, func(a, b Group) int {
		if a.Bytes != b.Bytes {
			return int(b.Bytes - a.Bytes)
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out
}

func Remove(home string, files []File) (removed []File, failed []Failure) {
	roots := map[string]*os.Root{}
	defer func() {
		for _, r := range roots {
			_ = r.Close()
		}
	}()
	dirs := map[string]bool{}
	for _, f := range files {
		name, rel, _ := strings.Cut(f.Path, "/")
		root, ok := roots[name]
		if !ok {
			root = openFolder(filepath.Join(home, name))
			roots[name] = root
		}
		if root == nil {
			failed = append(failed, Failure{f.Path, fmt.Errorf("%s is not a real folder", name)})
			continue
		}
		info, err := root.Lstat(rel)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err == nil && (!info.Mode().IsRegular() || !info.ModTime().Equal(f.Mod) || info.Size() != f.Size) {
			continue
		}
		if err == nil {
			err = root.Remove(rel)
		}
		if err != nil {
			failed = append(failed, Failure{f.Path, err})
			continue
		}
		removed = append(removed, f)
		for d := path.Dir(rel); d != "."; d = path.Dir(d) {
			dirs[name+"/"+d] = true
		}
	}
	empty := make([]string, 0, len(dirs))
	for d := range dirs {
		empty = append(empty, d)
	}
	slices.SortFunc(empty, func(a, b string) int { return len(b) - len(a) })
	for _, d := range empty {
		name, rel, _ := strings.Cut(d, "/")
		if root := roots[name]; root != nil {
			_ = root.Remove(rel)
		}
	}
	return removed, failed
}

func openFolder(dir string) *os.Root {
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return nil
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil
	}
	return root
}

func GcCommand(age time.Duration) string {
	if age <= 0 {
		age = 30 * 24 * time.Hour
	}
	return fmt.Sprintf("hand gc --older-than %dd", age/(24*time.Hour))
}

func Size(n int64) string {
	switch {
	case n >= 1<<40:
		return fmt.Sprintf("%.1f TB", float64(n)/(1<<40))
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	}
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
}

func Warning(files []File, now time.Time, limit int64, age time.Duration) string {
	var parts []string
	if total := Bytes(files); limit > 0 && total > limit {
		parts = append(parts, fmt.Sprintf("%s hold %s, over the %s limit", strings.Join(Folders, "/ and ")+"/", Size(total), Size(limit)))
	}
	if age > 0 {
		if old := Older(files, now, age); len(old) == 1 {
			parts = append(parts, fmt.Sprintf("1 file is older than %dd (%s)", age/(24*time.Hour), Size(Bytes(old))))
		} else if len(old) > 1 {
			parts = append(parts, fmt.Sprintf("%d files are older than %dd (%s)", len(old), age/(24*time.Hour), Size(Bytes(old))))
		}
	}
	return strings.Join(parts, "; ")
}
