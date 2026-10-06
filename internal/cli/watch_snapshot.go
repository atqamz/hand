package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	snapshotKeep   = 7
	snapshotLayout = "20060102"
)

func (w *watcher) snapshot(ctx context.Context) {
	day := w.r.env.Now().UTC().Format(snapshotLayout)
	if day == w.snapped {
		return
	}
	w.snapped = day
	dir := filepath.Join(w.r.root, "backups", w.r.fleet.ID)
	path := filepath.Join(dir, "hand-"+day+".db")
	err := os.MkdirAll(dir, 0o700)
	if _, statErr := os.Lstat(path); err == nil && statErr != nil {
		tmp := path + ".tmp"
		_ = os.Remove(tmp)
		if err = w.st.Backup(ctx, tmp); err == nil {
			err = os.Rename(tmp, path)
		}
	}
	if err != nil {
		w.say("snapshot: " + err.Error())
		return
	}
	if err = pruneSnapshots(dir); err != nil {
		w.say("snapshot prune: " + err.Error())
	}
}

func pruneSnapshots(dir string) error {
	des, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var errs []error
	var kept []string
	for _, de := range des {
		if strings.HasPrefix(de.Name(), "hand-") && strings.HasSuffix(de.Name(), ".db.tmp") {
			errs = append(errs, os.Remove(filepath.Join(dir, de.Name())))
			continue
		}
		if day, ok := strings.CutPrefix(de.Name(), "hand-"); ok {
			if day, ok = strings.CutSuffix(day, ".db"); ok {
				if _, err := time.Parse(snapshotLayout, day); err == nil && len(day) == len(snapshotLayout) {
					kept = append(kept, de.Name())
				}
			}
		}
	}
	slices.Sort(kept)
	for len(kept) > snapshotKeep {
		errs = append(errs, os.Remove(filepath.Join(dir, kept[0])))
		kept = kept[1:]
	}
	return errors.Join(errs...)
}
