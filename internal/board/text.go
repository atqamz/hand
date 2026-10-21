package board

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	window     = 4 << 20
	maxWhole   = 8 << 20
	maxLine    = 256 << 10
	headBytes  = 8 << 10
	chunkLines = 500
	maxHits    = 200
	maxMarks   = 20000
	maxNeedle  = 200
	writeSpan  = 5 * time.Minute
)

var sgr = map[int]string{30: "k", 31: "r", 32: "g", 33: "y", 34: "b", 35: "m", 36: "c", 37: "w", 90: "k", 91: "r", 92: "g", 93: "y", 94: "b", 95: "m", 96: "c", 97: "w"}

type lineWriter struct {
	w        *bufio.Writer
	n        int
	from     int
	chunk    int
	gut      []byte
	color    string
	bold     bool
	open     bool
	done     int64
	partial  bool
	onlyDone bool
	diff     bool
	marks    int
}

func textHead(head []byte) bool {
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	for i := 0; i < utf8.UTFMax-1 && len(head) > 0 && !utf8.Valid(head); i++ {
		head = head[:len(head)-1]
	}
	return utf8.Valid(head)
}

func diffClass(b []byte) string {
	for _, p := range []string{"diff ", "index ", "+++ ", "--- "} {
		if bytes.HasPrefix(b, []byte(p)) {
			return "d-file"
		}
	}
	switch {
	case bytes.HasPrefix(b, []byte("@@")):
		return "d-hunk"
	case bytes.HasPrefix(b, []byte("+")):
		return "d-add"
	case bytes.HasPrefix(b, []byte("-")):
		return "d-del"
	}
	return ""
}

func isDiff(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == ".diff" || ext == ".patch"
}

func (lw *lineWriter) line(b []byte, cut int, partial bool) {
	if lw.chunk == 0 {
		lw.from = lw.n
		fmt.Fprintf(lw.w, `<div class="chunk" data-from="%d"><pre class="src">`, lw.n)
	}
	var class string
	if lw.diff {
		class = diffClass(b)
	}
	if class != "" {
		fmt.Fprintf(lw.w, `<span class="%s">`, class)
	}
	if partial {
		lw.w.WriteString(`<span class="partial">`)
	}
	lw.text(b)
	lw.shut()
	if cut > 0 {
		fmt.Fprintf(lw.w, `<span class="cut">… %s more on this line</span>`, size(int64(cut)))
	}
	if partial {
		lw.w.WriteString("</span>")
	} else {
		lw.w.WriteByte('\n')
	}
	if class != "" {
		lw.w.WriteString("</span>")
	}
	lw.gut = strconv.AppendInt(lw.gut, int64(lw.n), 10)
	lw.gut = append(lw.gut, '\n')
	lw.n++
	if lw.chunk++; lw.chunk == chunkLines {
		lw.close()
	}
}

func (lw *lineWriter) close() {
	if lw.chunk == 0 {
		return
	}
	lw.w.WriteString(`</pre><pre class="gut" aria-hidden="true">`)
	lw.w.Write(lw.gut)
	lw.w.WriteString("</pre></div>")
	lw.gut = lw.gut[:0]
	lw.chunk = 0
}

func (lw *lineWriter) shut() {
	if lw.open {
		lw.w.WriteString("</span>")
		lw.open = false
	}
}

func (lw *lineWriter) paint() {
	if lw.open || lw.color == "" && !lw.bold {
		return
	}
	lw.open = true
	cls := "x-" + lw.color
	if lw.color == "" {
		cls = "x-bold"
	} else if lw.bold {
		cls += " x-bold"
	}
	fmt.Fprintf(lw.w, `<span class="%s">`, cls)
}

func (lw *lineWriter) setSGR(params string) {
	lw.shut()
	ps := strings.Split(params, ";")
	for i := 0; i < len(ps); i++ {
		p := ps[i]
		v, err := strconv.Atoi(p)
		switch {
		case strings.Contains(p, ":") || p != "" && err != nil:
		case p == "" || v == 0:
			lw.color, lw.bold = "", false
		case v == 1:
			lw.bold = true
		case v == 22:
			lw.bold = false
		case v == 39:
			lw.color = ""
		case v == 38 || v == 48 || v == 58:
			if i+1 < len(ps) {
				switch ps[i+1] {
				case "5":
					i += 2
				case "2":
					i += 4
				}
			}
		case sgr[v] != "":
			lw.color = sgr[v]
		}
	}
}

func plainByte(c byte) bool {
	return c >= 0x20 && c < 0x7f && c != '<' && c != '>' && c != '&' && c != '"' && c != '\''
}

func (lw *lineWriter) ctl(r rune, title string) {
	marked := lw.marks < maxMarks
	lw.marks++
	if marked {
		fmt.Fprintf(lw.w, `<span class="ctl" title="%s">`, title)
	}
	switch {
	case r < 0x20:
		lw.w.WriteRune(0x2400 + r)
	case r == 0x7f:
		lw.w.WriteRune(0x2421)
	default:
		fmt.Fprintf(lw.w, "U+%04X", r)
	}
	if marked {
		lw.w.WriteString("</span>")
	}
}

func (lw *lineWriter) text(b []byte) {
	if i := bytes.LastIndexByte(b, '\r'); i >= 0 {
		lw.track(b[:i])
		b = b[i+1:]
	}
	for len(b) > 0 {
		c := b[0]
		if c != 0x1b {
			lw.paint()
		}
		switch {
		case c == 0x1b:
			b = lw.escape(b)
		case c < 0x20 && c != '\t' || c == 0x7f:
			lw.ctl(rune(c), fmt.Sprintf("control character 0x%02X", c))
			b = b[1:]
		case c < utf8.RuneSelf:
			i := 0
			for i < len(b) && plainByte(b[i]) {
				i++
			}
			if i == 0 {
				lw.w.WriteString(html.EscapeString(string(b[:1])))
				i = 1
			} else {
				lw.w.Write(b[:i])
			}
			b = b[i:]
		default:
			r, n := utf8.DecodeRune(b)
			switch {
			case r == utf8.RuneError && n == 1:
				lw.w.WriteString("�")
			case r >= 0x80 && r <= 0x9f:
				lw.ctl(r, fmt.Sprintf("control character U+%04X", r))
			case bidi(r):
				lw.ctl(r, fmt.Sprintf("bidirectional control U+%04X", r))
			default:
				lw.w.Write(b[:n])
			}
			b = b[n:]
		}
	}
}

func bidi(r rune) bool {
	return r >= 0x202A && r <= 0x202E || r >= 0x2066 && r <= 0x2069 || r == 0x200E || r == 0x200F || r == 0x061C
}

func (lw *lineWriter) track(b []byte) {
	for len(b) > 0 {
		i := bytes.IndexByte(b, 0x1b)
		if i < 0 {
			return
		}
		b = lw.escape(b[i:])
	}
}

func (lw *lineWriter) escape(b []byte) []byte {
	if len(b) < 2 {
		return nil
	}
	switch b[1] {
	case '[':
		i := 2
		for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
			i++
		}
		if i < len(b) && b[i] == 'm' {
			lw.setSGR(string(b[2:i]))
		}
		return b[min(i+1, len(b)):]
	case ']':
		for i := 2; i < len(b); i++ {
			if b[i] == 0x07 {
				return b[i+1:]
			}
			if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\' {
				return b[i+2:]
			}
		}
		return nil
	case '(', ')', '*', '+', '#', '%':
		return b[min(3, len(b)):]
	}
	_, n := utf8.DecodeRune(b[1:])
	return b[1+n:]
}

type rawLine struct {
	text    []byte
	cut     int
	read    int
	ended   bool
	hasData bool
}

func readLine(br *bufio.Reader, keep []byte) (rawLine, error) {
	l := rawLine{text: keep[:0]}
	for {
		part, err := br.ReadSlice('\n')
		l.read += len(part)
		take := min(len(part), max(maxLine-len(l.text), 0))
		l.text = append(l.text, part[:take]...)
		l.cut += len(part) - take
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err == nil {
			l.ended = true
			if l.cut > 0 {
				l.cut--
			}
			l.text = bytes.TrimSuffix(l.text, []byte("\n"))
		}
		l.hasData = l.read > 0
		l.text = bytes.TrimRight(l.text, "\r")
		return l, err
	}
}

func (lw *lineWriter) stream(r io.Reader, limit int64) (int64, error) {
	br := bufio.NewReaderSize(io.LimitReader(r, limit), 64<<10)
	var buf []byte
	var used int64
	for {
		l, err := readLine(br, buf)
		buf = l.text
		if l.hasData {
			used += int64(l.read)
			if lw.partial = !l.ended; !lw.partial {
				lw.done = used
			}
			if l.ended || !lw.onlyDone {
				lw.line(l.text, l.cut, !l.ended)
			}
		}
		if err != nil {
			lw.close()
			if errors.Is(err, io.EOF) {
				return used, nil
			}
			return used, err
		}
	}
}

func containsFold(line, lower []byte) bool {
	if bytes.ContainsFunc(lower, func(r rune) bool { return r >= utf8.RuneSelf }) {
		return bytes.Contains(bytes.ToLower(line), lower)
	}
	for i := 0; i+len(lower) <= len(line); i++ {
		j := 0
		for ; j < len(lower); j++ {
			c := line[i+j]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != lower[j] {
				break
			}
		}
		if j == len(lower) {
			return true
		}
	}
	return false
}

func hitLines(ctx context.Context, r io.Reader, needle []byte) []hit {
	br := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	var hits []hit
	for n := 1; len(hits) < maxHits; n++ {
		l, err := readLine(br, buf)
		buf = l.text
		if l.hasData && containsFold(l.text, needle) {
			hits = append(hits, hit{Line: n})
		}
		if err != nil || n%4096 == 0 && ctx.Err() != nil {
			break
		}
	}
	return hits
}

func countLines(ctx context.Context, f io.ReaderAt, upto int64) (int, error) {
	buf := make([]byte, 1<<20)
	n := 0
	for at := int64(0); at < upto; {
		k, err := f.ReadAt(buf[:min(int64(len(buf)), upto-at)], at)
		n += bytes.Count(buf[:k], []byte("\n"))
		at += int64(k)
		if err != nil && !errors.Is(err, io.EOF) {
			return n, err
		}
		if k == 0 || ctx.Err() != nil {
			return n, ctx.Err()
		}
	}
	return n, nil
}

func lineStart(ctx context.Context, f io.ReaderAt, size int64, line int) (int64, bool) {
	buf := make([]byte, 1<<20)
	need := line - 1
	for at := int64(0); need > 0 && at < size; {
		k, _ := f.ReadAt(buf[:min(int64(len(buf)), size-at)], at)
		for i := 0; ; {
			j := bytes.IndexByte(buf[i:k], '\n')
			if j < 0 {
				break
			}
			i += j + 1
			if need--; need == 0 {
				return at + int64(i), at+int64(i) < size
			}
		}
		if at += int64(k); k == 0 || ctx.Err() != nil {
			break
		}
	}
	return 0, line <= 1
}

func lineAfter(f io.ReaderAt, at, size int64) int64 {
	if at <= 0 || at >= size {
		return min(max(at, 0), size)
	}
	buf := make([]byte, min(maxLine, size-at+1))
	k, _ := f.ReadAt(buf, at-1)
	if i := bytes.IndexByte(buf[:k], '\n'); i >= 0 {
		return at + int64(i)
	}
	return at
}

type hit struct {
	Line int
	Href string
}

type textView struct {
	Whole, Follow, Empty, Plain bool
	First                       int
	SizeText, Find              string
	Start, Earlier, Later, End  string
	Hits                        []hit
	Capped                      bool
}

func (b *Board) textPage(w http.ResponseWriter, r *http.Request, f *os.File, fi fs.FileInfo, data map[string]any, pretty []byte) {
	ctx := r.Context()
	q := r.URL.Query()
	self := data["Self"].(string)
	var src io.ReaderAt = f
	total := fi.Size()
	if pretty != nil {
		src, total = bytes.NewReader(pretty), int64(len(pretty))
	}
	v := &textView{First: 1, SizeText: size(total), Whole: total <= maxWhole, Empty: total == 0, End: "#text-end", Plain: q.Get("as") == "text"}
	href := func(kv string) string {
		switch {
		case v.Plain && kv != "":
			return self + "?as=text&" + kv
		case v.Plain:
			return self + "?as=text"
		case kv != "":
			return self + "?" + kv
		}
		return self
	}
	var at, end int64
	end = total
	if !v.Whole {
		at = total - window
		if q.Has("line") {
			n, _ := strconv.Atoi(q.Get("line"))
			if start, ok := lineStart(ctx, src, total, n); ok {
				at = start
			}
		} else if q.Has("at") {
			at, _ = strconv.ParseInt(q.Get("at"), 10, 64)
		}
		at = lineAfter(src, min(max(at, 0), total), total)
		if end = at + window; end < total {
			end = lineAfter(src, end, total)
		} else {
			end = total
		}
		n, err := countLines(ctx, src, at)
		if err != nil {
			return
		}
		v.First = n + 1
		if at > 0 {
			v.Start = href("at=0")
			v.Earlier = href(fmt.Sprintf("at=%d", max(at-window, 0)))
		}
		if end < total {
			v.Later = href(fmt.Sprintf("at=%d", end))
			v.End = href("")
		}
	}
	v.Follow = end == total && pretty == nil
	if needle := strings.TrimSpace(q.Get("find")); needle != "" {
		needle = needle[:min(len(needle), maxNeedle)]
		v.Find = needle
		v.Hits = hitLines(ctx, io.NewSectionReader(src, 0, total), bytes.ToLower([]byte(needle)))
		v.Capped = len(v.Hits) == maxHits
		for i := range v.Hits {
			v.Hits[i].Href = fmt.Sprintf("#L%d", v.Hits[i].Line)
			if !v.Whole {
				v.Hits[i].Href = fmt.Sprintf("%s#L%d", href(fmt.Sprintf("line=%d", v.Hits[i].Line)), v.Hits[i].Line)
			}
		}
	}
	if ctx.Err() != nil {
		return
	}
	data["Text"] = v
	data["Stream"] = streamMarker
	var page bytes.Buffer
	if err := pages.ExecuteTemplate(&page, "doc.html", b.pageData(data)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	before, after, _ := strings.Cut(page.String(), streamMarker)
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(writeSpan))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	bw := bufio.NewWriterSize(w, 64<<10)
	bw.WriteString(before)
	lw := &lineWriter{w: bw, n: v.First, diff: isDiff(data["Name"].(string))}
	_, _ = lw.stream(io.NewSectionReader(src, at, end-at), end-at)
	next := lw.n
	if lw.partial {
		next--
	}
	fmt.Fprintf(bw, `<p class="text-end" data-next="%d" data-line="%d" data-partial="%t" hidden></p>`, at+lw.done, next, lw.partial)
	bw.WriteString(after)
	_ = bw.Flush()
}

func completeLines(r io.Reader) (done int64, lines int) {
	buf := make([]byte, 64<<10)
	var read int64
	for {
		k, err := r.Read(buf)
		if i := bytes.LastIndexByte(buf[:k], '\n'); i >= 0 {
			done = read + int64(i) + 1
		}
		lines += bytes.Count(buf[:k], []byte("\n"))
		read += int64(k)
		if err != nil {
			break
		}
	}
	return done, lines
}

func (b *Board) follow(w http.ResponseWriter, r *http.Request, f *os.File, fi fs.FileInfo) {
	q := r.URL.Query()
	from, _ := strconv.ParseInt(q.Get("from"), 10, 64)
	line, _ := strconv.Atoi(q.Get("line"))
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", "text/html; charset=utf-8")
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(writeSpan))
	var prev [1]byte
	if from < 0 || from > fi.Size() || line < 1 || from > 0 && (readByte(f, from-1, prev[:]) != '\n') {
		h.Set("X-Hand-Reset", "1")
		return
	}
	span := min(fi.Size()-from, window)
	done, lines := completeLines(io.NewSectionReader(f, from, span))
	h.Set("X-Hand-Next", strconv.FormatInt(from+done, 10))
	h.Set("X-Hand-Line", strconv.Itoa(line+lines))
	if done == 0 {
		return
	}
	bw := bufio.NewWriterSize(w, 64<<10)
	lw := &lineWriter{w: bw, n: line, onlyDone: true, diff: isDiff(r.PathValue("path"))}
	_, _ = lw.stream(io.NewSectionReader(f, from, done), done)
	_ = bw.Flush()
}

func readByte(f *os.File, at int64, buf []byte) byte {
	if k, _ := f.ReadAt(buf[:1], at); k == 1 {
		return buf[0]
	}
	return 0
}

const streamMarker = "@@HAND-TEXT-STREAM@@"
