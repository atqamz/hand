package desktop

import (
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

const (
	refMemoMax      = 512
	refMemoSrcMax   = 64 << 10
	refMemoBytesMax = 4 << 20
)

type refKey struct {
	fleet, src string
	literal    bool
}

func linkRefs(fleet, src string) string {
	rs := refRanges(fleet, src)
	if len(rs) == 0 {
		return src
	}
	var b strings.Builder
	last := 0
	for _, r := range rs {
		b.WriteString(src[last:r[0]])
		b.WriteString(refLink(fleet, src[r[0]:r[1]]))
		last = r[1]
	}
	b.WriteString(src[last:])
	return b.String()
}

func refLink(fleet, ref string) string {
	return "[" + ref + "](/" + fleet + "/ref/" + ref + ` "Open ` + ref + `")`
}

func refRanges(fleet, src string) [][2]int {
	if !refFleet(fleet) || !strings.ContainsAny(src, "tdar") {
		return nil
	}
	d := parser.New(parser.Default)
	d.Set(src)
	var rs [][2]int
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
				rs, last = textRefs(rs, src, in[j], lo, hi, last)
			}
			j++
		}
	}
	return rs
}

func textRefs(rs [][2]int, src string, t parser.Inline, lo, hi, last int) ([][2]int, int) {
	if t.Start < last || t.End > len(src) || src[t.Start:t.End] != t.Text && strings.Contains(src[t.Start:t.End], "\n") {
		return rs, last
	}
	for k := t.Start; k < t.End; k++ {
		n := markdown.RefAt(src[lo:hi], k-lo)
		if n == 0 || k+n > t.End {
			continue
		}
		rs = append(rs, [2]int{k, k + n})
		last, k = k+n, k+n-1
	}
	return rs, last
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

func literalRefs(fleet, src string) string {
	rs := refRanges(fleet, src)
	if len(rs) == 0 {
		return ""
	}
	end := len(strings.TrimRight(src, " \t\r\n"))
	var b strings.Builder
	bol := true
	for i := 0; i < end; {
		if len(rs) > 0 && rs[0][0] == i {
			b.WriteString(refLink(fleet, src[i:rs[0][1]]))
			i, rs, bol = rs[0][1], rs[1:], false
			continue
		}
		c := src[i]
		i++
		switch {
		case c == '\r' && i < end && src[i] == '\n':
		case c == '\r' || c == '\n':
			b.WriteString("\\\n")
			bol = true
		case bol && c == ' ':
			b.WriteString("&#32;")
		case bol && c == '\t':
			b.WriteString("&#9;")
		case c > ' ' && c < 0x7f && !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'):
			b.WriteString("&#" + strconv.Itoa(int(c)) + ";")
			bol = false
		default:
			b.WriteByte(c)
			bol = false
		}
	}
	return b.String()
}

func (a *App) linked(src string) string { return a.memo(src, false) }

func (a *App) literal(src string) string { return a.memo(src, true) }

func (a *App) memo(src string, literal bool) string {
	k := refKey{a.Fleet.ID, src, literal}
	if s, ok := a.refMemo[k]; ok {
		return s
	}
	var s string
	if literal {
		s = literalRefs(k.fleet, src)
	} else {
		s = linkRefs(k.fleet, src)
	}
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
