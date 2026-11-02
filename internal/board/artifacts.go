package board

import (
	"sync"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/retention"
)

const artifactsEvery = 5 * time.Minute

type artifactCache struct {
	mu   sync.Mutex
	at   time.Time
	text string
}

func (b *Board) artifactWarning() string {
	b.artifacts.mu.Lock()
	defer b.artifacts.mu.Unlock()
	if !b.artifacts.at.IsZero() && time.Since(b.artifacts.at) < artifactsEvery {
		return b.artifacts.text
	}
	b.artifacts.at, b.artifacts.text = time.Now(), ""
	p, err := harness.LoadPolicy(b.o.Home)
	if err != nil || (p.Retention.WarnSize == 0 && p.Retention.WarnDays == 0) {
		return ""
	}
	if files, err := retention.Scan(b.o.Home); err == nil {
		b.artifacts.text = retention.Warning(files, b.now(), p.Retention.WarnSize, p.Retention.WarnDays)
	}
	return b.artifacts.text
}
