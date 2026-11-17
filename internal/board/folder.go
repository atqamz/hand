package board

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const folderPage = 500

type item struct {
	name, key string
	dir, link bool
}

type entry struct {
	Name, Href, Thumb, Kind, Size, At, Refused string
	Dir, Image                                 bool
}

type pager struct {
	Text, Self, First, Prev, Next, Last string
	Pages                               int
}

type step struct{ Name, Href string }

func order(x, y item) int {
	if x.dir != y.dir {
		if x.dir {
			return -1
		}
		return 1
	}
	if c := strings.Compare(x.key, y.key); c != 0 {
		return c
	}
	return strings.Compare(x.name, y.name)
}

func readItems(f *os.File) []item {
	var items []item
	for {
		batch, err := f.ReadDir(1024)
		for _, d := range batch {
			if !strings.HasPrefix(d.Name(), ".") {
				items = append(items, item{d.Name(), strings.ToLower(d.Name()), d.IsDir(), d.Type()&fs.ModeSymlink != 0})
			}
		}
		if err != nil {
			break
		}
	}
	slices.SortFunc(items, order)
	return items
}

func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func whyRefused(err error) string {
	switch {
	case errors.Is(err, errOutside):
		return "leads outside the fleet folder"
	case errors.Is(err, errHidden):
		return "points at a file the Board never shows"
	case errors.Is(err, errKind):
		return "points at something that is not a file or folder"
	case errors.Is(err, fs.ErrNotExist):
		return "points at nothing"
	}
	return "cannot be read"
}

type member struct {
	name, kind, refused, at string
	dir, image, link        bool
	size                    int64
}

func (b *Board) members(root, real string, f *os.File, rel string, page int) (out []member, at, pages, total int) {
	items := readItems(f)
	pages = max(1, (len(items)+folderPage-1)/folderPage)
	at = min(max(page, 1), pages)
	lo, hi := (at-1)*folderPage, min(at*folderPage, len(items))
	rt, err := os.OpenRoot(root)
	if err == nil {
		defer rt.Close()
	}
	out = make([]member, 0, hi-lo)
	for _, it := range items[lo:hi] {
		m := member{name: it.name, dir: it.dir, kind: "DIR", size: -1}
		switch {
		case it.link:
			m.kind, m.link = "LINK", true
			_, _, fi, err := Resolve(b.o.Home, rel+"/"+it.name)
			if err != nil {
				m.refused = whyRefused(err)
				out = append(out, m)
				continue
			}
			m.dir = fi.IsDir()
			m.image = !m.dir && imageLike.MatchString(it.name)
		case !it.dir:
			m.kind, m.image = fileKind(it.name), imageLike.MatchString(it.name)
		}
		if rt != nil {
			if info, err := rt.Lstat(filepath.Join(real, it.name)); err == nil {
				m.at = info.ModTime().UTC().Format(time.RFC3339)
				if info.Mode().IsRegular() {
					m.size = info.Size()
				}
			}
		}
		out = append(out, m)
	}
	return out, at, pages, len(items)
}

func (b *Board) listing(root, real string, f *os.File, rel string, page int) ([]entry, *pager) {
	ms, page, pages, total := b.members(root, real, f, rel, page)
	self := docURL(b.o.Base, rel)
	var pg *pager
	if pages > 1 {
		at := func(n int) string {
			if n == 1 {
				return self
			}
			return fmt.Sprintf("%s?page=%d", self, n)
		}
		pg = &pager{Text: fmt.Sprintf("%s entries, page %d of %d", thousands(total), page, pages), Self: self, Pages: pages}
		if page > 1 {
			pg.Prev = at(page - 1)
		}
		if page > 2 {
			pg.First = at(1)
		}
		if page < pages {
			pg.Next = at(page + 1)
		}
		if page < pages-1 {
			pg.Last = at(pages)
		}
	}
	out := make([]entry, 0, len(ms))
	for _, m := range ms {
		e := entry{Name: m.name, Href: docURL(b.o.Base, rel+"/"+m.name), Dir: m.dir, Kind: m.kind, Image: m.image, At: m.at, Refused: m.refused}
		e.Thumb = e.Href
		if m.refused != "" {
			e.Href = ""
		} else if m.image {
			e.Href += "?view"
		}
		if m.size >= 0 {
			e.Size = size(m.size)
		}
		out = append(out, e)
	}
	return out, pg
}

func (b *Board) neighbours(root, rel string, images bool) (prev, next string, page int) {
	dir, name := path.Split(rel)
	d, err := os.OpenInRoot(root, filepath.FromSlash(path.Clean(dir)))
	if err != nil {
		return "", "", 1
	}
	defer d.Close()
	items := readItems(d)
	i := slices.IndexFunc(items, func(it item) bool { return it.name == name })
	if i < 0 {
		return "", "", 1
	}
	pick := func(from, by int) string {
		for j := from; j >= 0 && j < len(items); j += by {
			it := items[j]
			if it.dir || images && !imageLike.MatchString(it.name) {
				continue
			}
			if _, _, fi, err := Resolve(b.o.Home, dir+it.name); err != nil || fi.IsDir() {
				continue
			}
			return it.name
		}
		return ""
	}
	return pick(i-1, -1), pick(i+1, 1), i/folderPage + 1
}

func (b *Board) around(root, rel string, images bool, tail string, source bool) (prev, next *step, page int) {
	p, n, page := b.neighbours(root, rel, images)
	dir := path.Dir(rel) + "/"
	if dir == "./" {
		dir = ""
	}
	at := func(name string) *step {
		if name == "" {
			return nil
		}
		href := docURL(b.o.Base, dir+name)
		if imageLike.MatchString(name) {
			href += tail
		} else if source && isMarkdown(name) {
			href += "?as=text"
		}
		return &step{name, href}
	}
	return at(p), at(n), page
}
