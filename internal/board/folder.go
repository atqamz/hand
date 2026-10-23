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

func (b *Board) listing(root, real string, f *os.File, rel string, page int) ([]entry, *pager) {
	items := readItems(f)
	pages := max(1, (len(items)+folderPage-1)/folderPage)
	page = min(max(page, 1), pages)
	lo, hi := (page-1)*folderPage, min(page*folderPage, len(items))
	self := docURL(b.o.Base, rel)
	var pg *pager
	if pages > 1 {
		at := func(n int) string {
			if n == 1 {
				return self
			}
			return fmt.Sprintf("%s?page=%d", self, n)
		}
		pg = &pager{Text: fmt.Sprintf("%s entries, page %d of %d", thousands(len(items)), page, pages), Self: self, Pages: pages}
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
	rt, err := os.OpenRoot(root)
	if err == nil {
		defer rt.Close()
	}
	out := make([]entry, 0, hi-lo)
	for _, it := range items[lo:hi] {
		e := entry{Name: it.name, Href: docURL(b.o.Base, rel+"/"+it.name), Dir: it.dir, Kind: "DIR"}
		e.Thumb = e.Href
		switch {
		case it.link:
			e.Kind = "LINK"
			_, _, fi, err := Resolve(b.o.Home, rel+"/"+it.name)
			if err != nil {
				e.Href, e.Refused = "", whyRefused(err)
				out = append(out, e)
				continue
			}
			e.Dir = fi.IsDir()
			e.Image = !e.Dir && imageLike.MatchString(it.name)
		case !it.dir:
			e.Kind, e.Image = fileKind(it.name), imageLike.MatchString(it.name)
		}
		if e.Image {
			e.Href += "?view"
		}
		if rt != nil {
			if info, err := rt.Lstat(filepath.Join(real, it.name)); err == nil {
				e.At = info.ModTime().UTC().Format(time.RFC3339)
				if info.Mode().IsRegular() {
					e.Size = size(info.Size())
				}
			}
		}
		out = append(out, e)
	}
	return out, pg
}

func (b *Board) around(root, rel string, images bool, tail string, source bool) (prev, next *step, page int) {
	dir, name := path.Split(rel)
	d, err := os.OpenInRoot(root, filepath.FromSlash(path.Clean(dir)))
	if err != nil {
		return nil, nil, 1
	}
	defer d.Close()
	items := readItems(d)
	i := slices.IndexFunc(items, func(it item) bool { return it.name == name })
	if i < 0 {
		return nil, nil, 1
	}
	pick := func(from, by int) *step {
		for j := from; j >= 0 && j < len(items); j += by {
			it := items[j]
			if it.dir || images && !imageLike.MatchString(it.name) {
				continue
			}
			if _, _, fi, err := Resolve(b.o.Home, dir+it.name); err != nil || fi.IsDir() {
				continue
			}
			href := docURL(b.o.Base, dir+it.name)
			if imageLike.MatchString(it.name) {
				href += tail
			} else if source && isMarkdown(it.name) {
				href += "?as=text"
			}
			return &step{it.name, href}
		}
		return nil
	}
	return pick(i-1, -1), pick(i+1, 1), i/folderPage + 1
}
