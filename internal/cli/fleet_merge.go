package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
	"github.com/atqamz/hand/internal/update"
)

func init() {
	fleetCommands["merge"] = cmdFleetMerge
}

var mergeDirs = []string{"memory", "reports", "briefs", "shots", "deliverables", "plans", "inbox", "projects"}

type mergeFile struct {
	src, dst string
}

type mergeDirCount struct {
	files, bytes, moved, same, saved int64
}

type mergeFilePlan struct {
	moves  []mergeFile
	manual []string
	counts map[string]*mergeDirCount
}

type mergeCollision struct{ path, reason string }

func cmdFleetMerge(r *runner, args []string) error {
	set := flags("fleet merge")
	dry := set.Bool("dry-run", false, "compute the merge and print its manifest, changing nothing")
	stop := set.Bool("stop", false, "stop the source's watcher and supervisor; workers are never stopped")
	archive := set.Bool("archive-source", false, "after a real merge, stop the source's Luvus server, remove its registry file and write a marker")
	cancel := set.Bool("cancel-timers", false, "cancel the source's armed timers, recording a timer.cancelled event for each")
	renames := map[string]string{}
	set.Func("rename-project", "OLD=NEW: rename a source project that collides with a destination project", func(v string) error {
		old, newName, ok := strings.Cut(v, "=")
		if !ok || old == "" || newName == "" {
			return fmt.Errorf("%w: --rename-project wants OLD=NEW, got %q", state.ErrInvalid, v)
		}
		if _, dup := renames[old]; dup {
			return fmt.Errorf("%w: project %s is renamed twice", state.ErrInvalid, old)
		}
		renames[old] = newName
		return nil
	})
	pos, err := parse(set, args, 3)
	if err != nil {
		return err
	}
	if !strings.EqualFold(pos[1], "INTO") {
		return usageError{"fleet merge: want SRC INTO DST, got " + strings.Join(pos, " ")}
	}
	root, err := r.rootDir()
	if err != nil {
		return err
	}
	entries, err := mergeFleets(root)
	if err != nil {
		return err
	}
	src, err := resolveMergeFleet(entries, root, pos[0])
	if err != nil {
		return err
	}
	dst, err := resolveMergeFleet(entries, root, pos[2])
	if err != nil {
		return err
	}
	if src.ID == dst.ID {
		return fmt.Errorf("%w: source and destination are the same fleet", state.ErrInvalid)
	}
	if within(src.Home, dst.Home) || within(dst.Home, src.Home) {
		return fmt.Errorf("%w: fleet homes are nested: %s and %s", state.ErrInvalid, src.Home, dst.Home)
	}
	srcPath, dstPath := filepath.Join(src.Home, "hand.db"), filepath.Join(dst.Home, "hand.db")
	if err := r.mergeCheck(root, src, dst, *stop, *cancel); err != nil {
		return err
	}
	plan, collisions, err := planMergeFiles(src.Home, dst.Home, src.ID)
	if err != nil {
		return err
	}
	if len(collisions) > 0 {
		var b strings.Builder
		b.WriteString("files exist on both sides with different content; move one aside first:")
		for _, c := range collisions {
			b.WriteString("\n  " + c.path + ": " + c.reason)
		}
		return fmt.Errorf("%w: %s", state.ErrConflict, b.String())
	}

	runPath := dstPath
	var tmpDir string
	if *dry {
		if tmpDir, err = os.MkdirTemp("", "hand-merge-"); err != nil {
			return err
		}
		defer os.RemoveAll(tmpDir)
		runPath = filepath.Join(tmpDir, "hand.db")
		if err := state.CopyDatabase(dstPath, runPath); err != nil {
			return err
		}
	}
	rep, err := state.Merge(r.ctx(), srcPath, runPath, state.MergeOptions{SourceHome: src.Home, DestHome: dst.Home, CancelTimers: *cancel, RenameProject: renames})
	if err != nil {
		return err
	}
	dbHash, err := state.DatabaseHash(runPath)
	if err != nil {
		return err
	}
	archiveState := "not requested"
	if *archive {
		archiveState = "would stop the server, remove the registry file and write merged-into"
	}
	if !*dry {
		if err := applyMergeFiles(src.Home, dst.Home, plan); err != nil {
			return fmt.Errorf("%w; the database is merged but the files are not: %v", state.ErrConflict, err)
		}
		if *archive {
			if err := r.archiveMergeSource(r.ctx(), root, src, dst); err != nil {
				return err
			}
			archiveState = "server stopped, registry file removed, merged-into written"
		}
	}
	var d toon.Doc
	d.Field("merged", dst.ID+" "+dst.Name)
	d.Field("source", src.ID+" "+src.Name)
	d.Field("destination_home", dst.Home)
	d.Field("source_home", src.Home)
	d.Bool("dry_run", *dry)
	offsets := make([][]string, 0, len(rep.Offsets))
	for _, k := range []string{"t", "a", "r", "d", "w", "i", "event"} {
		offsets = append(offsets, []string{k, strconv.FormatInt(rep.Offsets[k], 10)})
	}
	d.Rows("offsets", []string{"ref", "offset"}, offsets)
	counts := make([][]string, 0, len(rep.Counts))
	for _, c := range rep.Counts {
		counts = append(counts, []string{c.Table, strconv.FormatInt(c.Source, 10), strconv.FormatInt(c.Dest, 10), strconv.FormatInt(c.Merged, 10), strconv.FormatInt(c.Offset, 10)})
	}
	d.Rows("counts", []string{"table", "source", "destination", "merged", "offset"}, counts)
	refs := make([][]string, 0, len(rep.Refs))
	for _, c := range rep.Refs {
		refs = append(refs, []string{c.Kind, strconv.FormatInt(c.Changed, 10), strconv.FormatInt(c.Protected, 10)})
	}
	d.Rows("refs", []string{"kind", "changed", "protected"}, refs)
	cells := make([][]string, 0, len(rep.Cells))
	for _, c := range rep.Cells {
		cells = append(cells, []string{c.Table, c.Key, c.Column, c.Refs})
	}
	d.Rows("ambiguous", []string{"table", "key", "column", "refs"}, cells)
	d.Field("text_sha256", rep.TextSHA256)
	d.Field("db_sha256", dbHash)
	files := make([][]string, 0, len(mergeDirs))
	dirs := make([]string, 0, len(plan.counts))
	for dir := range plan.counts {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		c := plan.counts[dir]
		files = append(files, []string{dir, strconv.FormatInt(c.files, 10), strconv.FormatInt(c.bytes, 10), strconv.FormatInt(c.moved, 10), strconv.FormatInt(c.same, 10), strconv.FormatInt(c.saved, 10)})
	}
	d.Rows("files", []string{"dir", "files", "bytes", "moved", "identical", "saved"}, files)
	if len(plan.manual) > 0 {
		d.List("manual", plan.manual)
	}
	if len(rep.Warnings) > 0 {
		d.List("warnings", rep.Warnings)
	}
	d.Field("archive", archiveState)
	d.Help("The source home and its database stay untouched; remove its registry link only after checking the merge",
		"Workers are never stopped by this command: stop every launching or running attempt yourself before merging")
	return r.print(&d)
}

func mergeFleets(root string) ([]fleet.Entry, error) {
	des, err := os.ReadDir(filepath.Join(root, "fleets"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []fleet.Entry
	for _, de := range des {
		if !state.FleetID.MatchString(de.Name()) {
			continue
		}
		home, err := fleet.Home(root, de.Name())
		if err != nil {
			out = append(out, fleet.Entry{ID: de.Name(), State: "unreadable: " + err.Error()})
			continue
		}
		h, err := state.InspectFleet(filepath.Join(home, "hand.db"))
		switch {
		case err != nil:
			out = append(out, fleet.Entry{ID: de.Name(), Home: home, State: "unreadable: " + err.Error()})
		case h.Fleet.ID != de.Name():
			out = append(out, fleet.Entry{ID: de.Name(), Home: home, State: "moved"})
		default:
			out = append(out, fleet.Entry{ID: de.Name(), Name: h.Fleet.Name, Home: home, State: "ok"})
		}
	}
	return out, nil
}

func resolveMergeFleet(entries []fleet.Entry, root, arg string) (fleet.Entry, error) {
	if state.FleetID.MatchString(arg) {
		home, err := fleet.Home(root, arg)
		if err != nil {
			return fleet.Entry{}, err
		}
		for _, e := range entries {
			if e.ID == arg {
				if e.State != "ok" {
					return fleet.Entry{}, fmt.Errorf("%w: fleet %s is %s", state.ErrInvalid, arg, e.State)
				}
				e.Home = home
				return e, nil
			}
		}
		return fleet.Entry{}, fmt.Errorf("%w: fleet %s is not registered in %s", state.ErrNotFound, arg, root)
	}
	var found []fleet.Entry
	for _, e := range entries {
		if e.Name == arg {
			found = append(found, e)
		}
	}
	switch {
	case len(found) == 0:
		return fleet.Entry{}, fmt.Errorf("%w: no fleet named %s; run `hand fleet list`", state.ErrNotFound, arg)
	case len(found) > 1:
		return fleet.Entry{}, fmt.Errorf("%w: %d fleets are named %s; use the fleet id", state.ErrConflict, len(found), arg)
	case found[0].State != "ok":
		return fleet.Entry{}, fmt.Errorf("%w: fleet %s is %s", state.ErrInvalid, found[0].ID, found[0].State)
	}
	return found[0], nil
}

func (r *runner) mergeCheck(root string, src, dst fleet.Entry, stop, cancel bool) error {
	if _, _, _, live := update.LivePID(filepath.Join(root, "board.pid")); live {
		return fmt.Errorf("%w: the board is running; stop it with `hand board stop` before merging", state.ErrConflict)
	}
	sh, err := state.InspectFleet(filepath.Join(src.Home, "hand.db"))
	if err != nil {
		return err
	}
	dh, err := state.InspectFleet(filepath.Join(dst.Home, "hand.db"))
	if err != nil {
		return err
	}
	if dh.Supervisor != "" {
		return fmt.Errorf("%w: destination %s has live supervisor %s; stop it first", state.ErrConflict, dst.ID, dh.Supervisor)
	}
	if len(dh.LiveAttempts) > 0 {
		return fmt.Errorf("%w: destination %s has live attempt(s) %s; the operator must stop them", state.ErrConflict, dst.ID, refList(dh.LiveAttempts))
	}
	if dh.PendingInput > 0 {
		return fmt.Errorf("%w: destination %s has %d pending input(s)", state.ErrConflict, dst.ID, dh.PendingInput)
	}
	if held, err := watchHeldAt(dst.Home); err != nil {
		return err
	} else if held {
		return fmt.Errorf("%w: destination %s has a live watcher; stop it with `hand --home %s watch stop`", state.ErrConflict, dst.ID, dst.Home)
	}
	if len(sh.LiveAttempts) > 0 {
		return fmt.Errorf("%w: source %s has live attempt(s) %s; the operator must stop them", state.ErrConflict, src.ID, refList(sh.LiveAttempts))
	}
	if sh.PendingInput > 0 {
		return fmt.Errorf("%w: source %s has %d pending input(s); deliver or drop them first", state.ErrConflict, src.ID, sh.PendingInput)
	}
	held, err := watchHeldAt(src.Home)
	if err != nil {
		return err
	}
	if held {
		if !stop {
			return fmt.Errorf("%w: source %s has a live watcher; pass --stop or run `hand --home %s watch stop`", state.ErrConflict, src.ID, src.Home)
		}
		if _, _, err := stopPIDFile("watcher", filepath.Join(src.Home, "watch.pid")); err != nil {
			return err
		}
		if held, err := watchHeldAt(src.Home); err != nil {
			return err
		} else if held {
			return fmt.Errorf("%w: source %s still has a live watcher", state.ErrConflict, src.ID)
		}
	}
	if sh.Supervisor != "" {
		if !stop {
			return fmt.Errorf("%w: source %s has live supervisor %s; pass --stop or stop it first", state.ErrConflict, src.ID, sh.Supervisor)
		}
		if err := r.runFleetCommand(src.Home, "supervisor", "stop"); err != nil {
			return err
		}
		if sh, err = state.InspectFleet(filepath.Join(src.Home, "hand.db")); err != nil {
			return err
		}
		if sh.Supervisor != "" {
			return fmt.Errorf("%w: source %s still has live supervisor %s", state.ErrConflict, src.ID, sh.Supervisor)
		}
	}
	if len(sh.ArmedTimers) > 0 && !cancel {
		return fmt.Errorf("%w: source %s has armed timer(s) %s; pass --cancel-timers or cancel them first", state.ErrConflict, src.ID, timerList(sh.ArmedTimers))
	}
	return nil
}

func refList(ids []int64) string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = state.AttemptRef(id)
	}
	return strings.Join(out, ", ")
}

func timerList(ids []int64) string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = state.TimerRef(id)
	}
	return strings.Join(out, ", ")
}

func watchHeldAt(home string) (bool, error) {
	f, err := os.OpenFile(filepath.Join(home, "watch.lock"), os.O_RDWR, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer flock.Release(f)
	ok, err := flock.Lock(f, false)
	return !ok && err == nil, err
}

func (r *runner) runFleetCommand(home string, args ...string) error {
	var errOut bytes.Buffer
	env := r.env
	env.Stdout, env.Stderr, env.Board = io.Discard, &errOut, true
	if code := Run(append([]string{"--home", home}, args...), env); code != 0 {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = "exit " + strconv.Itoa(code)
		}
		return fmt.Errorf("%w: hand --home %s %s: %s", state.ErrConflict, home, strings.Join(args, " "), msg)
	}
	return nil
}

func planMergeFiles(src, dst, srcID string) (*mergeFilePlan, []mergeCollision, error) {
	plan := &mergeFilePlan{counts: map[string]*mergeDirCount{}}
	var collisions []mergeCollision
	for _, dir := range mergeDirs {
		root := filepath.Join(src, dir)
		info, err := os.Lstat(root)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if !info.IsDir() {
			return nil, []mergeCollision{{dir, "is not a folder in the source"}}, nil
		}
		count := &mergeDirCount{}
		plan.counts[dir] = count
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			targetRel := rel
			target := filepath.Join(dst, rel)
			info, err := d.Info()
			if err != nil {
				return err
			}
			count.files++
			if info.Mode().IsRegular() {
				count.bytes += info.Size()
			}
			targetInfo, err := os.Lstat(target)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				plan.moves = append(plan.moves, mergeFile{path, target})
				count.moved++
			case err != nil:
				return err
			case rel == filepath.Join("memory", "operator.md") && info.Mode().IsRegular() && targetInfo.Mode().IsRegular():
				same, err := sameContent(path, target)
				if err != nil {
					return err
				}
				if same {
					count.same++
					return nil
				}
				targetRel = filepath.Join("memory", "operator."+srcID+".md")
				target = filepath.Join(dst, targetRel)
				if _, err := os.Lstat(target); err == nil {
					same, err := sameContent(path, target)
					if err != nil {
						return err
					}
					if same {
						count.same++
						return nil
					}
					collisions = append(collisions, mergeCollision{rel, "and " + targetRel + " both exist with different content"})
					return nil
				} else if !errors.Is(err, fs.ErrNotExist) {
					return err
				}
				plan.moves = append(plan.moves, mergeFile{path, target})
				plan.manual = append(plan.manual, "memory/operator.md: kept the destination's; the source is saved as "+targetRel)
				count.moved++
				count.saved++
			default:
				same, err := sameContent(path, target)
				if err != nil {
					return err
				}
				if same {
					count.same++
					return nil
				}
				collisions = append(collisions, mergeCollision{rel, "different content"})
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	if _, err := os.Stat(filepath.Join(src, "routing.json")); err == nil {
		plan.manual = append(plan.manual, "routing.json: kept both; merge the profiles by hand")
	}
	if _, err := os.Stat(filepath.Join(src, "board.token")); err == nil {
		plan.manual = append(plan.manual, "board.token: kept the destination's; the source token was not copied")
	}
	return plan, collisions, nil
}

func sameContent(a, b string) (bool, error) {
	ai, err := os.Lstat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Lstat(b)
	if err != nil {
		return false, err
	}
	if ai.Mode()&fs.ModeSymlink != 0 || bi.Mode()&fs.ModeSymlink != 0 {
		if ai.Mode()&fs.ModeSymlink == 0 || bi.Mode()&fs.ModeSymlink == 0 {
			return false, nil
		}
		x, err := os.Readlink(a)
		if err != nil {
			return false, err
		}
		y, err := os.Readlink(b)
		return x == y, err
	}
	if !ai.Mode().IsRegular() || !bi.Mode().IsRegular() || ai.Size() != bi.Size() {
		return false, nil
	}
	x, err := fileHash(a)
	if err != nil {
		return false, err
	}
	y, err := fileHash(b)
	return x == y, err
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return string(h.Sum(nil)), nil
}

func applyMergeFiles(src, dst string, plan *mergeFilePlan) error {
	for _, m := range plan.moves {
		if _, err := os.Lstat(m.dst); err == nil {
			return fmt.Errorf("%s appeared in the destination while merging", m.dst)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := movePath(m.src, m.dst); err != nil {
			return err
		}
	}
	return nil
}

func movePath(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.Symlink(target, dst); err != nil {
			return err
		}
		return os.Remove(src)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return closeErr
	}
	return os.Remove(src)
}

func (r *runner) archiveMergeSource(ctx context.Context, root string, src, dst fleet.Entry) error {
	session := fleet.Session(src.ID)
	client := luvus.Client{Socket: luvus.SocketPath(r.env.Getenv, session)}
	if err := client.StopServer(ctx, luvus.SessionDir(r.env.Getenv, session), stopOne); err != nil {
		return fmt.Errorf("%w: archive: stop Luvus server: %v", state.ErrConflict, err)
	}
	link := filepath.Join(root, "fleets", src.ID)
	if b, err := os.ReadFile(link); err == nil && strings.TrimSpace(string(b)) == src.Home {
		if err := os.Remove(link); err != nil {
			return err
		}
	}
	marker := fmt.Sprintf("merged into %s %s\n%s\n", dst.ID, dst.Name, dst.Home)
	if err := os.WriteFile(filepath.Join(src.Home, "merged-into"), []byte(marker), 0o600); err != nil {
		return err
	}
	return nil
}
