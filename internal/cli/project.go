package cli

import (
	"context"
	"strconv"
	"strings"
	"unicode"

	"github.com/atqamz/hand/internal/gitcfg"
	"github.com/atqamz/hand/internal/toon"
)

var projectCommands = map[string]handler{
	"add":   cmdProjectAdd,
	"list":  cmdProjectList,
	"trust": cmdProjectTrust,
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
