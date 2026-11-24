package desktop

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/markdown/parser"

	"github.com/atqamz/hand/internal/markdown"
)

const standupShape = "**Shipped**\nExport job retried clean\nManifest written last\nBackfill at 30 days\n---\n**Next**\nRegion split\nAlert on stale manifest\n---\n**Blocked**\nNothing\n"

var boardBlock = regexp.MustCompile(`(?s)<p>.*?</p>|<hr>|<h[1-6][^>]*>|<pre>|<blockquote>`)

func boardShape(src string) string {
	var out []string
	for _, m := range boardBlock.FindAllString(string(markdown.Render(src, nil)), -1) {
		switch {
		case strings.HasPrefix(m, "<p>"):
			out = append(out, fmt.Sprintf("P%d", 1+strings.Count(m, "<br>")))
		case m == "<hr>":
			out = append(out, "HR")
		case strings.HasPrefix(m, "<h"):
			out = append(out, "H")
		case m == "<pre>":
			out = append(out, "CODE")
		default:
			out = append(out, "QUOTE")
		}
	}
	return strings.Join(out, " ")
}

func desktopShape(src string) string {
	d := parser.Parse(src)
	var out []string
	for i := 1; i < d.Len(); i++ {
		switch d.Block(i).Kind {
		case parser.Paragraph:
			n := 1
			for _, in := range d.Inlines(i) {
				switch in.Kind {
				case parser.LineBreak:
					n++
				case parser.SoftBreak:
					out = append(out, "SOFT")
				}
			}
			out = append(out, fmt.Sprintf("P%d", n))
		case parser.ThematicBreak:
			out = append(out, "HR")
		case parser.Heading:
			out = append(out, "H")
		case parser.CodeBlock:
			out = append(out, "CODE")
		case parser.Quote:
			out = append(out, "QUOTE")
		}
	}
	return strings.Join(out, " ")
}

func TestDesktopBlocksEqualTheBoards(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"standup", standupShape, "P4 HR P3 HR P2"},
		{"label above a rule", "Label\n---\nbody", "P1 HR P1"},
		{"rule after a blank line", "a\nb\n\n---\nc\nd", "P2 HR P2"},
		{"heading and lines", "# Title\nfirst\nsecond", "H P2"},
		{"code keeps its rule", "intro\nmore\n```\na\n---\nb\n```\nafter\nlines", "P2 CODE P2"},
		{"quote lines", "> a\n> b\n\nc\nd", "QUOTE P2 P2"},
		{"front matter", "---\ntitle: x\nowner: y\n---\nbody", "HR P2 HR P1"},
		{"crlf", strings.ReplaceAll(standupShape, "\n", "\r\n"), "P4 HR P3 HR P2"},
	} {
		t.Run(c.name, func(t *testing.T) {
			board := boardShape(c.src)
			if board != c.want {
				t.Fatalf("board shape = %q, want %q", board, c.want)
			}
			if got := desktopShape(softBreaks(c.src)); got != board {
				t.Fatalf("desktop shape = %q, board = %q", got, board)
			}
		})
	}
}

func TestDesktopBlocksDifferWithoutThePrePass(t *testing.T) {
	if got := desktopShape(standupShape); got == boardShape(standupShape) {
		t.Fatalf("mygo alone already gives the Board's shape %q", got)
	}
}
