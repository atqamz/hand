package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/atqamz/hand/internal/gitcfg"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

var projectCommands = map[string]handler{
	"add":    cmdProjectAdd,
	"list":   cmdProjectList,
	"trust":  cmdProjectTrust,
	"grants": cmdProjectGrants,
	"note":   cmdProjectNote,
	"notes":  cmdProjectNotes,
}

func init() {
	commands["project"] = func(r *runner, args []string) error { return sub(r, args, "project", projectCommands) }
}

func cmdProjectAdd(r *runner, args []string) error {
	pos, err := parse(flags("project add"), args, 2)
	if err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := r.ctx()
	settings := ""
	switch now, err := gitcfg.Read(ctx, pos[1]); {
	case err == nil:
		settings = now.Digest()
	case gitcfg.Initialised(pos[1]):
		return err
	}
	p, err := st.AddProject(ctx, pos[0], pos[1], settings)
	if err != nil {
		return err
	}
	var d toon.Doc
	d.Field("project", p.Name)
	d.Field("repo", p.Repo)
	d.Help("Capture work: `hand task add --goal TEXT " + p.Name + " TITLE`")
	return r.print(&d)
}

func cmdProjectList(r *runner, args []string) error {
	if _, err := parse(flags("project list"), args, 0); err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	ps, err := st.Projects(context.Background())
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(ps))
	for _, p := range ps {
		rows = append(rows, []string{p.Name, p.Repo})
	}
	var d toon.Doc
	d.Rows("projects", []string{"name", "repo"}, rows)
	return r.print(&d)
}

func cmdProjectTrust(r *runner, args []string) error {
	pos, err := parse(flags("project trust"), args, 1)
	if err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := r.ctx()
	p, err := st.Project(ctx, pos[0])
	if err != nil {
		return err
	}
	now, err := gitcfg.Read(ctx, p.Repo)
	if err != nil {
		return err
	}
	status := "unchanged"
	if now.Digest() != p.Settings {
		if err := st.TrustProject(ctx, p.Name, now.Digest()); err != nil {
			return err
		}
		status = "accepted"
	}
	var rows [][]string
	for _, setting := range now {
		for _, v := range setting.Values {
			rows = append(rows, []string{printable(setting.Name), printable(v)})
		}
	}
	var d toon.Doc
	d.Field("project", p.Name)
	d.Field("status", status)
	d.Rows("settings", []string{"setting", "value"}, rows)
	return r.print(&d)
}

func cmdProjectGrants(r *runner, args []string) error {
	fs := flags("project grants")
	remove := fs.Int64("remove", 0, "id of the grant to remove")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := r.ctx()
	p, err := st.Project(ctx, pos[0])
	if err != nil {
		return err
	}
	var d toon.Doc
	d.Field("project", p.Name)
	if *remove != 0 {
		g, err := st.RemoveGrant(ctx, p.Name, *remove)
		if err != nil {
			return err
		}
		d.Field("removed", strconv.FormatInt(g.ID, 10)+" "+g.Harness+" "+g.Pattern)
	}
	grants, err := st.Grants(ctx, p.Name, "")
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(grants))
	for _, g := range grants {
		rows = append(rows, []string{strconv.FormatInt(g.ID, 10), g.Harness, printable(g.Pattern), g.CreatedAt})
	}
	d.Rows("grants", []string{"id", "harness", "pattern", "created_at"}, rows)
	if len(grants) > 0 {
		d.Help("Remove one: `hand project grants --remove ID " + p.Name + "`")
	}
	return r.print(&d)
}

func cmdProjectNote(r *runner, args []string) error {
	pos, err := parse(flags("project note"), args, 2)
	if err != nil {
		return err
	}
	text := pos[1]
	if strings.TrimSpace(text) == "" {
		return usageError{"project note: the note is empty"}
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := r.ctx()
	p, err := projectByName(ctx, st, pos[0])
	if err != nil {
		return err
	}
	now := r.env.Now()
	file, err := appendProjectNote(r.home, p.Name, now, text)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(r.home, file)
	if err != nil {
		rel = filepath.Join("notes", p.Name+".md")
	}
	var d toon.Doc
	d.Field("project", p.Name)
	d.Field("date", now.Format("2006-01-02"))
	d.Field("file", filepath.ToSlash(rel))
	return r.print(&d)
}

func cmdProjectNotes(r *runner, args []string) error {
	pos, err := parse(flags("project notes"), args, 1)
	if err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	p, err := projectByName(r.ctx(), st, pos[0])
	if err != nil {
		return err
	}
	b, err := os.ReadFile(projectNotePath(r.home, p.Name))
	if errors.Is(err, fs.ErrNotExist) {
		_, err = fmt.Fprint(r.env.Stdout, "no notes\n")
		return err
	}
	if err != nil {
		return err
	}
	_, err = r.env.Stdout.Write(b)
	return err
}

func projectByName(ctx context.Context, st *state.Store, name string) (state.Project, error) {
	p, err := st.Project(ctx, name)
	if !errors.Is(err, state.ErrNotFound) {
		return p, err
	}
	ps, err := st.Projects(ctx)
	if err != nil {
		return state.Project{}, err
	}
	names := make([]string, 0, len(ps))
	for _, known := range ps {
		names = append(names, known.Name)
	}
	if len(names) == 0 {
		return state.Project{}, fmt.Errorf("%w: project %s; no projects are registered", state.ErrNotFound, name)
	}
	return state.Project{}, fmt.Errorf("%w: project %s; known projects: %s", state.ErrNotFound, name, strings.Join(names, ", "))
}

func projectNotePath(home, project string) string {
	return filepath.Join(home, "notes", project+".md")
}

func appendProjectNote(home, project string, now time.Time, text string) (string, error) {
	dir := filepath.Join(home, "notes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	file := projectNotePath(home, project)
	f, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return "", err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return "", err
	}
	var b strings.Builder
	if info.Size() == 0 {
		b.WriteString("# " + project + " — notes\n")
	}
	b.WriteString("\n## " + now.Format("2006-01-02") + "\n\n")
	b.WriteString(strings.TrimRight(text, "\r\n"))
	b.WriteString("\n\n")
	if _, err := f.WriteString(b.String()); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return file, nil
}

func printable(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
			continue
		}
		q := strconv.QuoteToASCII(string(r))
		b.WriteString(q[1 : len(q)-1])
	}
	return b.String()
}
