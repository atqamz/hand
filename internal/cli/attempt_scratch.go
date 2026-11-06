package cli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

func init() {
	attemptCommands["gc"] = cmdAttemptGC
}

var scratchFields = []string{"path", "bytes"}

var attemptScratchKey = regexp.MustCompile(`^t[0-9]+-a[0-9]+$`)

func (r *runner) scratchDir(a state.Attempt) string {
	name := filepath.Base(a.Worktree)
	if !r.rootOK() || !attemptScratchKey.MatchString(name) {
		return ""
	}
	return filepath.Join(fleet.Scratch(r.root, r.fleet.ID), name)
}

func (r *runner) rootOK() bool { return r.root != "" && r.fleet.ID != "" }

func (r *runner) scratchOrphans(live []state.Attempt) []string {
	if !r.rootOK() {
		return nil
	}
	base := fleet.Scratch(r.root, r.fleet.ID)
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || !attemptScratchKey.MatchString(e.Name()) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(fleet.Worktrees(r.root, r.fleet.ID), e.Name())); errors.Is(err, fs.ErrNotExist) {
			out = append(out, filepath.Join(base, e.Name()))
		}
	}
	for _, a := range live {
		path := r.scratchDir(a)
		out = slices.DeleteFunc(out, func(p string) bool { return p == path })
	}
	return out
}

type scratchDir struct {
	path   string
	bytes  int64
	newest time.Time
}

func scanScratch(paths []string, older time.Duration) []scratchDir {
	var out []scratchDir
	for _, p := range paths {
		if _, err := os.Lstat(p); err != nil {
			continue
		}
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
	orphans = append(orphans, r.scratchOrphans(live)...)
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
