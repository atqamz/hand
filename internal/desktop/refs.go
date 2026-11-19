package desktop

import (
	"strings"

	"github.com/atqamz/hand/internal/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

const refMemoMax = 512

type refKey struct{ fleet, src string }

// linkRefs turns each tNNN, aNNN, dNNN and rNNN of src's plain text into a
// link to its page, on the Board's boundary rules (markdown.RefAt). Text of
// a link, a code span, a code block, HTML or an autolink is left as written.
func linkRefs(fleet, src string) string {
	if !refFleet(fleet) || !strings.ContainsAny(src, "tdar") {
		return src
	}
	d := parser.New(parser.Default)
	d.Set(src)
	var b strings.Builder
	last := 0
	for i := 0; i < d.Len(); i++ {
		in := d.Inlines(i)
		for j := 0; j < len(in); {
			switch in[j].Kind {
			case parser.Link, parser.Image:
				j = in[j].Next
				continue
			case parser.Text:
				last = linkText(&b, fleet, src, in, j, last)
			}
			j++
		}
	}
	if last == 0 {
		return src
	}
	b.WriteString(src[last:])
	return b.String()
}

func linkText(b *strings.Builder, fleet, src string, in []parser.Inline, j, last int) int {
	t := in[j]
	if t.Start < last || t.End > len(src) || src[t.Start:t.End] != t.Text && strings.Contains(src[t.Start:t.End], "\n") {
		return last
	}
	lo, hi := innerOf(src, in, j)
	for k := t.Start; k < t.End; k++ {
		n := markdown.RefAt(src[lo:hi], k-lo)
		if n == 0 || k+n > t.End {
			continue
		}
		ref := src[k : k+n]
		b.WriteString(src[last:k])
		b.WriteString("[" + ref + "](/" + fleet + "/ref/" + ref + ` "Open ` + ref + `")`)
		last, k = k+n, k+n-1
	}
	return last
}

// innerOf bounds the text a ref at in[j] is read in: the inside of the
// nearest emphasis around it, as the Board reads it, else the whole source.
func innerOf(src string, in []parser.Inline, j int) (int, int) {
	for p := j - 1; p >= 0; p-- {
		e := in[p]
		if e.Next <= j {
			continue
		}
		d := 1
		switch e.Kind {
		case parser.Strong:
			d = 2
		case parser.Strikethrough:
			if strings.HasPrefix(src[e.Start:], "~~") {
				d = 2
			}
		case parser.Emphasis:
		default:
			continue
		}
		if e.Start+d <= e.End-d {
			return e.Start + d, e.End - d
		}
	}
	return 0, len(src)
}

func refFleet(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

func (a *App) linked(src string) string {
	k := refKey{a.Fleet.ID, src}
	if s, ok := a.refMemo[k]; ok {
		return s
	}
	if a.refMemo == nil || len(a.refMemo) >= refMemoMax {
		a.refMemo = map[refKey]string{}
	}
	s := linkRefs(k.fleet, src)
	a.refMemo[k] = s
	return s
}
