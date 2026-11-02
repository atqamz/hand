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
	cmd  string
}

func (b *Board) artifactWarning() (string, string) {
	b.artifacts.mu.Lock()
	defer b.artifacts.mu.Unlock()
	if !b.artifacts.at.IsZero() && time.Since(b.artifacts.at) < artifactsEvery {
		return b.artifacts.text, b.artifacts.cmd
	}
	b.artifacts.at, b.artifacts.text, b.artifacts.cmd = time.Now(), "", ""
	p, err := harness.LoadPolicy(b.o.Home)
	if err != nil || (p.Retention.WarnSize == 0 && p.Retention.WarnDays == 0) {
		return "", ""
	}
	b.artifacts.cmd = retention.GcCommand(p.Retention.WarnDays)
	if files, err := retention.Scan(b.o.Home); err != nil {
		b.artifacts.text = "Could not check reports/ and shots/: " + clip(err.Error(), 160)
	} else {
		b.artifacts.text = retention.Warning(files, b.now(), p.Retention.WarnSize, p.Retention.WarnDays)
	}
	return b.artifacts.text, b.artifacts.cmd
}
