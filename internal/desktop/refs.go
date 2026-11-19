package desktop

import (
	"strings"

	"github.com/atqamz/hand/internal/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

const (
	refMemoMax      = 512
	refMemoSrcMax   = 64 << 10
	refMemoBytesMax = 4 << 20
)

type refKey struct{ fleet, src string }

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
		var open []emph
		for j := 0; j < len(in); {
			for len(open) > 0 && open[len(open)-1].next <= j {
				open = open[:len(open)-1]
			}
			switch in[j].Kind {
			case parser.Link, parser.Image:
				j = in[j].Next
				continue
			case parser.Strong, parser.Emphasis, parser.Strikethrough:
				if e, ok := emphOf(src, in[j]); ok {
					open = append(open, e)
				}
			case parser.Text:
				lo, hi := 0, len(src)
				if n := len(open); n > 0 {
					lo, hi = open[n-1].lo, open[n-1].hi
				}
				last = linkText(&b, fleet, src, in[j], lo, hi, last)
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

func linkText(b *strings.Builder, fleet, src string, t parser.Inline, lo, hi, last int) int {
	if t.Start < last || t.End > len(src) || src[t.Start:t.End] != t.Text && strings.Contains(src[t.Start:t.End], "\n") {
		return last
	}
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

type emph struct{ lo, hi, next int }

func emphOf(src string, e parser.Inline) (emph, bool) {
	d := 1
	switch e.Kind {
	case parser.Strong:
		d = 2
	case parser.Strikethrough:
		if strings.HasPrefix(src[e.Start:], "~~") {
			d = 2
		}
	}
	return emph{e.Start + d, e.End - d, e.Next}, e.Start+d <= e.End-d
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
	s := linkRefs(k.fleet, src)
	n := len(src) + len(s)
	if len(src) > refMemoSrcMax {
		return s
	}
	if a.refMemo == nil || len(a.refMemo) >= refMemoMax || a.refMemoBytes+n > refMemoBytesMax {
		a.refMemo, a.refMemoBytes = map[refKey]string{}, 0
	}
	a.refMemo[k] = s
	a.refMemoBytes += n
	return s
}
