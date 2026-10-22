package board_test

import (
	"context"
	"encoding/json"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

const sep = string(filepath.Separator)

var pathFiles = map[string]string{
	"reports/t1/design.md":       design,
	"reports/t1/résumé notes.md": "# notes\n",
	"shots/t2/a.png":             string(flowPNG),
	"hand.db":                    secret,
	".secret/key":                secret,
	"projects/app/README.md":     secret,
	"luvus/server.pid":           secret,
}

func pathLink(href, text string) string {
	return `<a class="path" href="` + href + `" target="_blank" rel="noopener">` + text + `</a>`
}

func jsonText(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b[1 : len(b)-1])
}

func pathPages(t *testing.T, home, text string) map[string]string {
	t.Helper()
	fx := newFixture(t)
	fx.options.Home = home
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("where is it?"), reply(jsonText(text))})
	ctx := context.Background()
	task := active(t, fx.st, "Design")
	if _, err := fx.st.SetPlan(ctx, task.ID, text); err != nil {
		t.Fatal(err)
	}
	a := attempt(t, fx.st, task.ID, state.AttemptRunning, "")
	if _, err := fx.st.AddReport(ctx, a.ID, state.ReportDone, "Design written\n\n"+text); err != nil {
		t.Fatal(err)
	}
	d, err := fx.st.Ask(ctx, task.ID, "Approve the design?\n\n"+text)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "reports", "t1", "index.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	h := fx.handler()
	fleet := get(t, h, "/")
	pages := map[string]string{
		"chat":         region(fleet, "timeline"),
		"needs":        region(fleet, "queue"),
		"conversation": get(t, h, "/supervisor/log"),
		"decision":     get(t, h, "/decision/d1"),
		"task":         get(t, h, "/task/t1"),
		"document":     get(t, h, "/doc/reports/t1/index.md"),
	}
	base := serve(t, quick(fx.st, fx.options))
	ch, _ := stream(t, base+"/events")
	for _, e := range first(t, ch, len(regionNames)) {
		if e.name == "queue" || e.name == "timeline" {
			pages["live "+e.name] = e.data
		}
	}
	if _, err := fx.st.Answer(ctx, d.ID, "1. Approve", "operator"); err != nil {
		t.Fatal(err)
	}
	pages["answered decision"] = get(t, h, "/decision/d1")
	return pages
}

var copies = map[string]int{"chat": 1, "needs": 2, "conversation": 1, "decision": 1, "task": 2, "document": 1, "live queue": 2, "live timeline": 1, "answered decision": 1}

func linksEverywhere(t *testing.T, pages map[string]string, want string) {
	t.Helper()
	if len(pages) != len(copies) {
		t.Fatalf("rendered %d pages, want %d", len(pages), len(copies))
	}
	for page, body := range pages {
		if n := strings.Count(body, want); n != copies[page] {
			t.Errorf("%s holds %d of %s, want %d:\n%s", page, n, want, copies[page], body)
		}
	}
}

func staysText(t *testing.T, home string, paths ...string) {
	t.Helper()
	text := "- " + strings.Join(paths, "\n- ")
	for page, body := range pathPages(t, home, text) {
		lacks(t, page, body, `class="path"`)
		for _, p := range paths {
			if !strings.Contains(body, html.EscapeString(p)) {
				t.Errorf("%s lost the text %s:\n%s", page, p, body)
			}
		}
	}
}

func TestAPathInsideTheFleetBecomesALink(t *testing.T) {
	home := fleetFolder(t, pathFiles)
	t.Setenv("HOME", filepath.Dir(home))
	t.Setenv("USERPROFILE", filepath.Dir(home))
	file := filepath.Join(home, "reports", "t1", "design.md")
	folder := filepath.Join(home, "shots", "t2") + sep
	tilde := "~/" + filepath.Base(home) + "/reports/t1/design.md"
	pages := pathPages(t, home, "The design is "+file+", the shots are in "+folder+". From home: "+tilde+".")
	linksEverywhere(t, pages, pathLink("/doc/reports/t1/design.md", html.EscapeString(file)))
	linksEverywhere(t, pages, pathLink("/doc/shots/t2", html.EscapeString(folder)))
	linksEverywhere(t, pages, pathLink("/doc/reports/t1/design.md", html.EscapeString(tilde)))
}

func TestAPathOutsideTheFleetStaysText(t *testing.T) {
	home := fleetFolder(t, pathFiles)
	outside := fleetFolder(t, map[string]string{"notes.md": secret})
	t.Setenv("HOME", filepath.Dir(home))
	t.Setenv("USERPROFILE", filepath.Dir(home))
	paths := []string{
		filepath.Join(outside, "notes.md"),
		"~/" + filepath.Base(outside) + "/notes.md",
		"~/" + filepath.Base(home) + "/reports/../reports/t1/design.md",
		home + sep + "reports" + sep + ".." + sep + ".." + sep + filepath.Base(outside) + sep + "notes.md",
		home + sep + "reports" + sep + "t1" + sep + ".." + sep + "t1" + sep + "design.md",
		"/home/x/../../etc/passwd",
	}
	if runtime.GOOS != "windows" {
		paths = append(paths, `C:\fleet\reports\t1\design.md`)
	}
	staysText(t, home, paths...)
}

func TestAMissingPathStaysText(t *testing.T) {
	home := fleetFolder(t, pathFiles)
	staysText(t, home, filepath.Join(home, "reports", "t1", "gone.md"), filepath.Join(home, "reports", "t9")+sep)
}

func TestARefusedPathStaysText(t *testing.T) {
	home := fleetFolder(t, pathFiles)
	staysText(t, home, filepath.Join(home, "hand.db"), filepath.Join(home, ".secret", "key"), filepath.Join(home, "projects", "app", "README.md"), filepath.Join(home, "luvus", "server.pid"))
}

func TestACodeSpanThatIsAPathLinks(t *testing.T) {
	home := fleetFolder(t, pathFiles)
	spaced := filepath.Join(home, "reports", "t1", "résumé notes.md")
	file := filepath.Join(home, "reports", "t1", "design.md")
	pages := pathPages(t, home, "Notes: `"+spaced+"`. Not a path: `"+file+" and more`, nor "+file+":12.")
	linksEverywhere(t, pages, pathLink("/doc/reports/t1/r%C3%A9sum%C3%A9%20notes.md", "<code>"+html.EscapeString(spaced)+"</code>"))
	for page, body := range pages {
		contains(t, page, body, "<code>"+html.EscapeString(file)+" and more</code>", html.EscapeString(file)+":12.")
		if n := len(regexp.MustCompile(`<a class="path"`).FindAllString(body, -1)); n != copies[page] {
			t.Errorf("%s has %d path links, want %d", page, n, copies[page])
		}
	}
}
