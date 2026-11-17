package desktop

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

const shotRetry = 30 * time.Second

var shotBudget = 48 << 20

type shotState struct {
	bmp    *ui.Bitmap
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
	if a.shotPix >= shotBudget {
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
			bmp, _ = drawBitmap(b)
		}
		return func() {
			if gen != a.shotGen {
				return
			}
			a.shotBusy = false
			st := a.shots[name]
			st.done = true
			if w, h := ui.ImageSize(bmp); bmp != nil && a.shotPix+int(w*h) <= shotBudget {
				st.bmp = bmp
				a.shotPix += int(w * h)
			}
			if err != nil {
				st.failed = a.now()
			}
			a.pumpShots()
		}
	})
}
