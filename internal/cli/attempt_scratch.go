package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/toon"
)

func init() {
	attemptCommands["gc"] = cmdAttemptGC
}

var scratchFields = []string{"path", "bytes"}

type scratchDir struct {
	path   string
	bytes  int64
	newest time.Time
}

func scanScratch(paths []string, older time.Duration) []scratchDir {
	var out []scratchDir
	for _, p := range paths {
		d := scratchDir{path: p}
		_ = filepath.WalkDir(p, func(_ string, e fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if info, err := e.Info(); err == nil {
				if !e.IsDir() {
					d.bytes += info.Size()
				}
				if info.ModTime().After(d.newest) {
					d.newest = info.ModTime()
				}
			}
			return nil
		})
		if time.Since(d.newest) >= older {
			out = append(out, d)
		}
	}
	return out
}

func scratchRows(dirs []scratchDir) ([][]string, int64) {
	var total int64
	rows := make([][]string, 0, len(dirs))
	for _, d := range dirs {
		total += d.bytes
		rows = append(rows, []string{d.path, strconv.FormatInt(d.bytes, 10)})
	}
	return rows, total
}

func (r *runner) leftovers(name, worktree string) []string {
	return harness.Leftovers(name, worktree, os.Getuid(), r.env.Getenv)
}

func removeScratch(dirs []scratchDir) (removed []scratchDir, warns []string) {
	for _, d := range dirs {
		if err := os.RemoveAll(d.path); err != nil {
			warns = append(warns, "scratch not removed: "+err.Error())
		} else {
			removed = append(removed, d)
		}
	}
	return removed, warns
}

func cmdAttemptGC(r *runner, args []string) error {
	set := flags("attempt gc")
	apply := set.Bool("apply", false, "remove the directories instead of listing them")
	older := set.Duration("older-than", 0, "only directories not modified for this long, for example 72h")
	if _, err := parse(set, args, 0); err != nil {
		return err
	}
	root, err := r.rootDir()
	if err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	live, err := st.LiveAttempts(r.ctx())
	if err != nil {
		return err
	}
	var running []string
	for _, a := range live {
		running = append(running, r.leftovers(a.Harness, a.Worktree)...)
	}
	orphans := slices.DeleteFunc(harness.Orphans("claude", filepath.Join(root, "worktrees"), os.Getuid(), r.env.Getenv), func(p string) bool {
		return slices.Contains(running, p)
	})
	dirs := scanScratch(orphans, *older)
	var warns []string
	if *apply {
		dirs, warns = removeScratch(dirs)
	}
	rows, total := scratchRows(dirs)
	var d toon.Doc
	d.Field("applied", strconv.FormatBool(*apply))
	d.Rows("orphans", scratchFields, rows)
	d.Field("total_bytes", strconv.FormatInt(total, 10))
	for _, w := range warns {
		d.Field("warn", w)
	}
	if !*apply && len(dirs) > 0 {
		d.Help("Remove them with `hand attempt gc --apply`; transcripts are never touched")
	}
	return r.print(&d)
}
