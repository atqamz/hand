package board_test

import (
	"html"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/markdown"
)

var pathHrefs = regexp.MustCompile(`<a class="path" href="([^"]*)"`)

func exportedLinks(text string, resolve func(string) string) []string {
	var out []string
	left := 50
	offer := func(p string) bool {
		if left == 0 {
			return false
		}
		left--
		href := resolve(p)
		if href != "" {
			out = append(out, href)
		}
		return href != ""
	}
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		line = strings.NewReplacer("**", " ", "~~", " ", "*", " ").Replace(line)
		for i := 0; i < len(line); {
			if line[i] == '`' {
				if j := strings.IndexByte(line[i+1:], '`'); j > 0 {
					if p := line[i+1 : i+1+j]; markdown.PathCode(p) {
						offer(p)
					}
					i += j + 2
					continue
				}
			}
			if n := markdown.PathAt(line, i); n > 0 && offer(line[i:i+n]) {
				i += n
				continue
			}
			i++
		}
	}
	return out
}

func TestRenderLinksWhatTheExportedCandidatesAndResolutionAccept(t *testing.T) {
	home := fleetFolder(t, map[string]string{
		"reports/one.md":      "# one\n",
		"reports/t1/a b.md":   "# ab\n",
		"reports/t1/flow.png": string(flowPNG),
		"reports/t1/sub/x.md": "# x\n",
		"hand.db":             secret,
		"top.md":              secret,
		".secret/key":         secret,
		"projects/app/R.md":   secret,
	})
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	abs := func(rel string) string { return filepath.Join(home, filepath.FromSlash(rel)) }
	corpus := []string{
		"`reports/one.md`",
		"`./reports/one.md` and `reports/missing.md`",
		"`reports/` `reports/t1/sub`",
		"`reports/t1/a b.md` `reports/t1/flow.png`",
		"`reports/one.md:12` `reports/one.md#L3` `reports/../top.md` `top.md` `projects/app/R.md`",
		"`" + abs("reports/one.md") + "` `~/reports/one.md` `~/top.md`",
		"read " + abs("reports/one.md") + ", then " + abs("reports/t1/flow.png") + ". Not " + abs("hand.db") + ".",
		"(see " + abs("reports/one.md") + ") and ~/reports/t1/sub/x.md",
		"**" + abs("reports/one.md") + "** *" + abs("reports/t1/flow.png") + "* ~~" + abs("reports/t1/sub/x.md") + "~~",
		"**`reports/one.md`** **reports/one.md** reports/one.md",
		"- " + abs("reports/one.md") + "\n- `reports/t1/flow.png`\n1. ~/reports/one.md",
		"> " + abs("reports/one.md") + " `reports/one.md`",
		"| a | b |\n|---|---|\n| " + abs("reports/one.md") + " | `reports/t1/flow.png` |",
		"# " + abs("reports/one.md") + "\n\n## `reports/one.md`",
		"```\n" + abs("reports/one.md") + "\n`reports/one.md`\n```\n\n" + abs("reports/t1/flow.png"),
		"x" + abs("reports/one.md") + " a/" + abs("reports/one.md") + " https://x.test" + abs("reports/one.md"),
		"[" + abs("reports/one.md") + "](https://x.test/y) `" + abs("reports/one.md"),
		"`reports/one.md` `reports/one.md` " + abs("reports/one.md") + " " + abs("reports/one.md"),
		strings.Repeat("`reports/one.md` ", 40) + strings.Repeat(abs("reports/t1/flow.png")+" ", 40),
		"",
	}
	local := func(p string) string { return board.DocHref(home, "", p) }
	linked := 0
	for _, text := range corpus {
		var got []string
		for _, m := range pathHrefs.FindAllStringSubmatch(string(markdown.Render(text, local)), -1) {
			got = append(got, html.UnescapeString(m[1]))
		}
		want := exportedLinks(text, local)
		if !slices.Equal(got, want) {
			t.Errorf("%q\nrendered %q\nexported %q", text, got, want)
		}
		linked += len(got)
	}
	if linked < 40 {
		t.Errorf("corpus produced only %d links", linked)
	}
}
