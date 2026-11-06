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
	from, to string
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
	dry := set.Bool("dry-run", false, "compute the merge on a copy and print its manifest; change no row or file, though SQLite may touch a hand.db-shm; refuses --stop")
	stop := set.Bool("stop", false, "stop the source's watcher and supervisor after every refusal is checked; workers are never stopped")
	archive := set.Bool("archive-source", false, "after a real merge, stop the source's Luvus server, write merged-into and then remove its registry file")
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
	if *dry && *stop {
		return fmt.Errorf("%w: --dry-run cannot stop a service; drop --stop, or stop the source's watcher and supervisor yourself", state.ErrInvalid)
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
	plan, collisions, err := planMergeFiles(src.Home, dst.Home, src.ID, renames)
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
	if err := r.mergePreflight(root, src, dst, *stop, *cancel); err != nil {
		return err
	}
	opts := state.MergeOptions{SourceHome: src.Home, DestHome: dst.Home, CancelTimers: *cancel, RenameProject: renames}
	if *stop && !*dry {
		preflight := opts
		preflight.Preflight = true
		if err := preflightMerge(r.ctx(), srcPath, dstPath, preflight); err != nil {
			return err
		}
		if err := r.stopMergeSource(src); err != nil {
			return err
		}
	}
	runPath := dstPath
	if *dry {
		var cleanup func()
		runPath, cleanup, err = mergeSnapshot(dstPath)
		if err != nil {
			return err
		}
		defer cleanup()
	}
	if err := quietMergeWatchers(src.Home, dst.Home); err != nil {
		return err
	}
	rep, err := state.Merge(r.ctx(), srcPath, runPath, opts)
	if err != nil {
		return err
	}
	dbHash, hashErr := state.DatabaseHash(runPath)
	archiveState := "not requested"
	if *archive {
		archiveState = "pending"
		if *dry {
			archiveState = "would stop the server, write and verify merged-into, then remove the registry file"
		}
	}
	m := &mergeManifest{rep: rep, dbHash: dbHash, dry: *dry, src: src.Home, dst: dst.Home, plan: plan, archive: archiveState}
	if *dry {
		m.postErr = hashErr
		err = printMergeManifest(r, m)
		if err != nil {
			return err
		}
		return hashErr
	}
	if hashErr != nil {
		m.postErr = hashErr
		m.archive = "not run: database hash failed"
		if err := printMergeManifest(r, m); err != nil {
			return err
		}
		return hashErr
	}
	if err := printMergeManifest(r, m); err != nil {
		return err
	}
	moved, err := applyMergeFiles(src.Home, dst.Home, plan)
	m.moved = moved
	if err != nil {
		m.postErr = err
		m.archive = "not run: file move failed"
		_ = printMergeManifest(r, m)
		return fmt.Errorf("%w; the database is merged but the files are not: %v", state.ErrConflict, err)
	}
	if *archive {
		m.archive, err = r.archiveMergeSource(r.ctx(), root, src, dst)
		if err != nil {
			m.postErr = err
			_ = printMergeManifest(r, m)
			return err
		}
	}
	return printMergeManifest(r, m)
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

func (r *runner) mergePreflight(root string, src, dst fleet.Entry, stop, cancel bool) error {
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
	if sh.Supervisor != "" && !stop {
		return fmt.Errorf("%w: source %s has live supervisor %s; pass --stop or stop it first", state.ErrConflict, src.ID, sh.Supervisor)
	}
	if held, err := watchHeldAt(src.Home); err != nil {
		return err
	} else if held && !stop {
		return fmt.Errorf("%w: source %s has a live watcher; pass --stop or run `hand --home %s watch stop`", state.ErrConflict, src.ID, src.Home)
	}
	if len(sh.ArmedTimers) > 0 && !cancel {
		return fmt.Errorf("%w: source %s has armed timer(s) %s; pass --cancel-timers or cancel them first", state.ErrConflict, src.ID, timerList(sh.ArmedTimers))
	}
	return nil
}

func (r *runner) stopMergeSource(src fleet.Entry) error {
	if held, err := watchHeldAt(src.Home); err != nil {
		return err
	} else if held {
		if _, _, err := stopPIDFile("watcher", filepath.Join(src.Home, "watch.pid")); err != nil {
			return err
		}
		if held, err := watchHeldAt(src.Home); err != nil {
			return err
		} else if held {
			return fmt.Errorf("%w: source %s still has a live watcher", state.ErrConflict, src.ID)
		}
	}
	sh, err := state.InspectFleet(filepath.Join(src.Home, "hand.db"))
	if err != nil {
		return err
	}
	if sh.Supervisor != "" {
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
	return nil
}

func quietMergeWatchers(homes ...string) error {
	for _, home := range homes {
		held, err := watchHeldAt(home)
		if err != nil {
			return err
		}
		if held {
			return fmt.Errorf("%w: %s has a live watcher; stop it before merging", state.ErrConflict, home)
		}
	}
	return nil
}

func mergeSnapshot(dstPath string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "hand-merge-")
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, "hand.db")
	if err := state.CopyDatabase(dstPath, path); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	return path, func() { _ = os.RemoveAll(dir) }, nil
}

func preflightMerge(ctx context.Context, srcPath, dstPath string, o state.MergeOptions) error {
	snapshot, cleanup, err := mergeSnapshot(dstPath)
	if err != nil {
		return err
	}
	defer cleanup()
	_, err = state.Merge(ctx, srcPath, snapshot, o)
	return err
}

type mergeManifest struct {
	rep      state.MergeReport
	dbHash   string
	dry      bool
	src, dst string
	plan     *mergeFilePlan
	moved    []mergeFile
	archive  string
	postErr  error
}

func printMergeManifest(r *runner, m *mergeManifest) error {
	var d toon.Doc
	d.Field("merged", m.rep.Dest.ID+" "+m.rep.Dest.Name)
	d.Field("source", m.rep.Source.ID+" "+m.rep.Source.Name)
	d.Field("destination_home", m.dst)
	d.Field("source_home", m.src)
	d.Bool("dry_run", m.dry)
	offsets := make([][]string, 0, len(m.rep.Offsets))
	for _, k := range []string{"t", "a", "r", "d", "w", "i", "event"} {
		offsets = append(offsets, []string{k, strconv.FormatInt(m.rep.Offsets[k], 10)})
	}
	d.Rows("offsets", []string{"ref", "offset"}, offsets)
	counts := make([][]string, 0, len(m.rep.Counts))
	for _, c := range m.rep.Counts {
		counts = append(counts, []string{c.Table, strconv.FormatInt(c.Source, 10), strconv.FormatInt(c.Dest, 10), strconv.FormatInt(c.Merged, 10), strconv.FormatInt(c.Offset, 10)})
	}
	d.Rows("counts", []string{"table", "source", "destination", "merged", "offset"}, counts)
	refs := make([][]string, 0, len(m.rep.Refs))
	for _, c := range m.rep.Refs {
		refs = append(refs, []string{c.Kind, strconv.FormatInt(c.Changed, 10), strconv.FormatInt(c.Protected, 10)})
	}
	d.Rows("refs", []string{"kind", "changed", "protected"}, refs)
	cells := make([][]string, 0, len(m.rep.Cells))
	for _, c := range m.rep.Cells {
		cells = append(cells, []string{c.Table, c.Key, c.Column, c.Refs})
	}
	d.Rows("ambiguous", []string{"table", "key", "column", "refs"}, cells)
	d.Field("text_sha256", m.rep.TextSHA256)
	d.Field("db_sha256", m.dbHash)
	files := make([][]string, 0, len(mergeDirs))
	dirs := make([]string, 0, len(m.plan.counts))
	for dir := range m.plan.counts {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		c := m.plan.counts[dir]
		files = append(files, []string{dir, strconv.FormatInt(c.files, 10), strconv.FormatInt(c.bytes, 10), strconv.FormatInt(c.moved, 10), strconv.FormatInt(c.same, 10), strconv.FormatInt(c.saved, 10)})
	}
	d.Rows("files", []string{"dir", "files", "bytes", "moved", "identical", "saved"}, files)
	if len(m.plan.manual) > 0 {
		d.List("manual", m.plan.manual)
	}
	if len(m.moved) > 0 {
		moves := make([][]string, 0, len(m.moved))
		for _, mv := range m.moved {
			moves = append(moves, []string{filepath.ToSlash(mv.from), filepath.ToSlash(mv.to)})
		}
		d.Rows("moves", []string{"from", "to"}, moves)
	}
	if len(m.rep.Warnings) > 0 {
		d.List("warnings", m.rep.Warnings)
	}
	d.Field("archive", m.archive)
	if m.postErr != nil {
		d.Field("post_commit_error", m.postErr.Error())
	}
	help := []string{"The source database and folder stay; the files a real run moves are no longer in it. Remove its registry link only after checking the merge",
		"Workers are never stopped by this command: stop every launching or running attempt yourself before merging",
		"After a merge, rename or move the survivor with hand fleet move PATH; run the merge first, then the move",
		"db_sha256 is a sorted logical dump, so a dry run and a real run over the same inputs print the same value"}
	if m.dry {
		help = append([]string{"A dry run changes no row and moves no file, though SQLite may touch a hand.db-shm"}, help...)
	} else {
		help = append([]string{"The database is committed when this manifest first appears; later fields report the file move"}, help...)
	}
	d.Help(help...)
	return r.print(&d)
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

func planMergeFiles(src, dst, srcID string, renames map[string]string) (*mergeFilePlan, []mergeCollision, error) {
	plan := &mergeFilePlan{counts: map[string]*mergeDirCount{}}
	var collisions []mergeCollision
	targets := map[string]string{}
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
			if name, ok := memoryProjectName(rel); ok {
				if renamed, ok := renames[name]; ok {
					targetRel = filepath.Join("memory", "projects", renamed+".md")
				}
			}
			target := filepath.Join(dst, targetRel)
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
				if prev, ok := targets[target]; ok {
					collisions = append(collisions, mergeCollision{rel, "and " + prev + " both map to " + targetRel})
					return nil
				}
				targets[target] = rel
				plan.moves = append(plan.moves, mergeFile{src: path, dst: target, from: rel, to: targetRel})
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
				if prev, ok := targets[target]; ok {
					collisions = append(collisions, mergeCollision{rel, "and " + prev + " both map to " + targetRel})
					return nil
				}
				targets[target] = rel
				plan.moves = append(plan.moves, mergeFile{src: path, dst: target, from: rel, to: targetRel})
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

func memoryProjectName(rel string) (string, bool) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 3 || parts[0] != "memory" || parts[1] != "projects" || !strings.HasSuffix(parts[2], ".md") {
		return "", false
	}
	name := strings.TrimSuffix(parts[2], ".md")
	return name, name != ""
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

func applyMergeFiles(src, dst string, plan *mergeFilePlan) ([]mergeFile, error) {
	var moved []mergeFile
	for _, m := range plan.moves {
		if _, err := os.Lstat(m.dst); err == nil {
			return moved, fmt.Errorf("%s appeared in the destination while merging", m.dst)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return moved, err
		}
		if err := movePath(m.src, m.dst); err != nil {
			return moved, err
		}
		moved = append(moved, m)
	}
	return moved, nil
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

func (r *runner) archiveMergeSource(ctx context.Context, root string, src, dst fleet.Entry) (string, error) {
	session := fleet.Session(src.ID)
	client := luvus.Client{Socket: luvus.SocketPath(r.env.Getenv, session)}
	if err := client.StopServer(ctx, luvus.SessionDir(r.env.Getenv, session), stopOne); err != nil {
		return "", fmt.Errorf("%w: archive: stop Luvus server: %v", state.ErrConflict, err)
	}
	marker := fmt.Sprintf("merged into %s %s\n%s\n", dst.ID, dst.Name, dst.Home)
	markerPath := filepath.Join(src.Home, "merged-into")
	if err := os.WriteFile(markerPath, []byte(marker), 0o600); err != nil {
		return "server stopped, merged-into not written, registry file kept", err
	}
	if b, err := os.ReadFile(markerPath); err != nil {
		return "server stopped, merged-into not verified, registry file kept", err
	} else if string(b) != marker {
		return "server stopped, merged-into not verified, registry file kept", fmt.Errorf("%w: merged-into does not hold what was written", state.ErrInvalid)
	}
	link := filepath.Join(root, "fleets", src.ID)
	recorded, err := os.ReadFile(link)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "server stopped, merged-into written and verified, registry file already absent", nil
	case err != nil:
		return "server stopped, merged-into written and verified, registry file not read", err
	case strings.TrimSpace(string(recorded)) != src.Home:
		return "server stopped, merged-into written and verified, registry file left in place: it names another home", nil
	}
	if err := removeIfSame(link, recorded); err != nil {
		return "server stopped, merged-into written and verified, registry file not removed", err
	}
	if _, err := os.Stat(link); err == nil {
		return "server stopped, merged-into written and verified, registry file left in place: it changed while merging", nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "server stopped, merged-into written and verified, registry file not verified as removed", err
	}
	return "server stopped, merged-into written and verified, registry file removed", nil
}
