package board

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/atqamz/hand/internal/harness"
)

const (
	refreshEvery = 30 * time.Second
	refreshWait  = time.Minute
)

var refreshable = []string{"claude", "codex", "agy", "copilot"}

type refresher struct {
	mu      sync.Mutex
	busy    map[string]bool
	started map[string]time.Time
	done    atomic.Int64
}

func (r *refresher) begin(name string, now time.Time) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch at := r.started[name]; {
	case r.busy[name]:
		return name + " models are already refreshing"
	case !at.IsZero() && now.Sub(at) < refreshEvery:
		return fmt.Sprintf("%s models were refreshed %d s ago; try again in a moment", name, int(now.Sub(at)/time.Second))
	}
	r.busy[name], r.started[name] = true, now
	return ""
}

func (r *refresher) end(name string) {
	r.mu.Lock()
	delete(r.busy, name)
	r.mu.Unlock()
	r.done.Add(1)
}

func (b *Board) refreshModels(w http.ResponseWriter, r *http.Request) {
	if !b.allowed(w) {
		return
	}
	if s, _ := r.Context().Value(sessionKey{}).(session); s.Device != nil {
		b.fail(w, http.StatusForbidden, "a paired phone cannot refresh model lists; use the Board on your laptop")
		return
	}
	names := refreshable
	if h := r.PostFormValue("harness"); h != "" {
		if !slices.Contains(refreshable, h) {
			b.fail(w, http.StatusBadRequest, "refresh the models of claude, codex, agy or copilot")
			return
		}
		names = []string{h}
	}
	var refused string
	started := 0
	for _, name := range names {
		if why := b.refresh.begin(name, b.now()); why != "" {
			refused = why
			continue
		}
		started++
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), refreshWait)
			defer cancel()
			defer b.refresh.end(name)
			if _, err := harness.Refresh(ctx, name, b.o.Harness, b.o.Models); err != nil {
				log.Printf("board: refreshing the %s models failed: %v", name, err)
			}
		}()
	}
	if started == 0 {
		b.fail(w, http.StatusTooManyRequests, refused)
		return
	}
	b.done(w, r, b.o.Base+"/", "Refreshing models…")
}
