package fleet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/fsx"
	"github.com/atqamz/hand/internal/luvus"
)

func LuvusStore(home string) string {
	return filepath.Join(home, "luvus", runtime.GOOS+"-"+runtime.GOARCH)
}

func Backups(home string) string { return filepath.Join(home, "backups") }

func ModelsCache(home string) string { return filepath.Join(home, "cache", "models") }

func OldLuvusStore(root string) string { return filepath.Join(root, "luvus") }

func OldBackups(root, id string) string { return filepath.Join(root, "backups", id) }

func OldModels(root string) string { return filepath.Join(root, "models") }

func LoadPin(root, home string) (luvus.Pin, bool, error) {
	if home != "" {
		pin, ok, err := luvus.LoadPin(LuvusStore(home))
		if ok || err != nil {
			return pin, ok, err
		}
	}
	return luvus.LoadPin(OldLuvusStore(root))
}

func Migrate(ctx context.Context, root, id, home string, env []string, now time.Time) ([]string, error) {
	var lines []string
	var errs []error
	note := func(line string, err error) {
		if line != "" {
			lines = append(lines, line)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	note(migratePin(ctx, root, home, env, now))
	note(migrateFiles("backups", OldBackups(root, id), Backups(home), nil))
	note(migrateFiles("models", OldModels(root), ModelsCache(home), func(name string) bool { return strings.HasSuffix(name, ".json") }))
	return lines, errors.Join(errs...)
}

func migratePin(ctx context.Context, root, home string, env []string, now time.Time) (string, error) {
	store := LuvusStore(home)
	if _, ok, err := luvus.LoadPin(store); ok || err != nil {
		return "", nil
	}
	old, ok, err := luvus.LoadPin(OldLuvusStore(root))
	if !ok || err != nil {
		return "", err
	}
	if err := old.Verify(); err != nil {
		return "", fmt.Errorf("the machine's Luvus pin was not copied into the fleet: %w", err)
	}
	pin, err := luvus.Keep(ctx, store, old.Path, env, now)
	if err != nil {
		return "", fmt.Errorf("the machine's Luvus pin was not copied into the fleet (%w); the machine copy stays in use", err)
	}
	return fmt.Sprintf("luvus %s copied to %s; a running server keeps %s until it restarts", pin.Version, store, old.Path), nil
}

func migrateFiles(label, from, to string, keep func(string) bool) (string, error) {
	des, err := os.ReadDir(from)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	n := 0
	for _, de := range des {
		name := de.Name()
		if !de.Type().IsRegular() || strings.HasSuffix(name, ".tmp") || keep != nil && !keep(name) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(to, name)); err == nil {
			continue
		}
		if err := copyInto(filepath.Join(from, name), to, name); err != nil {
			return "", fmt.Errorf("%s: %w", label, err)
		}
		n++
	}
	if n == 0 {
		return "", nil
	}
	return fmt.Sprintf("%s: %d files copied to %s; the machine copies stay", label, n, to), nil
}

func copyInto(src, dir, name string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dir, "."+name+".*.tmp")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, in)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = fsx.Rename(tmp.Name(), filepath.Join(dir, name))
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

type restorePoint struct {
	path string
	at   time.Time
}

func RestorePoints(root, id, home string) []string {
	var found []restorePoint
	seen := map[string]bool{}
	for _, dir := range []string{Backups(home), OldBackups(root, id)} {
		des, _ := os.ReadDir(dir)
		for _, de := range des {
			name := de.Name()
			if !strings.HasPrefix(name, "hand.db.") && !(strings.HasPrefix(name, "hand-") && strings.HasSuffix(name, ".db")) {
				continue
			}
			if fi, err := de.Info(); err == nil && fi.Mode().IsRegular() && !seen[name] {
				seen[name] = true
				found = append(found, restorePoint{filepath.Join(dir, name), fi.ModTime()})
			}
		}
	}
	slices.SortStableFunc(found, func(a, b restorePoint) int { return b.at.Compare(a.at) })
	out := make([]string, len(found))
	for i, f := range found {
		out[i] = f.path
	}
	return out
}
