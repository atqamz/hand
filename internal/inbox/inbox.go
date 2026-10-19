package inbox

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	Folder    = "inbox"
	Trash     = ".trash"
	TrashKeep = 7 * 24 * time.Hour
	mark      = ".pruned"
)

var (
	Name = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9]+(-.+|\.[A-Za-z0-9]+)$`)
	days = regexp.MustCompile(`^[1-9][0-9]{0,3}d$`)
)

type File struct {
	Name string
	Size int64
	Mod  time.Time
}

type Usage struct {
	Files          int
	Bytes          int64
	Oldest, Newest time.Time
	TrashFiles     int
	TrashBytes     int64
}

type Failure struct {
	Name string
	Err  error
}

type Pruned struct {
	Moved, Deleted []File
	Failed         []Failure
}

func ParseAge(s string) (time.Duration, bool) {
	if !days.MatchString(s) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
	return time.Duration(n) * 24 * time.Hour, err == nil
}

func MiB(n int64) string {
	return strings.TrimSuffix(strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64), ".0") + " MiB"
}

func Count(n int, bytes int64) string {
	noun := "files"
	if n == 1 {
		noun = "file"
	}
	return fmt.Sprintf("%d %s, %s", n, noun, MiB(bytes))
}

func Bytes(files []File) int64 {
	var n int64
	for _, f := range files {
		n += f.Size
	}
	return n
}

func Size(home string) (Usage, error) {
	trash, err := trashDir(home)
	if err != nil {
		return Usage{}, err
	}
	files, err := scan(filepath.Join(home, Folder))
	if err != nil {
		return Usage{}, err
	}
	held, err := scan(trash)
	if err != nil {
		return Usage{}, err
	}
	u := Usage{Files: len(files), Bytes: Bytes(files), TrashFiles: len(held), TrashBytes: Bytes(held)}
	for i, f := range files {
		if i == 0 || f.Mod.Before(u.Oldest) {
			u.Oldest = f.Mod
		}
		if f.Mod.After(u.Newest) {
			u.Newest = f.Mod
		}
	}
	return u, nil
}

func Prune(home string, now time.Time, olderThan time.Duration, dry bool) (Pruned, error) {
	dir := filepath.Join(home, Folder)
	trash, err := trashDir(home)
	if err != nil {
		return Pruned{}, err
	}
	held, err := scan(trash)
	if err != nil {
		return Pruned{}, err
	}
	files, err := scan(dir)
	if err != nil {
		return Pruned{}, err
	}
	var p Pruned
	for _, f := range held {
		if now.Sub(f.Mod) <= TrashKeep {
			continue
		}
		if !dry {
			if err := os.Remove(filepath.Join(trash, f.Name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				p.Failed = append(p.Failed, Failure{filepath.Join(Trash, f.Name), err})
				continue
			}
		}
		p.Deleted = append(p.Deleted, f)
	}
	for _, f := range files {
		if now.Sub(f.Mod) <= olderThan {
			continue
		}
		if !dry {
			if err := move(dir, trash, f, now); err != nil {
				p.Failed = append(p.Failed, Failure{f.Name, err})
				continue
			}
		}
		p.Moved = append(p.Moved, f)
	}
	return p, nil
}

func Marked(home, day string) bool {
	b, err := os.ReadFile(filepath.Join(home, Folder, Trash, mark))
	return err == nil && strings.TrimSpace(string(b)) == day
}

func Mark(home, day string) error {
	trash, err := trashDir(home)
	if err != nil {
		return err
	}
	if err := os.Mkdir(trash, 0o700); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	tmp, err := os.CreateTemp(trash, mark+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString(day + "\n")
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(trash, mark))
}

func move(dir, trash string, f File, now time.Time) error {
	if err := os.Mkdir(trash, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	from, to := filepath.Join(dir, f.Name), filepath.Join(trash, f.Name)
	if _, err := os.Lstat(to); err == nil {
		return errors.New("a file of that name is already in the trash")
	}
	info, err := os.Lstat(from)
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("no longer a regular file")
	}
	if err != nil {
		return err
	}
	if err := os.Chtimes(from, now, now); err != nil {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		return errors.Join(err, os.Chtimes(from, time.Time{}, f.Mod))
	}
	return nil
}

func trashDir(home string) (string, error) {
	trash := filepath.Join(home, Folder, Trash)
	for _, dir := range []string{filepath.Dir(trash), trash} {
		info, err := os.Lstat(dir)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return trash, nil
		case err != nil:
			return "", err
		case !info.IsDir():
			return "", fmt.Errorf("%s is a symlink or a file, not a folder; Hand prunes only a real folder", dir)
		}
	}
	return trash, nil
}

func scan(dir string) ([]File, error) {
	des, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []File
	for _, de := range des {
		if !Name.MatchString(de.Name()) {
			continue
		}
		info, err := de.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode().IsRegular() {
			out = append(out, File{de.Name(), info.Size(), info.ModTime()})
		}
	}
	return out, nil
}
