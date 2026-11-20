package desktop

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"time"
)

const (
	pathMax      = 50
	pathValueMax = 512
	pathQueryMax = 16 << 10
	pathTTL      = 30 * time.Second
	pathMemoMax  = 4096
	pathQueueMax = 500
)

type pathKey struct{ fleet, cand string }

type pathAnswer struct {
	rel string
	at  time.Time
}

type pathFeat struct {
	links bool
	at    time.Time
}

func (a *App) pathRel(fleet string) func(string) string {
	m := a.pathMemo[fleet]
	return func(c string) string { return m[c].rel }
}

func (a *App) askPaths(fleet string, cands []string) {
	if a.Model == nil || fleet == "" || fleet != a.Fleet.ID || len(cands) == 0 {
		return
	}
	now := a.now()
	for _, c := range cands {
		if st, ok := a.pathMemo[fleet][c]; ok && (st.rel != "" || now.Sub(st.at) <= pathTTL) {
			continue
		}
		k := pathKey{fleet, c}
		if a.pathQueued[k] || len(a.pathQueue) >= pathQueueMax {
			continue
		}
		if a.pathQueued == nil {
			a.pathQueued = map[pathKey]bool{}
		}
		a.pathQueued[k] = true
		a.pathQueue = append(a.pathQueue, k)
	}
	a.pumpPaths()
}

func (a *App) pumpPaths() {
	if a.pathBusy || len(a.pathQueue) == 0 || a.Model == nil {
		return
	}
	fleet := a.pathQueue[0].fleet
	var chunk []string
	var rest []pathKey
	size := 0
	for _, k := range a.pathQueue {
		n := len("path=") + len(url.QueryEscape(k.cand)) + 1
		if k.fleet != fleet || len(chunk) >= pathMax || size+n > pathQueryMax {
			rest = append(rest, k)
			continue
		}
		chunk, size = append(chunk, k.cand), size+n
	}
	a.pathQueue = rest
	feat, haveFeat := a.pathFeat[fleet]
	haveFeat = haveFeat && a.now().Sub(feat.at) <= pathTTL
	a.pathBusy = true
	model, base, timeout := a.Model, a.Context, a.pollTimeout()
	a.offload(func() func() {
		ctx, cancel := context.WithTimeout(base, timeout)
		defer cancel()
		links, fetched := feat.links, false
		var res []*Link
		var err error
		if !haveFeat {
			var fs []string
			if fs, err = model.Features(ctx, fleet); err == nil {
				links, fetched = slices.Contains(fs, "links"), true
			}
		}
		if err == nil && links {
			if res, err = model.Links(ctx, fleet, chunk); err == nil && len(res) != len(chunk) {
				err = fmt.Errorf("the board answered %d links for %d paths", len(res), len(chunk))
			}
		}
		return func() {
			at := a.now()
			a.pathBusy = false
			if fetched {
				if a.pathFeat == nil {
					a.pathFeat = map[string]pathFeat{}
				}
				a.pathFeat[fleet] = pathFeat{links, at}
			}
			changed := false
			for i, c := range chunk {
				delete(a.pathQueued, pathKey{fleet, c})
				rel := ""
				if err == nil && links && res[i] != nil {
					rel = res[i].Rel
				}
				changed = a.setPath(fleet, c, rel, at) || changed
			}
			if changed {
				a.pathGen++
			}
			a.pumpPaths()
		}
	})
}

func (a *App) setPath(fleet, cand, rel string, at time.Time) bool {
	cleared := false
	if a.pathCount >= pathMemoMax {
		a.pathMemo, a.pathCount, cleared = nil, 0, true
	}
	if a.pathMemo == nil {
		a.pathMemo = map[string]map[string]pathAnswer{}
	}
	m := a.pathMemo[fleet]
	if m == nil {
		m = map[string]pathAnswer{}
		a.pathMemo[fleet] = m
	}
	old, had := m[cand]
	if !had {
		a.pathCount++
	}
	m[cand] = pathAnswer{rel, at}
	return cleared || old.rel != rel
}
