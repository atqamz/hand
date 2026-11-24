package desktop

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

var breakCases = []struct{ name, in, want string }{
	{"setext shape", "Label\n---\nbody", "Label\n***\nbody"},
	{"setext with trailing spaces", "Label\n----  \nbody", "Label\n***\nbody"},
	{"standup shape", "**Done**\none\ntwo\n---\n**Next**\nthree\n", "**Done**\\\none\\\ntwo\n***\n**Next**\\\nthree\n"},
	{"rule after blank line", "text\n\n---\nmore\nlines", "text\n\n---\nmore\\\nlines"},
	{"rule at the start", "---\ntitle: x\n---\nbody", "---\ntitle: x\n***\nbody"},
	{"rule after heading", "# H\n---\ntext", "# H\n---\ntext"},
	{"rule after list", "- a\n- b\n---\nx\ny", "- a\n- b\n---\nx\\\ny"},
	{"rule inside a list item", "- a\n  b\n  ---", "- a\\\n  b\n  ***"},
	{"star rule after text", "text\n***\nmore", "text\n***\nmore"},
	{"spaced star rule after text", "text\n* * *\nmore", "text\n* * *\nmore"},
	{"underscore rule after text", "text\n___\nmore", "text\n___\nmore"},
	{"spaced dash rule after text", "text\n- - -\nmore", "text\n- - -\nmore"},
	{"two dashes stay", "text\n--\nmore", "text\n--\nmore"},
	{"equals underline stays", "text\n===\nmore", "text\n===\nmore"},
	{"single newlines", "a\nb\nc", "a\\\nb\\\nc"},
	{"blank line separates paragraphs", "a\nb\n\nc\nd", "a\\\nb\n\nc\\\nd"},
	{"one line", "x", "x"},
	{"empty", "", ""},
	{"newline only", "\n", "\n"},
	{"no trailing newline", "x\ny", "x\\\ny"},
	{"trailing newline", "x\ny\n", "x\\\ny\n"},
	{"heading then text", "# H\ntext\nmore", "# H\ntext\\\nmore"},
	{"text then heading", "text\n# H\nmore", "text\n# H\nmore"},
	{"fence with rule and newlines", "```\na\n---\nb\n```\ntext\n---", "```\na\n---\nb\n```\ntext\n***"},
	{"fence with language", "```go\nx := 1\ny := 2\n```\nafter\nlines", "```go\nx := 1\ny := 2\n```\nafter\\\nlines"},
	{"an indented fence line does not close", "```\n    ```\na\nb\n```\nx\ny", "```\n    ```\na\nb\n```\nx\\\ny"},
	{"fence on an item line", "- ```\n  a\n  b\n  ```\n  after\n  lines", "- ```\n  a\n  b\n  ```\n  after\\\n  lines"},
	{"fence on an ordered item line", "1. ~~~\n   a\n   b\n   ~~~", "1. ~~~\n   a\n   b\n   ~~~"},
	{"item text is not a fence", "- a\n  b", "- a\\\n  b"},
	{"link definition", "text\nmore\n\n[guide]: https://example.com/g\n[other]: <https://example.com/o> \"T\"\nafter\nlines", "text\\\nmore\n\n[guide]: https://example.com/g\n[other]: <https://example.com/o> \"T\"\nafter\\\nlines"},
	{"link definition with the address on the next line", "[guide]:\nhttps://example.com/g\nnext\nlines", "[guide]:\nhttps://example.com/g\nnext\\\nlines"},
	{"unclosed fence", "```\na\nb", "```\na\nb"},
	{"longer fence closes later", "````\n```\na\n---\n````\nx\ny", "````\n```\na\n---\n````\nx\\\ny"},
	{"fence between text", "a\nb\n```\nc\nd\n```\ne\nf", "a\\\nb\n```\nc\nd\n```\ne\\\nf"},
	{"tilde fence", "~~~\na\n---\nb\n~~~\nx\ny", "~~~\na\n---\nb\n~~~\nx\\\ny"},
	{"tilde fence is not closed by backticks", "~~~\na\n```\nb\n~~~\nx", "~~~\na\n```\nb\n~~~\nx"},
	{"fence inside a list item", "- item\n\n  ```\n  a\n  ---\n  b\n  ```\n  after\n  lines", "- item\n\n  ```\n  a\n  ---\n  b\n  ```\n  after\\\n  lines"},
	{"quote", "> a\n> b\n> c", "> a\\\n> b\\\n> c"},
	{"nested quote", "> a\n> b\n> > c\n> > d", "> a\\\n> b\n> > c\\\n> > d"},
	{"double quote marks", ">> a\n>> b", ">> a\\\n>> b"},
	{"quote with fence", "> ```\n> a\n> b\n> ```\n> x\n> y", "> ```\n> a\n> b\n> ```\n> x\\\n> y"},
	{"quote with rule", "> text\n> ---\n> more\n> lines", "> text\n> ***\n> more\\\n> lines"},
	{"quote then text", "> a\n> b\n\nc\nd", "> a\\\n> b\n\nc\\\nd"},
	{"table", "| a | b |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |\nafter\nlines", "| a | b |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |\nafter\\\nlines"},
	{"table after text", "intro\nmore\n| a | b |\n|---|---|\n| 1 | 2 |", "intro\\\nmore\n| a | b |\n|---|---|\n| 1 | 2 |"},
	{"table without outer pipes", "a | b\n--|--\n1 | 2\n3 | 4", "a | b\n--|--\n1 | 2\n3 | 4"},
	{"pipe line over a rule is not a table", "a | b\n---\nc", "a | b\n***\nc"},
	{"hard breaks already present", "one  \ntwo\\\nthree\nfour", "one  \ntwo\\\nthree\\\nfour"},
	{"escaped backslash is not a break", "one\\\\\ntwo", "one\\\\\\\ntwo"},
	{"crlf", "a\r\nb\r\n---\r\nc", "a\\\nb\n***\nc"},
	{"crlf fence", "```\r\na\r\n---\r\n```\r\nx\r\ny", "```\na\n---\n```\nx\\\ny"},
	{"lone cr", "a\rb", "a\\\nb"},
	{"indented code", "    code\n    more\n\ntext\nmore", "    code\n    more\n\ntext\\\nmore"},
	{"indented code with a rule", "    a\n    ---\n    b", "    a\n    ---\n    b"},
	{"tab indented code", "\tcode\n\tmore", "\tcode\n\tmore"},
	{"indented text continues a paragraph", "a\n    b\nc", "a\\\n    b\\\nc"},
	{"code in a list item", "- item\n\n      code\n      more\n\n  after\n  lines", "- item\n\n      code\n      more\n\n  after\\\n  lines"},
	{"code span across lines", "`a\nb` c\nd", "`a\nb` c\\\nd"},
	{"double backtick span across lines", "x ``a\n`b\nc`` d\ne", "x ``a\n`b\nc`` d\\\ne"},
	{"code span on one line", "`a` b\n`c` d", "`a` b\\\n`c` d"},
	{"unmatched backtick", "a `b\nc", "a `b\\\nc"},
	{"escaped backtick", "a \\`b\nc `d` e", "a \\`b\\\nc `d` e"},
	{"list items", "- a\n- b\n\ntext\nmore", "- a\n- b\n\ntext\\\nmore"},
	{"item with continuation", "- a\n  b\n- c", "- a\\\n  b\n- c"},
	{"ordered item with continuation", "1. a\n   b\n   c", "1. a\\\n   b\\\n   c"},
	{"paragraph then list", "text\nmore\n- a\n- b", "text\\\nmore\n- a\n- b"},
}

func TestSoftBreaks(t *testing.T) {
	for _, c := range breakCases {
		t.Run(c.name, func(t *testing.T) {
			got := softBreaks(c.in)
			if got != c.want {
				t.Fatalf("softBreaks(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
			if again := softBreaks(got); again != got {
				t.Fatalf("not idempotent: %q then %q", got, again)
			}
		})
	}
}

func TestSoftBreaksLeaveOperatorTextAsIs(t *testing.T) {
	md := literalRefs("f1", "a\nb\n---\nc t1")
	if got := softBreaks(md); got != md {
		t.Fatalf("literal text changed:\n got %q\nwant %q", got, md)
	}
}

func TestSoftBreaksVeryLongParagraph(t *testing.T) {
	const n = 20000
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("word %d `code`", i)
	}
	start := time.Now()
	got := softBreaks(strings.Join(lines, "\n"))
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v", d)
	}
	if c := strings.Count(got, "\\\n"); c != n-1 {
		t.Fatalf("breaks = %d, want %d", c, n-1)
	}
	if strings.HasSuffix(got, `\`) {
		t.Fatal("the last line carries a break")
	}
}
