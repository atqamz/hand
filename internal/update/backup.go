package update

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/state"
)

const stampLayout = "20060102T150405"

func Backup(ctx context.Context, root, target string, fleets []fleet.Entry, now time.Time) ([]string, error) {
	stamp := now.UTC().Format(stampLayout)
	var out []string
	for _, e := range fleets {
		if e.State != "ok" {
			continue
		}
		dir := filepath.Join(root, "backups", e.ID)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return out, err
		}
		path := filepath.Join(dir, "hand.db."+stamp)
		if err := backupStore(ctx, e.Home, path, now); err != nil {
			return out, err
		}
		out = append(out, path)
		if err := prune(dir, "hand.db."); err != nil {
			return out, err
		}
	}
	dir := filepath.Join(root, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return out, err
	}
	path := filepath.Join(dir, "hand."+stamp)
	if err := copyFile(target, path, 0o755); err != nil {
		return out, err
	}
	out = append(out, path)
	return out, prune(dir, "hand.")
}

func backupStore(ctx context.Context, home, path string, now time.Time) error {
	st, err := state.Open(filepath.Join(home, "hand.db"), func() time.Time { return now })
	if err != nil {
		return err
	}
	defer st.Close()
	return st.Backup(ctx, path)
}

func prune(dir, prefix string) error {
	des, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var kept []string
	for _, de := range des {
		name := de.Name()
		if rest, ok := strings.CutPrefix(name, prefix); ok && len(rest) == len(stampLayout) {
			if _, err := time.Parse(stampLayout, rest); err == nil {
				kept = append(kept, name)
			}
		}
	}
	slices.Sort(kept)
	for len(kept) > 2 {
		if err := os.Remove(filepath.Join(dir, kept[0])); err != nil {
			return err
		}
		kept = kept[1:]
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}
