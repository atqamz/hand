package board

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	headSize   = 8 << 10
	maxWhole   = 8 << 20
	window     = 4 << 20
	maxLine    = 256 << 10
	chunkLines = 500
	maxHits    = 200
	textWrite  = 5 * time.Minute
	streamMark = "<hand-text-stream>"
	endMark    = `<span id="text-end" class="text-end" data-next="%d" data-line="%d"></span>`
)

var sgrColors = [...]string{"x-k", "x-r", "x-g", "x-y", "x-b", "x-m", "x-c", "x-w"}

func textHead(head []byte) bool {
	for i := len(head) - 1; i >= max(0, len(head)-utf8.UTFMax+1); i-- {
		if utf8.RuneStart(head[i]) {
			if !utf8.FullRune(head[i:]) {
				head = head[:i]
			}
			break
		}
	}
	return bytes.IndexByte(head, 0) < 0 && utf8.Valid(head)
}

type lineWriter struct {
	w            *bufio.Writer
	n, first, in int
	color        string
	bold         bool
	styled, ctl  bool
	partial      bool
	num          []byte
}

func (l *lineWriter) line(b []byte, cut int64, partial bool) {
	if partial {
		l.close()
	}
	if l.in == 0 {
		l.first = l.n
		l.w.WriteString(`<div class="chunk" data-line="`)
		l.num = strconv.AppendInt(l.num[:0], int64(l.n), 10)
		l.w.Write(l.num)
		if partial {
			l.w.WriteString(`" data-partial`)
		} else {
			l.w.WriteString(`"`)
		}
		l.w.WriteString("><pre class=\"text\">\n")
	}
	l.style()
	l.text(b)
	l.plain()
	if l.styled {
		l.w.WriteString("</span>")
		l.styled = false
	}
	if cut > 0 {
		fmt.Fprintf(l.w, `<span class="cut">… %s more on this line</span>`, size(cut))
	}
	l.w.WriteByte('\n')
	l.n++
	l.partial = partial
	if l.in++; l.in == chunkLines || partial {
		l.close()
	}
}

func (l *lineWriter) close() {
	if l.in == 0 {
		return
	}
	l.w.WriteString("</pre><pre class=\"gut\" aria-hidden=\"true\">\n")
	for i := l.first; i < l.n; i++ {
		l.num = append(strconv.AppendInt(l.num[:0], int64(i), 10), '\n')
		l.w.Write(l.num)
	}
	l.w.WriteString("</pre></div>")
	l.in = 0
}

func (l *lineWriter) style() {
	if l.color == "" && !l.bold {
		return
	}
	l.w.WriteString(`<span class="`)
	l.w.WriteString(l.color)
	if l.bold {
		if l.color != "" {
			l.w.WriteByte(' ')
		}
		l.w.WriteString("x-bold")
	}
	l.w.WriteString(`">`)
	l.styled = true
}

func (l *lineWriter) plain() {
	if l.ctl {
		l.w.WriteString("</span>")
		l.ctl = false
	}
}

func (l *lineWriter) mark() {
	if !l.ctl {
		l.w.WriteString(`<span class="ctl">`)
		l.ctl = true
	}
}

func (l *lineWriter) text(b []byte) {
	if i := bytes.LastIndexByte(b, '\r'); i >= 0 {
		b = b[i+1:]
	}
	for len(b) > 0 {
		switch c := b[0]; {
		case c == 0x1b:
			n, params, sgr := escape(b)
			if sgr {
				l.setSGR(params)
			}
			b = b[n:]
		case c == '\t':
			l.plain()
			l.w.WriteByte('\t')
			b = b[1:]
		case c < 0x20 || c == 0x7f:
			r := 0x2400 + rune(c)
			if c == 0x7f {
				r = 0x2421
			}
			l.mark()
			l.w.WriteRune(r)
			b = b[1:]
		case c < utf8.RuneSelf:
			l.plain()
			i := 0
			for i < len(b) && safe(b[i]) {
				i++
			}
			if i == 0 {
				l.w.WriteString(entity[c])
				i = 1
			} else {
				l.w.Write(b[:i])
			}
			b = b[i:]
		default:
			r, n := utf8.DecodeRune(b)
			switch {
			case r == utf8.RuneError && n == 1:
				l.plain()
				l.w.WriteRune(utf8.RuneError)
			case hiddenRune(r):
				l.mark()
				fmt.Fprintf(l.w, "U+%04X", r)
			default:
				l.plain()
				l.w.Write(b[:n])
			}
			b = b[n:]
		}
	}
}

var entity = [utf8.RuneSelf]string{'<': "&lt;", '>': "&gt;", '&': "&amp;", '"': "&#34;", '\'': "&#39;"}

func safe(c byte) bool {
	return c >= 0x20 && c < 0x7f && entity[c] == ""
}

func hiddenRune(r rune) bool {
	return r >= 0x80 && r <= 0x9f || r == 0x061c || r == 0x200e || r == 0x200f || r >= 0x2028 && r <= 0x202e || r >= 0x2066 && r <= 0x2069
}

func escape(b []byte) (n int, params []byte, sgr bool) {
	if len(b) < 2 {
		return len(b), nil, false
	}
	switch b[1] {
	case '[':
		i := 2
		for i < len(b) && b[i] >= 0x20 && b[i] <= 0x3f {
			i++
		}
		if i < len(b) && b[i] >= 0x40 && b[i] <= 0x7e {
			return i + 1, b[2:i], b[i] == 'm' && (i == 2 || b[2] < '<')
		}
		return i, nil, false
	case ']':
		for i := 2; i < len(b); i++ {
			if b[i] == 0x07 {
				return i + 1, nil, false
			}
			if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\' {
				return i + 2, nil, false
			}
		}
		return len(b), nil, false
	}
	_, k := utf8.DecodeRune(b[1:])
	return 1 + k, nil, false
}

func (l *lineWriter) setSGR(p []byte) {
	l.plain()
	if l.styled {
		l.w.WriteString("</span>")
		l.styled = false
	}
	for skip := 0; ; {
		field, rest, more := bytes.Cut(p, []byte{';'})
		v, ok := sgrNumber(field)
		switch {
		case skip > 0:
			skip--
		case !ok:
		case v == 0:
			l.color, l.bold = "", false
		case v == 1:
			l.bold = true
		case v == 22:
			l.bold = false
		case v == 39:
			l.color = ""
		case v >= 30 && v <= 37:
			l.color = sgrColors[v-30]
		case v >= 90 && v <= 97:
			l.color = sgrColors[v-90]
		case v == 38 || v == 48:
			next, _, _ := bytes.Cut(rest, []byte{';'})
			switch k, _ := sgrNumber(next); k {
			case 5:
				skip = 2
			case 2:
				skip = 4
			}
		}
		if !more {
			break
		}
		p = rest
	}
	l.style()
}

func sgrNumber(b []byte) (int, bool) {
	v := 0
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		v = min(v*10+int(c-'0'), 1000)
	}
	return v, true
}

func fold(dst, b []byte) []byte {
	if i := bytes.LastIndexByte(b, '\r'); i >= 0 {
		b = b[i+1:]
	}
	for len(b) > 0 {
		i := 0
		for i < len(b) && b[i] < utf8.RuneSelf && b[i] != 0x1b {
			i++
		}
		from := len(dst)
		dst = append(dst, b[:i]...)
		for j, c := range dst[from:] {
			if 'A' <= c && c <= 'Z' {
				dst[from+j] = c + 'a' - 'A'
			}
		}
		switch b = b[i:]; {
		case len(b) == 0:
		case b[0] == 0x1b:
			n, _, _ := escape(b)
			b = b[n:]
		default:
			r, n := utf8.DecodeRune(b)
			dst = utf8.AppendRune(dst, unicode.ToLower(r))
			b = b[n:]
		}
	}
	return dst
}

type lineReader struct {
	br   *bufio.Reader
	long []byte
}

func newLineReader(r io.Reader) *lineReader {
	return &lineReader{br: bufio.NewReaderSize(r, 64<<10)}
}

func (s *lineReader) next() (line []byte, used, cut int64, complete bool, err error) {
	s.long = s.long[:0]
	for {
		part, err := s.br.ReadSlice('\n')
		used += int64(len(part))
		if err == nil {
			part, complete = part[:len(part)-1], true
		}
		take := min(len(part), maxLine-len(s.long))
		s.long = append(s.long, part[:take]...)
		cut += int64(len(part) - take)
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case used == 0:
			return nil, 0, 0, false, err
		case err != nil && !errors.Is(err, io.EOF):
			return nil, used, 0, false, err
		}
		return bytes.TrimSuffix(s.long, []byte{'\r'}), used, cut, complete, nil
	}
}

func (l *lineWriter) stream(r io.Reader) (done int64, err error) {
	s := newLineReader(r)
	var at int64
	for {
		line, used, cut, complete, err := s.next()
		if err != nil {
			l.close()
			if errors.Is(err, io.EOF) {
				err = nil
			}
			return done, err
		}
		at += used
		l.line(line, cut, !complete)
		if complete {
			done = at
		}
	}
}

func lineAt(f io.ReaderAt, at int64) (int64, error) {
	buf := make([]byte, 64<<10)
	for end := at; end > 0; {
		n := min(int64(len(buf)), end)
		k, err := f.ReadAt(buf[:n], end-n)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		if i := bytes.LastIndexByte(buf[:k], '\n'); i >= 0 {
			return end - n + int64(i) + 1, nil
		}
		end -= n
	}
	return 0, nil
}

func nextLine(f io.ReaderAt, at, size int64) (int64, error) {
	if at <= 0 || at >= size {
		return min(max(at, 0), size), nil
	}
	buf := make([]byte, 64<<10)
	for from := at - 1; from < size; from += int64(len(buf)) {
		k, err := f.ReadAt(buf[:min(int64(len(buf)), size-from)], from)
		if i := bytes.IndexByte(buf[:k], '\n'); i >= 0 {
			return from + int64(i) + 1, nil
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		if k == 0 {
			break
		}
	}
	return size, nil
}

func newlines(f io.ReaderAt, from, to int64) (n int, last byte, err error) {
	buf := make([]byte, 256<<10)
	for at := from; at < to; {
		k, err := f.ReadAt(buf[:min(int64(len(buf)), to-at)], at)
		n += bytes.Count(buf[:k], []byte{'\n'})
		if k > 0 {
			last = buf[k-1]
		}
		at += int64(k)
		if err != nil && !errors.Is(err, io.EOF) {
			return n, last, err
		}
		if k == 0 {
			break
		}
	}
	return n, last, nil
}

func lineStart(f io.ReaderAt, line int, size int64) (int64, error) {
	buf := make([]byte, 256<<10)
	seen := 1
	for at := int64(0); seen < line && at < size; {
		k, err := f.ReadAt(buf[:min(int64(len(buf)), size-at)], at)
		for i := 0; i < k; {
			j := bytes.IndexByte(buf[i:k], '\n')
			if j < 0 {
				break
			}
			i += j + 1
			if seen++; seen == line {
				if at+int64(i) >= size {
					return -1, nil
				}
				return at + int64(i), nil
			}
		}
		at += int64(k)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		if k == 0 {
			break
		}
	}
	if seen < line {
		return -1, nil
	}
	return 0, nil
}

func find(f io.ReaderAt, size int64, needle string) (hits []int, more bool, err error) {
	want := fold(nil, []byte(needle))
	if len(want) == 0 {
		return nil, false, nil
	}
	s := newLineReader(io.NewSectionReader(f, 0, size))
	var buf []byte
	for n := 1; ; n++ {
		line, _, _, _, err := s.next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
			}
			return hits, false, err
		}
		if buf = fold(buf[:0], line); bytes.Contains(buf, want) {
			if len(hits) == maxHits {
				return hits, true, nil
			}
			hits = append(hits, n)
		}
	}
}

func span(f io.ReaderAt, q url.Values, size int64) (begin, end int64, err error) {
	at, _ := strconv.ParseInt(q.Get("at"), 10, 64)
	to, _ := strconv.ParseInt(q.Get("to"), 10, 64)
	at, to = min(max(at, 0), size), min(max(to, 0), size)
	switch {
	case q.Has("line"):
		n, _ := strconv.Atoi(q.Get("line"))
		if begin, err = lineStart(f, max(n, 1), size); err != nil || begin < 0 {
			return tail(f, size, err)
		}
	case q.Has("at"):
		if begin, err = lineAt(f, at); err != nil || begin >= size {
			return tail(f, size, err)
		}
	case q.Has("to"):
		if end, err = nextLine(f, to, size); err != nil || end > 0 {
			if err == nil {
				begin, err = lineAt(f, max(end-window, 0))
			}
			return begin, end, err
		}
	default:
		return tail(f, size, nil)
	}
	end, err = nextLine(f, min(begin+window, size), size)
	return begin, end, err
}

func tail(f io.ReaderAt, size int64, err error) (int64, int64, error) {
	if err != nil {
		return 0, 0, err
	}
	begin, err := lineAt(f, max(size-window, 0))
	return begin, size, err
}

type textView struct {
	Whole, Tail                bool
	First, Last                int
	Start, Earlier, Later, End string
	Find                       string
	Hits                       []textHit
	More                       bool
}

type textHit struct {
	Line int
	Href string
}

func (b *Board) text(w http.ResponseWriter, r *http.Request, f *os.File, fi fs.FileInfo, self string, data map[string]any) {
	q := r.URL.Query()
	size := fi.Size()
	v := &textView{Whole: size <= maxWhole && !q.Has("at") && !q.Has("to") && !q.Has("line"), First: 1, End: "#text-end"}
	begin, end := int64(0), size
	var err error
	if !v.Whole {
		begin, end, err = span(f, q, size)
		if err == nil {
			var before, in int
			var last byte
			if before, _, err = newlines(f, 0, begin); err == nil {
				in, last, err = newlines(f, begin, end)
			}
			v.First, v.Last = before+1, before+in
			if end > begin && last != '\n' {
				v.Last++
			}
		}
		if begin > 0 {
			v.Start, v.Earlier = self+"?at=0", self+"?to="+strconv.FormatInt(begin, 10)
		}
		if end < size {
			v.Later, v.End = self+"?at="+strconv.FormatInt(end, 10), self+"#text-end"
		}
	}
	v.Tail = end == size
	if needle := strings.TrimSpace(q.Get("find")); needle != "" && err == nil {
		v.Find = needle
		var hits []int
		hits, v.More, err = find(f, size, needle)
		for _, n := range hits {
			href := "#L" + strconv.Itoa(n)
			if !v.Whole {
				href = self + "?" + url.Values{"line": {strconv.Itoa(n)}, "find": {needle}}.Encode() + href
			}
			v.Hits = append(v.Hits, textHit{n, href})
		}
	}
	if err != nil {
		b.failErr(w, err)
		return
	}
	data["Text"], data["Stream"] = v, template.HTML(streamMark)
	var page bytes.Buffer
	if err := pages.ExecuteTemplate(&page, "doc.html", b.pageData(data)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	before, after, _ := strings.Cut(page.String(), streamMark)
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(textWrite))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	bw := bufio.NewWriterSize(w, 64<<10)
	bw.WriteString(before)
	l := &lineWriter{w: bw, n: v.First}
	done, _ := l.stream(io.NewSectionReader(f, begin, end-begin))
	next := l.n
	if l.partial {
		next--
	}
	fmt.Fprintf(bw, endMark, begin+done, next)
	bw.WriteString(after)
	_ = bw.Flush()
}

func (b *Board) follow(w http.ResponseWriter, r *http.Request, f *os.File, fi fs.FileInfo, text bool) {
	q := r.URL.Query()
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	size := fi.Size()
	from, ferr := strconv.ParseInt(q.Get("from"), 10, 64)
	line, lerr := strconv.Atoi(q.Get("line"))
	if !text || ferr != nil || lerr != nil || from < 0 || from > size || line < 1 || from > 0 && !newlineBefore(f, from) {
		h.Set("X-Hand-Reset", "1")
		w.WriteHeader(http.StatusOK)
		return
	}
	end, err := nextLine(f, min(from+window, size), size)
	var complete int64
	if err == nil {
		complete, err = lineAt(f, size)
	}
	if err != nil {
		b.failErr(w, err)
		return
	}
	if end = min(end, complete); end <= from {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(textWrite))
	h.Set("Content-Type", "text/html; charset=utf-8")
	bw := bufio.NewWriterSize(w, 64<<10)
	l := &lineWriter{w: bw, n: line}
	done, _ := l.stream(io.NewSectionReader(f, from, end-from))
	fmt.Fprintf(bw, endMark, from+done, l.n)
	_ = bw.Flush()
}

func newlineBefore(f io.ReaderAt, at int64) bool {
	var c [1]byte
	_, err := f.ReadAt(c[:], at-1)
	return err == nil && c[0] == '\n'
}
