package board

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

const (
	maxInlineImage = 4 << 20
	maxInlineTotal = 16 << 20
	htmlSpace      = " \t\n\f\r"
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
	name   string
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

func atTag(low []byte, name string) bool {
	k := 1 + len(name)
	return len(low) > k && low[0] == '<' && string(low[1:k]) == name && strings.IndexByte(htmlSpace+"/>", low[k]) >= 0
}

// shortcut: only <img src> is rewritten, srcset, <picture>, <input type=image> and CSS url() keep not loading; upgrade when a report needs them.
func (b *Board) inlineImages(h []byte, rel string) []byte {
	low := asciiLower(h)
	if bytes.Contains(low, []byte("<base")) {
		return h
	}
	n := &inlined{home: b.o.Home, dir: path.Dir(rel), left: maxInlineTotal, seen: map[string]inlineEntry{}}
	var out bytes.Buffer
	last := 0
	for i := 0; i < len(h); {
		j := bytes.IndexByte(h[i:], '<')
		if j < 0 {
			break
		}
		i += j
		switch {
		case bytes.HasPrefix(low[i:], []byte("<!--")):
			end := bytes.Index(low[i+4:], []byte("-->"))
			if end < 0 {
				i = len(h)
			} else {
				i += 4 + end + 3
			}
		case atTag(low[i:], "script"), atTag(low[i:], "style"), atTag(low[i:], "textarea"), atTag(low[i:], "title"):
			name := string(low[i+1 : i+1+bytes.IndexAny(low[i+1:], htmlSpace+"/>")])
			end := bytes.Index(low[i:], []byte("</"+name))
			if end < 0 {
				i = len(h)
			} else {
				i += end + 1
			}
		case atTag(low[i:], "img"):
			end, attrs, ok := parseImg(h[i:])
			if !ok {
				i++
				break
			}
			if v, vs, ve, ok := imgSrc(h[i:i+end], attrs); ok {
				if uri := n.uri(v); uri != "" {
					out.Write(h[last : i+vs])
					out.WriteString(`"` + uri + `"`)
					last = i + ve
				}
			}
			i += end
		default:
			i++
		}
	}
	if last == 0 {
		return h
	}
	out.Write(h[last:])
	return out.Bytes()
}

func parseImg(t []byte) (end int, attrs []imgAttr, ok bool) {
	i := 4
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
		for i < len(t) && strings.IndexByte(htmlSpace+"/>=", t[i]) < 0 {
			i++
		}
		if i == s {
			return 0, nil, false
		}
		a := imgAttr{name: string(asciiLower(t[s:i]))}
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
		attrs = append(attrs, a)
	}
	return 0, nil, false
}

func imgSrc(t []byte, attrs []imgAttr) (v string, vs, ve int, ok bool) {
	for _, a := range attrs {
		if a.name != "src" {
			continue
		}
		if ok || a.ve == 0 {
			return "", 0, 0, false
		}
		v, vs, ve, ok = string(t[a.vs:a.ve]), a.vs, a.ve, true
	}
	return strings.Trim(v, `"'`), vs, ve, ok
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
		if n.left > 0 {
			e = n.load(rel)
		}
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
