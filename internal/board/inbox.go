package board

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/atqamz/hand/internal/inbox"
)

const (
	maxPost = 11 << 20
)

var (
	shotName  = regexp.MustCompile(`^` + inbox.StoredExpr + `$`)
	imageExt  = map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/webp": "webp", "image/gif": "gif"}
	shotTypes = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif"}
	imageLike = regexp.MustCompile(`(?i)\.(png|jpe?g|webp|gif)$`)
)

func fileKind(name string) string {
	if ext := strings.TrimPrefix(filepath.Ext(name), "."); ext != "" && len(ext) <= 4 {
		return strings.ToUpper(ext)
	}
	return "FILE"
}

func isImage(name string) bool {
	_, ok := shotTypes[filepath.Ext(name)]
	return ok
}

func (b *Board) image(w http.ResponseWriter, r *http.Request) {
	if !b.allowed(w) {
		return
	}
	f, h, err := r.FormFile("image")
	if err != nil {
		b.fail(w, http.StatusBadRequest, "attach one file in the field image")
		return
	}
	defer f.Close()
	dir := filepath.Join(b.o.Home, "inbox")
	name, err := inbox.Save(dir, b.now(), h.Filename, f)
	switch {
	case inbox.TooBig(err):
		b.fail(w, http.StatusRequestEntityTooLarge, err.Error())
	case err != nil:
		b.failErr(w, err)
	default:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"path": filepath.Join(dir, name), "name": name, "kind": attachKind(name)})
	}
}

func attachKind(name string) string {
	if isImage(name) {
		return "image"
	}
	return "file"
}

func (b *Board) inbox(w http.ResponseWriter, r *http.Request) {
	b.serveInbox(w, r, func() { http.NotFound(w, r) })
}

func (b *Board) serveInbox(w http.ResponseWriter, r *http.Request, notFound func()) {
	name := r.PathValue("name")
	if !shotName.MatchString(name) {
		notFound()
		return
	}
	f, err := os.OpenInRoot(filepath.Join(b.o.Home, "inbox"), name)
	if err != nil {
		notFound()
		return
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		notFound()
		return
	}
	if isImage(name) {
		w.Header().Set("Content-Type", shotTypes[filepath.Ext(name)])
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": label(name)}))
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, "", time.Time{}, f)
}

type attachment struct {
	Name, Label, Kind, Size, Path, Href string
	N                                   int
	Image, Missing                      bool
}

func label(name string) string { return inbox.Label(name) }

func size(n int64) string {
	switch kib := (n + 1<<9) >> 10; {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case kib < 1<<10:
		return fmt.Sprintf("%d KiB", kib)
	default:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
}

var shotMatchers sync.Map

func shotPrefix(dir string, windows bool) string {
	if !windows {
		return regexp.QuoteMeta(dir + "/")
	}
	return strings.ReplaceAll(regexp.QuoteMeta(strings.ReplaceAll(dir, `\`, "/")+"/"), "/", `[\\/]`)
}

func shotMatcher(dir string) *regexp.Regexp {
	if re, ok := shotMatchers.Load(dir); ok {
		return re.(*regexp.Regexp)
	}
	re := regexp.MustCompile(`\[(image|file)(?: ([1-9][0-9]{0,3}))?: ` + shotPrefix(dir, filepath.Separator == '\\') + `(` + inbox.StoredExpr + `)\]`)
	shotMatchers.Store(dir, re)
	return re
}

func shots(text, home, base string) (string, []attachment) {
	dir := filepath.Join(home, "inbox")
	re := shotMatcher(dir)
	lines := strings.Split(text, "\n")
	locs := make([][][]int, len(lines))
	whole := make([]bool, len(lines))
	reserved := map[int]bool{}
	for i, line := range lines {
		locs[i] = re.FindAllStringSubmatchIndex(line, -1)
		whole[i] = len(locs[i]) == 1 && strings.TrimSpace(line[:locs[i][0][0]]) == "" && strings.TrimSpace(line[locs[i][0][1]:]) == ""
		for _, m := range locs[i] {
			if m[4] >= 0 {
				n, _ := strconv.Atoi(line[m[4]:m[5]])
				reserved[n] = true
			}
		}
	}
	tail := len(lines)
	for tail > 0 && (whole[tail-1] || strings.TrimSpace(lines[tail-1]) == "") {
		tail--
	}
	byN := map[int]string{}
	var files []attachment
	number := func(given, name string) int {
		n, _ := strconv.Atoi(given)
		if n == 0 || byN[n] != "" && byN[n] != name {
			for n = 1; reserved[n] || byN[n] != ""; n++ {
			}
		}
		if byN[n] == "" {
			byN[n] = name
			files = append(files, shotFile(home, base, dir, name, n))
		}
		return n
	}
	var keep []string
	for i, line := range lines {
		if whole[i] {
			m := locs[i][0]
			given := ""
			if m[4] >= 0 {
				given = line[m[4]:m[5]]
			}
			n, _ := strconv.Atoi(given)
			if m[4] < 0 || i >= tail && byN[n] == "" {
				number(given, line[m[6]:m[7]])
				continue
			}
		}
		var out strings.Builder
		last := 0
		for _, m := range locs[i] {
			if m[4] < 0 {
				continue
			}
			n := number(line[m[4]:m[5]], line[m[6]:m[7]])
			out.WriteString(line[last:m[0]])
			fmt.Fprintf(&out, "[attachment %d]", n)
			last = m[1]
		}
		keep = append(keep, out.String()+line[last:])
	}
	return strings.Trim(strings.Join(keep, "\n"), "\n"), files
}

func shotFile(home, base, dir, name string, n int) attachment {
	path := filepath.Join(dir, name)
	a := attachment{Name: name, Label: label(name), N: n, Path: path, Image: isImage(name), Kind: fileKind(label(name)), Href: DocHref(home, base, path)}
	if fi, err := os.Lstat(path); err == nil && fi.Mode().IsRegular() && a.Href != "" {
		a.Size = size(fi.Size())
	} else {
		a.Missing = true
	}
	return a
}
