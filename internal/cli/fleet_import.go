package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

var importDirs = []string{"memory", "deliverables", "plans", "inbox", "reports", "briefs"}

type importFilePair struct {
	Source, Destination, Reason string
}

type importFileReport struct {
	Copied, Same, Exported int
	Collisions             []importFilePair
	Failed                 []importFilePair
	Archive                string
}

func init() {
	fleetCommands["import"] = cmdFleetImport
}

func cmdFleetImport(r *runner, args []string) error {
	set := flags("fleet import SRC INTO DST")
	dry := set.Bool("dry-run", false, "print the manifest, write no rows, files or marker; SQLite may create hand.db-shm/-wal beside an opened database")
	rest := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--dry-run" {
			*dry = true
			continue
		}
		rest = append(rest, a)
	}
	pos, err := parse(set, rest, 3)
	if err != nil {
		return err
	}
	if pos[1] != "INTO" {
		return usageError{fmt.Sprintf("fleet import: want SRC INTO DST, got %q where INTO belongs", pos[1])}
	}
	root, err := fleet.Root(r.env.Getenv)
	if err != nil {
		return err
	}
	src, err := fleet.Resolve(root, pos[0])
	if err != nil {
		return err
	}
	dst, err := fleet.Resolve(root, pos[2])
	if err != nil {
		return err
	}
	ctx := r.ctx()
	if src.ID == dst.ID || sameHome(src.Home, dst.Home) {
		return fmt.Errorf("%w: source and destination are the same fleet %s", state.ErrConflict, src.ID)
	}
	if !*dry {
		lock, err := state.LockImport(dst.Home)
		if err != nil {
			return err
		}
		defer lock.Close()
	}
	marker := state.ImportMarker(src.Home)
	if _, err := os.Stat(marker); err == nil {
		return fmt.Errorf("%w: fleet %s was already imported (marker %s)", state.ErrConflict, src.ID, marker)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	recorded, err := state.ImportRecorded(ctx, dst.Home, src.ID)
	if err != nil {
		return err
	}
	if recorded {
		return fmt.Errorf("%w: fleet %s was already imported into %s (fleet.imported event)", state.ErrConflict, src.ID, dst.ID)
	}
	for _, f := range []struct {
		what, id, home string
	}{{"source", src.ID, src.Home}, {"destination", dst.ID, dst.Home}} {
		v, err := state.ImportSchema(f.home)
		if err != nil {
			return err
		}
		if v != state.SchemaVersion {
			return fmt.Errorf("%w: %s fleet %s has schema %d, not the current %d; open that fleet with this hand first", state.ErrInvalid, f.what, f.id, v, state.SchemaVersion)
		}
		h, err := state.ImportHealthAt(f.home)
		if err != nil {
			return err
		}
		if h.Supervisor != "" || len(h.Attempts) > 0 {
			live := []string{}
			if h.Supervisor != "" {
				live = append(live, h.Supervisor)
			}
			for _, id := range h.Attempts {
				live = append(live, state.AttemptRef(id))
			}
			return fmt.Errorf("%w: %s fleet %s is live (%s); stop it first", state.ErrConflict, f.what, f.id, strings.Join(live, ", "))
		}
	}
	res, err := state.ImportFleet(ctx, state.ImportOptions{SourceID: src.ID, SourceHome: src.Home, DestID: dst.ID, DestHome: dst.Home, DryRun: *dry})
	if err != nil {
		return err
	}
	if err := printImportDatabase(r, src, dst, *dry, res, marker); err != nil {
		return err
	}
	if *dry {
		files := importContent(src.Home, dst.Home, src.ID, res.Reports, true)
		if err := printImportFiles(r, files); err != nil {
			return err
		}
		if len(files.Failed) > 0 {
			return fmt.Errorf("%d file(s) would fail to copy; nothing was written", len(files.Failed))
		}
		return nil
	}
	writeErr := writeImportMarker(marker, dst.ID, res)
	files := importContent(src.Home, dst.Home, src.ID, res.Reports, false)
	if err := printImportFiles(r, files); err != nil {
		return err
	}
	if writeErr != nil {
		return fmt.Errorf("the database import committed, but the source marker failed: %w", writeErr)
	}
	if len(files.Failed) > 0 {
		return fmt.Errorf("the database import committed, but %d file(s) failed to copy", len(files.Failed))
	}
	return nil
}

func sameHome(a, b string) bool {
	x, err := os.Stat(a)
	if err != nil {
		return false
	}
	y, err := os.Stat(b)
	return err == nil && os.SameFile(x, y)
}

func writeImportMarker(path, destID string, res state.ImportResult) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := fmt.Fprintf(f, "%s projects %d tasks %d offset %d text %s\n", destID, addedProjects(res), taskCount(res), res.Offset, res.TextSHA256)
	cerr := f.Close()
	return errors.Join(werr, cerr)
}

func addedProjects(res state.ImportResult) int {
	n := 0
	for _, p := range res.Projects {
		if p.Action == "added" {
			n++
		}
	}
	return n
}

func taskCount(res state.ImportResult) int {
	n := 0
	for _, c := range res.Tasks {
		n += c
	}
	return n
}

func importContent(srcHome, dstHome, srcID string, reports []state.ImportReportRow, dry bool) importFileReport {
	var out importFileReport
	for _, dir := range importDirs {
		root := filepath.Join(srcHome, dir)
		if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			out.Failed = append(out.Failed, importFilePair{Source: dir, Reason: err.Error()})
			continue
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				rel, _ := filepath.Rel(srcHome, path)
				out.Failed = append(out.Failed, importFilePair{Source: rel, Reason: err.Error()})
				return nil
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(srcHome, path)
			if err != nil {
				out.Failed = append(out.Failed, importFilePair{Source: path, Reason: err.Error()})
				return nil
			}
			to := rel
			if filepath.ToSlash(rel) == "memory/operator.md" {
				to = filepath.Join("memory", "operator."+srcID+".md")
			}
			if !d.Type().IsRegular() {
				out.Failed = append(out.Failed, importFilePair{Source: rel, Destination: to, Reason: "not a regular file"})
				return nil
			}
			action, err := copyImportFile(path, filepath.Join(dstHome, to), dry)
			switch {
			case err != nil:
				out.Failed = append(out.Failed, importFilePair{Source: rel, Destination: to, Reason: err.Error()})
			case action == importSame:
				out.Same++
			case action == importCollision:
				out.Collisions = append(out.Collisions, importFilePair{Source: rel, Destination: to, Reason: "different content"})
			default:
				out.Copied++
			}
			return nil
		})
		if err != nil {
			out.Failed = append(out.Failed, importFilePair{Source: dir, Reason: err.Error()})
		}
	}
	out.Archive = filepath.Join("reports", "archive-"+srcID)
	for _, rep := range reports {
		name := fmt.Sprintf("r%d-%s.md", rep.ID, importSlug(rep.TaskTitle))
		to := filepath.Join(out.Archive, name)
		body := importReportBody(srcID, rep)
		action, err := copyImportBytes([]byte(body), filepath.Join(dstHome, to), dry)
		switch {
		case err != nil:
			out.Failed = append(out.Failed, importFilePair{Source: state.ReportRef(rep.ID), Destination: to, Reason: err.Error()})
		case action == importSame:
			out.Same++
		case action == importCollision:
			out.Collisions = append(out.Collisions, importFilePair{Source: state.ReportRef(rep.ID), Destination: to, Reason: "different content"})
		default:
			out.Exported++
		}
	}
	return out
}

const (
	importCopied = iota
	importSame
	importCollision
)

func copyImportFile(src, dst string, dry bool) (int, error) {
	si, err := os.Stat(src)
	if err != nil {
		return 0, err
	}
	di, err := os.Stat(dst)
	switch {
	case err == nil && di.Size() == si.Size() && sameImportFile(src, dst):
		return importSame, nil
	case err == nil:
		return importCollision, nil
	case !errors.Is(err, fs.ErrNotExist):
		return 0, err
	}
	if dry {
		return importCopied, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return 0, err
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, si.Mode().Perm())
	if err != nil {
		return 0, err
	}
	_, cerr := io.Copy(out, in)
	if err := out.Close(); err != nil {
		cerr = errors.Join(cerr, err)
	}
	if cerr != nil {
		_ = os.Remove(dst)
	}
	return importCopied, cerr
}

func sameImportFile(a, b string) bool {
	x, err := os.Open(a)
	if err != nil {
		return false
	}
	defer x.Close()
	y, err := os.Open(b)
	if err != nil {
		return false
	}
	defer y.Close()
	xb := make([]byte, 32*1024)
	yb := make([]byte, 32*1024)
	for {
		xn, xerr := io.ReadFull(x, xb)
		yn, yerr := io.ReadFull(y, yb)
		if xn != yn || !bytes.Equal(xb[:xn], yb[:yn]) {
			return false
		}
		if xerr == io.EOF || xerr == io.ErrUnexpectedEOF || yerr == io.EOF || yerr == io.ErrUnexpectedEOF {
			return (xerr == io.EOF || xerr == io.ErrUnexpectedEOF) && (yerr == io.EOF || yerr == io.ErrUnexpectedEOF)
		}
		if xerr != nil || yerr != nil {
			return false
		}
	}
}

func copyImportBytes(b []byte, dst string, dry bool) (int, error) {
	cur, err := os.ReadFile(dst)
	switch {
	case err == nil && bytes.Equal(cur, b):
		return importSame, nil
	case err == nil:
		return importCollision, nil
	case !errors.Is(err, fs.ErrNotExist):
		return 0, err
	}
	if dry {
		return importCopied, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	_, werr := f.Write(b)
	cerr := f.Close()
	return importCopied, errors.Join(werr, cerr)
}

func importReportBody(srcID string, rep state.ImportReportRow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "source: %s\n", srcID)
	fmt.Fprintf(&b, "task: %s\n", state.TaskRef(rep.NewTaskID))
	fmt.Fprintf(&b, "source_task: %s\n", state.TaskRef(rep.TaskID))
	fmt.Fprintf(&b, "source_attempt: %s (not imported)\n", state.AttemptRef(rep.AttemptID))
	fmt.Fprintf(&b, "created: %s\n", rep.CreatedAt)
	fmt.Fprintf(&b, "status: %s\n\n", rep.Status)
	b.WriteString(rep.Body)
	if !strings.HasSuffix(rep.Body, "\n") {
		b.WriteByte('\n')
	}
	return b.String()
}

func importSlug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 60 {
		out = strings.Trim(out[:60], "-")
	}
	if out == "" {
		return "report"
	}
	return out
}

func printImportDatabase(r *runner, src, dst fleet.Entry, dry bool, res state.ImportResult, marker string) error {
	var d toon.Doc
	d.Field("manifest", "database")
	d.Field("source", src.ID+" "+src.Name)
	d.Field("destination", dst.ID+" "+dst.Name)
	d.Bool("dry_run", dry)
	d.Field("task_offset", strconv.FormatInt(res.Offset, 10))
	taskRange := ""
	if taskCount(res) > 0 {
		taskRange = state.TaskRef(res.NewTaskMin) + ".." + state.TaskRef(res.NewTaskMax)
	}
	d.Field("task_range", taskRange)
	d.Rows("tables", []string{"table", "rows", "offset"}, [][]string{
		{"project", strconv.Itoa(addedProjects(res)), "0"},
		{"task", strconv.Itoa(taskCount(res)), strconv.FormatInt(res.Offset, 10)},
	})
	projects := make([][]string, 0, len(res.Projects))
	for _, p := range res.Projects {
		projects = append(projects, []string{p.Name, p.Repo, p.Source, p.Action})
	}
	d.Rows("projects", []string{"name", "repo", "as", "action"}, projects)
	statuses := make([]string, 0, len(res.Tasks))
	for st := range res.Tasks {
		statuses = append(statuses, st)
	}
	slices.Sort(statuses)
	tasks := make([][]string, 0, len(statuses))
	for _, st := range statuses {
		tasks = append(tasks, []string{st, strconv.Itoa(res.Tasks[st])})
	}
	d.Rows("tasks", []string{"status", "rows"}, tasks)
	refs := make([][]string, 0, len(res.Refs))
	for _, ref := range res.Refs {
		refs = append(refs, []string{ref.Table, ref.Key, ref.Column, ref.From, ref.To})
	}
	d.Rows("refs", []string{"table", "key", "column", "from", "to"}, refs)
	left := make([][]string, 0, len(res.Left))
	for _, ref := range res.Left {
		left = append(left, []string{ref.Table, ref.Key, ref.Column, ref.From})
	}
	d.Rows("left", []string{"table", "key", "column", "ref"}, left)
	ambiguous := make([][]string, 0, len(res.Ambiguous))
	for _, a := range res.Ambiguous {
		ambiguous = append(ambiguous, []string{a.Table, a.Key, a.Column, a.Token, a.Context})
	}
	d.Rows("ambiguous", []string{"table", "key", "column", "token", "context"}, ambiguous)
	d.Field("text_sha256", res.TextSHA256)
	if dry {
		d.Field("marker", "would write "+marker)
		d.Help("Dry run: no row, file or marker was written; the file part follows")
	} else {
		d.Field("marker", marker)
		d.Field("event_seq", strconv.FormatInt(res.EventSeq, 10))
		d.Help("The database import committed; the file part follows")
	}
	return r.print(&d)
}

func printImportFiles(r *runner, files importFileReport) error {
	var d toon.Doc
	d.Field("manifest", "files")
	d.Field("archive", files.Archive)
	d.Field("reports_exported", strconv.Itoa(files.Exported))
	d.Rows("files", []string{"kind", "count"}, [][]string{
		{"copied", strconv.Itoa(files.Copied)},
		{"same", strconv.Itoa(files.Same)},
		{"collision", strconv.Itoa(len(files.Collisions))},
		{"failed", strconv.Itoa(len(files.Failed))},
	})
	collisions := make([][]string, 0, len(files.Collisions))
	for _, c := range files.Collisions {
		collisions = append(collisions, []string{c.Source, c.Destination, c.Reason})
	}
	d.Rows("collisions", []string{"source", "destination", "reason"}, collisions)
	failed := make([][]string, 0, len(files.Failed))
	for _, f := range files.Failed {
		failed = append(failed, []string{f.Source, f.Destination, f.Reason})
	}
	d.Rows("failed", []string{"source", "destination", "reason"}, failed)
	d.Help("Copied files keep their source names, and copied briefs and reports keep source task ids; the archive headers name the source task and source attempt, which are not imported")
	return r.print(&d)
}
