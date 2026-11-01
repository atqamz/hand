package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/toon"
)

func init() {
	attemptCommands["gc"] = cmdAttemptGC
}

var scratchFields = []string{"path", "bytes"}

func dirBytes(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			if info, err := e.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

func scratchRows(paths []string) ([][]string, int64) {
	var total int64
	rows := make([][]string, 0, len(paths))
	for _, p := range paths {
		n := dirBytes(p)
		total += n
		rows = append(rows, []string{p, strconv.FormatInt(n, 10)})
	}
	return rows, total
}

func (r *runner) leftovers(name, worktree string) []string {
	return harness.Leftovers(name, worktree, os.Getuid(), r.env.Getenv)
}

func removeAll(paths []string) error {
	for _, p := range paths {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	return nil
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
	var paths []string
	for _, p := range harness.Orphans("claude", filepath.Join(root, "worktrees"), os.Getuid(), r.env.Getenv) {
		if info, err := os.Lstat(p); err == nil && time.Since(info.ModTime()) >= *older {
			paths = append(paths, p)
		}
	}
	rows, total := scratchRows(paths)
	var d toon.Doc
	d.Field("applied", strconv.FormatBool(*apply))
	d.Rows("orphans", scratchFields, rows)
	d.Field("total_bytes", strconv.FormatInt(total, 10))
	if *apply {
		if err := removeAll(paths); err != nil {
			return err
		}
	} else if len(paths) > 0 {
		d.Help("Remove them with `hand attempt gc --apply`; transcripts are never touched")
	}
	return r.print(&d)
}
