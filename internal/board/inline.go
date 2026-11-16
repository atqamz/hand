package board

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
)

const (
	maxInlineImage = 4 << 20
	maxInlineTotal = 16 << 20
	maxInlinePaths = 256
	htmlSpace      = " \t\n\f\r"
)

var (
	rawText  = []string{"script", "style", "textarea", "title", "xmp", "noembed", "noframes", "iframe"}
	unparsed = []string{"svg", "math", "select", "frameset", "plaintext"}
)

type inlined struct {
	home, dir string
	left      int
	seen      map[string]inlineEntry
}

type inlineEntry struct {
	uri string
	n   int
}

type imgAttr struct {
	vs, ve int
}

func asciiLower(b []byte) []byte {
	low := bytes.Clone(b)
	for i, c := range low {
		if 'A' <= c && c <= 'Z' {
			low[i] = c + 'a' - 'A'
		}
	}
	return low
}

func (b *Board) inlineImages(h []byte, rel string) []byte {
	low := asciiLower(h)
	n := &inlined{home: b.o.Home, dir: path.Dir(rel), left: maxInlineTotal, seen: map[string]inlineEntry{}}
	var out bytes.Buffer
	last, stopAt, base := 0, -1, false
	i := 0
	halt := func() {
		if stopAt < 0 {
			stopAt = i
		}
		i = len(h)
	}
	for i < len(h) {
		j := bytes.IndexByte(h[i:], '<')
		if j < 0 {
			break
		}
		i += j
		switch {
		case bytes.HasPrefix(low[i:], []byte("<!--")):
			end := commentEnd(low[i+4:])
			if end < 0 {
				halt()
			} else {
				i += 4 + end
			}
		case bytes.HasPrefix(low[i:], []byte("<!")), bytes.HasPrefix(low[i:], []byte("<?")), bytes.HasPrefix(low[i:], []byte("</")) && !atTagStart(low[i:]):
			end := bytes.IndexByte(h[i:], '>')
			if end < 0 {
				halt()
			} else {
				i += end + 1
			}
		case atTagStart(low[i:]):
			k := 1
			if low[i+k] == '/' {
				k++
			}
			closing := k == 2
			name := low[i+k : i+k+tagNameLen(low[i+k:])]
			end, attrs, ok := parseTag(h[i:], k+len(name))
			if !ok {
				halt()
				break
			}
			base = base || !closing && string(name) == "base"
			if !closing && slices.Contains(unparsed, string(name)) {
				halt()
				break
			}
			if !closing && (string(name) == "img" || string(name) == "image") {
				if v, vs, ve, ok := imgSrc(h[i:i+end], attrs); ok {
					if uri := n.uri(v); uri != "" {
						out.Write(h[last : i+vs])
						out.WriteString(`"` + uri + `"`)
						last = i + ve
					}
				}
			}
			i += end
			if !closing && slices.Contains(rawText, string(name)) {
				at := rawEnd(low[i:], string(name))
				if at < 0 || string(name) == "script" && bytes.Contains(low[i:i+at], []byte("<!--")) {
					halt()
				} else {
					i += at
				}
			}
		default:
			i++
		}
	}
	if base || stopAt >= 0 && bytes.Contains(low[stopAt:], []byte("<base")) || last == 0 {
		return h
	}
	out.Write(h[last:])
	return out.Bytes()
}

func commentEnd(low []byte) int {
	if bytes.HasPrefix(low, []byte(">")) {
		return 1
	}
	if bytes.HasPrefix(low, []byte("->")) {
		return 2
	}
	for from := 0; ; {
		at := bytes.Index(low[from:], []byte("--"))
		if at < 0 {
			return -1
		}
		at += from
		if bytes.HasPrefix(low[at+2:], []byte(">")) {
			return at + 3
		}
		if bytes.HasPrefix(low[at+2:], []byte("!>")) {
			return at + 4
		}
		from = at + 1
	}
}

func rawEnd(low []byte, name string) int {
	closer := []byte("</" + name)
	for from := 0; from < len(low); {
		at := bytes.Index(low[from:], closer)
		if at < 0 {
			return -1
		}
		at += from
		if next := at + len(closer); next < len(low) && strings.IndexByte(htmlSpace+"/>", low[next]) >= 0 {
			return at
		}
		from = at + 1
	}
	return -1
}

func atTagStart(low []byte) bool {
	k := 1
	if len(low) > 1 && low[1] == '/' {
		k = 2
	}
	return len(low) > k && 'a' <= low[k] && low[k] <= 'z'
}

func tagNameLen(low []byte) int {
	k := 0
	for k < len(low) && strings.IndexByte(htmlSpace+"/>", low[k]) < 0 {
		k++
	}
	return k
}

func parseTag(t []byte, start int) (end int, attrs []imgAttr, ok bool) {
	i := start
	for i < len(t) {
		for i < len(t) && strings.IndexByte(htmlSpace+"/", t[i]) >= 0 {
			i++
		}
		if i >= len(t) {
			break
		}
		if t[i] == '>' {
			return i + 1, attrs, true
		}
		s := i
		if t[i] == '=' {
			i++
		}
		for i < len(t) && strings.IndexByte(htmlSpace+"/>=", t[i]) < 0 {
			i++
		}
		src := len(t[s:i]) == 3 && t[s]|0x20 == 's' && t[s+1]|0x20 == 'r' && t[s+2]|0x20 == 'c'
		var a imgAttr
		for i < len(t) && strings.IndexByte(htmlSpace, t[i]) >= 0 {
			i++
		}
		if i < len(t) && t[i] == '=' {
			for i++; i < len(t) && strings.IndexByte(htmlSpace, t[i]) >= 0; i++ {
			}
			if i >= len(t) {
				break
			}
			a.vs = i
			if q := t[i]; q == '"' || q == '\'' {
				e := bytes.IndexByte(t[i+1:], q)
				if e < 0 {
					return 0, nil, false
				}
				i += e + 2
			} else {
				for i < len(t) && strings.IndexByte(htmlSpace+">", t[i]) < 0 {
					i++
				}
			}
			a.ve = i
		}
		if src && len(attrs) < 2 {
			attrs = append(attrs, a)
		}
	}
	return 0, nil, false
}

func imgSrc(t []byte, attrs []imgAttr) (v string, vs, ve int, ok bool) {
	if len(attrs) != 1 || attrs[0].ve == 0 {
		return "", 0, 0, false
	}
	v, vs, ve, ok = string(t[attrs[0].vs:attrs[0].ve]), attrs[0].vs, attrs[0].ve, true
	if len(v) > 1 && (v[0] == '"' || v[0] == '\'') {
		v = v[1 : len(v)-1]
	}
	return v, vs, ve, ok
}

func (n *inlined) uri(v string) string {
	v = strings.Trim(v, htmlSpace)
	if v == "" || strings.ContainsAny(v, `&\`) {
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path == "" || u.Path[0] == '/' || strings.HasSuffix(u.Path, "/") {
		return ""
	}
	rel := path.Join(n.dir, u.Path)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return ""
	}
	e, ok := n.seen[rel]
	if !ok {
		if n.left <= 0 || len(n.seen) >= maxInlinePaths {
			return ""
		}
		e = n.load(rel)
		n.seen[rel] = e
	}
	if e.n == 0 || e.n > n.left {
		return ""
	}
	n.left -= e.n
	return e.uri
}

func (n *inlined) load(rel string) inlineEntry {
	root, real, fi, err := Resolve(n.home, rel)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 || fi.Size() > min(maxInlineImage, int64(n.left)) {
		return inlineEntry{}
	}
	f, _, err := openSame(n.home, rel, root, real)
	if err != nil {
		return inlineEntry{}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxInlineImage+1))
	if err != nil || len(data) > maxInlineImage {
		return inlineEntry{}
	}
	head := data[:min(len(data), headBytes)]
	mt := http.DetectContentType(head)
	if _, image := imageExt[mt]; !image || !imageLike.MatchString(rel) {
		if mt = ""; strings.EqualFold(path.Ext(rel), ".svg") && textHead(head) {
			mt = "image/svg+xml"
		} else {
			return inlineEntry{}
		}
	}
	return inlineEntry{"data:" + mt + ";base64," + base64.StdEncoding.EncodeToString(data), len(data)}
}
