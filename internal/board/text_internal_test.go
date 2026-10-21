package board

import (
	"bufio"
	"bytes"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

var ownMarkup = regexp.MustCompile(`<div class="chunk" data-line="\d+"( data-partial)?>|<pre class="text">|<pre class="gut" aria-hidden="true">|<span class="(ctl|cut|x-[krgybmcw]|x-[krgybmcw] x-bold|x-bold)">|</(span|pre|div)>`)

func FuzzTheLineWriterEmitsNoMarkupOfItsOwn(f *testing.F) {
	for _, s := range []string{
		"<img src=x onerror=alert(1)>", "a<b", "\x1b[31m<script>\x1b[0m", "x\r<y", "&lt;", "\"'", "\u202e<", "\x1b]8;;javascript:alert(1)\x1b\\<a>\x1b]8;;\x1b\\",
		"\x1b[<script>", "\x1b[38;5;1;31m</span><b>", "\x1b[1m\x1b[32mgreen\nstill\x1b[0m\n", "\x00\x01\x7f\xff\xc3", "\u2028</pre>\u0085", "a\r\nb\rc\n\n\nlast",
		"\x1b", "\x1b[", "\x1b]", "\x1b\xe2\x80\xae<", strings.Repeat("<", maxLine+10) + "\n<", "\t\x1b[0;1;22;39;90;97m\x1b[m<!--",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		var out bytes.Buffer
		w := bufio.NewWriter(&out)
		l := &lineWriter{w: w, n: 1}
		if _, err := l.stream(strings.NewReader(s)); err != nil {
			t.Fatal(err)
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if !utf8.ValidString(got) {
			t.Fatalf("%q rendered invalid UTF-8", s)
		}
		for _, tag := range []string{"span", "pre", "div"} {
			if open, shut := regexp.MustCompile(`<`+tag+`[ >]`).FindAllStringIndex(got, -1), strings.Count(got, "</"+tag+">"); len(open) != shut {
				t.Fatalf("%q opens %d <%s> and closes %d:\n%s", s, len(open), tag, shut, got)
			}
		}
		if rest := ownMarkup.ReplaceAllString(got, ""); strings.ContainsAny(rest, "<>\x1b\r\x00") {
			t.Fatalf("%q rendered markup or a raw control:\n%s", s, got)
		}
		for _, c := range ownMarkup.ReplaceAllString(got, "") {
			if c < 0x20 && c != '\n' && c != '\t' || c == 0x7f || c >= 0x80 && c <= 0x9f || hiddenRune(c) {
				t.Fatalf("%q rendered the control %U", s, c)
			}
		}
	})
}
