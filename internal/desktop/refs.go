package desktop

import (
	"net/url"
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
	paths      bool
	gen        int
}

type refVal struct {
	md    string
	cands []string
}

type span struct {
	lo, hi    int
	cand, rel string
}

var (
	newParser = func() *parser.Document { return parser.New(parser.Default) }
	pathAt    = markdown.PathAt
)

func linkRefs(fleet, src string, rel func(string) string) (string, []string) {
	rs, cands := refRanges(fleet, src, rel)
	if len(rs) == 0 {
		return src, cands
	}
	var b strings.Builder
	last := 0
	for _, r := range rs {
		b.WriteString(src[last:r.lo])
		if r.rel != "" {
			b.WriteString(pathLink(fleet, src[r.lo:r.hi], r.rel))
		} else {
			b.WriteString(refLink(fleet, src[r.lo:r.hi]))
		}
		last = r.hi
	}
	b.WriteString(src[last:])
	return b.String(), cands
}

var labelEscape = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "[", "&#91;", "]", "&#93;", "&", `\&`, "~", `\~`, "|", `\|`, "!", `\!`)

func pathLink(fleet, label, rel string) string {
	if !strings.HasPrefix(label, "`") {
		label = labelEscape.Replace(label)
	}
	return "[" + label + "](/" + fleet + "/doc/" + (&url.URL{Path: rel}).EscapedPath() + ")"
}

func refLink(fleet, ref string) string {
	return "[" + ref + "](/" + fleet + "/ref/" + ref + ` "Open ` + ref + `")`
}

type walk struct {
	src      string
	rel      func(string) string
	rs       []span
	cands    []string
	last     int
	seen     int
	pathSkip int
}

func refRanges(fleet, src string, rel func(string) string) ([]span, []string) {
	if !refFleet(fleet) || !strings.ContainsAny(src, "tdar") && (rel == nil || !strings.ContainsAny(src, `/\`)) {
		return nil, nil
	}
	d := newParser()
	d.Set(src)
	w := walk{src: src, rel: rel}
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
			case parser.Code:
				w.code(in[j])
			case parser.Text:
				lo, hi := 0, len(src)
				if n := len(open); n > 0 {
					lo, hi = open[n-1].lo, open[n-1].hi
				}
				w.text(in[j], lo, hi)
			}
			j++
		}
	}
	return w.rs, w.cands
}

func (w *walk) code(c parser.Inline) {
	if w.rel == nil || c.Start < w.last || c.End > len(w.src) || c.End-c.Start < 3 || w.src[c.Start] != '`' || w.src[c.End-1] != '`' {
		return
	}
	if raw := w.src[c.Start+1 : c.End-1]; !strings.Contains(raw, "`") && markdown.PathCode(raw) {
		w.cand(c.Start, c.End, raw)
	}
}

func (w *walk) cand(lo, hi int, text string) bool {
	w.seen++
	if w.seen > pathMax || !askable(text) {
		return false
	}
	w.cands = append(w.cands, text)
	if rel := w.rel(text); rel != "" {
		w.rs = append(w.rs, span{lo, hi, text, rel})
		w.last = hi
		return true
	}
	return false
}

func askable(p string) bool {
	if len(p) > pathValueMax {
		return false
	}
	for i := range len(p) {
		if p[i] < ' ' || p[i] == 0x7f {
			return false
		}
	}
	return true
}

func (w *walk) text(t parser.Inline, lo, hi int) {
	src := w.src
	if t.Start < w.last || t.End > len(src) || src[t.Start:t.End] != t.Text && strings.Contains(src[t.Start:t.End], "\n") {
		return
	}
	for k := t.Start; k < t.End; k++ {
		if w.rel != nil && k >= w.pathSkip && w.seen < pathMax {
			if n := pathAt(src[lo:hi], k-lo); n > 0 && k+n <= t.End {
				w.pathSkip = k + n
				if w.cand(k, k+n, src[k:k+n]) {
					k += n - 1
					continue
				}
			}
		}
		n := markdown.RefAt(src[lo:hi], k-lo)
		if n == 0 || k+n > t.End {
			continue
		}
		w.rs = append(w.rs, span{lo: k, hi: k + n})
		w.last, k = k+n, k+n-1
	}
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
	rs, _ := refRanges(fleet, src, nil)
	if len(rs) == 0 {
		return ""
	}
	end := len(strings.TrimRight(src, " \t\r\n"))
	var b strings.Builder
	bol := true
	for i := 0; i < end; {
		if len(rs) > 0 && rs[0].lo == i {
			b.WriteString(refLink(fleet, src[i:rs[0].hi]))
			i, rs, bol = rs[0].hi, rs[1:], false
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

func (a *App) linked(src string) string { return a.memo(src, false, true) }

func (a *App) linkedRefs(src string) string { return a.memo(src, false, false) }

func (a *App) literal(src string) string { return a.memo(src, true, false) }

func (a *App) memo(src string, literal, paths bool) string {
	k := refKey{fleet: a.Fleet.ID, src: src, literal: literal, paths: paths}
	if paths {
		k.gen = a.pathGen
	}
	if v, ok := a.refMemo[k]; ok {
		a.askPaths(k.fleet, v.cands)
		return v.md
	}
	var v refVal
	if literal {
		v.md = literalRefs(k.fleet, src)
	} else {
		var rel func(string) string
		if paths {
			rel = a.pathRel(k.fleet)
		}
		v.md, v.cands = linkRefs(k.fleet, src, rel)
		a.askPaths(k.fleet, v.cands)
	}
	n := len(src) + len(v.md)
	if len(src) > refMemoSrcMax {
		return v.md
	}
	if a.refMemo == nil || len(a.refMemo) >= refMemoMax || a.refMemoBytes+n > refMemoBytesMax {
		a.refMemo, a.refMemoBytes = map[refKey]refVal{}, 0
	}
	a.refMemo[k] = v
	a.refMemoBytes += n
	return v.md
}
