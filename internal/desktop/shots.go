package desktop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

const shotRetry = 30 * time.Second

var shotBudget = 48 << 20

var shotDecode = drawBitmap

type shotState struct {
	bmp    *ui.Bitmap
	pix    int
	failed time.Time
	done   bool
}

func (a *App) shotRow(c *ui.Context, k kit.Colors, e Entry) {
	if len(e.Shots) == 0 {
		return
	}
	ui.Row(c).Wrap().Gap(kit.Space8).AlignItems(ui.Start).Margin(kit.Space8, 0, 0).Children(func() {
		for i, s := range e.Shots {
			key := fmt.Sprintf("shot:%s:%d", entryKey(e), i)
			label := shownText(s.Label)
			meta := s.Kind
			if s.Size != "" {
				meta += " · " + s.Size
			}
			switch {
			case s.Missing || !inboxName.MatchString(s.Name):
				kit.SentFile(c, k, key, s.Kind, label, "no longer in inbox/", true)
			case s.Image && a.shotFor(s.Name).bmp != nil:
				if kit.SentPicture(c, k, key, a.shots[s.Name].bmp, label) {
					a.OpenDoc("inbox/" + s.Name)
				}
			default:
				if st := a.shots[s.Name]; s.Image && st != nil && !st.done {
					meta = "Loading…"
				}
				if kit.SentFile(c, k, key, s.Kind, label, meta, false) {
					a.OpenDoc("inbox/" + s.Name)
				}
			}
		}
	})
}

func (a *App) shotFor(name string) *shotState {
	st := a.shots[name]
	if st == nil || st.done && st.bmp == nil && !st.failed.IsZero() && a.now().Sub(st.failed) > shotRetry {
		if a.shots == nil {
			a.shots = map[string]*shotState{}
		}
		st = &shotState{}
		a.shots[name] = st
		a.shotQueue = append(a.shotQueue, name)
		a.pumpShots()
	}
	return st
}

func (a *App) pumpShots() {
	if a.shotBusy || len(a.shotQueue) == 0 || a.Model == nil || a.Fleet.ID == "" {
		return
	}
	name := a.shotQueue[0]
	a.shotQueue = a.shotQueue[1:]
	room := a.shotRoom(name)
	if room <= 0 {
		a.shots[name].done = true
		a.pumpShots()
		return
	}
	a.shotBusy = true
	gen, fleet, model, timeout := a.shotGen, a.Fleet.ID, a.Model, a.pollTimeout()
	a.offload(func() func() {
		ctx, cancel := context.WithTimeout(a.Context, timeout)
		defer cancel()
		b, err := model.InboxBytes(ctx, fleet, name)
		var bmp *ui.Bitmap
		if err == nil {
			bmp, _ = shotDecode(b, room)
		}
		return func() {
			if gen != a.shotGen {
				return
			}
			a.shotBusy = false
			st := a.shots[name]
			st.done = true
			if w, h := ui.ImageSize(bmp); bmp != nil {
				a.holdShot(name, st, bmp, int(w*h))
			}
			if err != nil {
				st.failed = a.now()
			}
			a.pumpShots()
		}
	})
}

func (a *App) shotHeld(from string) (pix int) {
	for n, st := range a.shots {
		if shotOrder(n, from) >= 0 {
			pix += st.pix
		}
	}
	return pix
}

func shotOrder(a, b string) int {
	as, ac, aok := shotParts(a)
	bs, bc, bok := shotParts(b)
	switch {
	case aok != bok:
		if aok {
			return 1
		}
		return -1
	case aok:
		if c := strings.Compare(as, bs); c != 0 {
			return c
		}
		if len(ac) != len(bc) {
			return len(ac) - len(bc)
		}
		if c := strings.Compare(ac, bc); c != 0 {
			return c
		}
	}
	return strings.Compare(a, b)
}

func shotParts(name string) (stamp, counter string, ok bool) {
	if len(name) < 18 || name[8] != '-' || name[15] != '-' {
		return "", "", false
	}
	for i := range 15 {
		if i != 8 && (name[i] < '0' || name[i] > '9') {
			return "", "", false
		}
	}
	rest := name[16:]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 || end == len(rest) || rest[end] != '.' && rest[end] != '-' {
		return "", "", false
	}
	return name[:15], strings.TrimLeft(rest[:end], "0"), true
}

func (a *App) shotRoom(name string) int64 {
	return min(int64(shotBudget-a.shotHeld(name)), docPixelsMax)
}

func (a *App) holdShot(name string, st *shotState, bmp *ui.Bitmap, pix int) {
	for a.shotHeld("")+pix > shotBudget {
		oldest := ""
		for n, o := range a.shots {
			if o.bmp != nil && shotOrder(n, name) < 0 && (oldest == "" || shotOrder(n, oldest) < 0) {
				oldest = n
			}
		}
		if oldest == "" {
			return
		}
		a.shots[oldest].bmp, a.shots[oldest].pix = nil, 0
	}
	st.bmp, st.pix = bmp, pix
}
