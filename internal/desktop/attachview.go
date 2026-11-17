package desktop

import (
	"strconv"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

func (a *App) chipRow(c *ui.Context, k kit.Colors) {
	if len(a.Chips) == 0 {
		return
	}
	removed, retried := 0, 0
	kit.ChipRow(c, func() {
		for _, ch := range a.Chips {
			title := a.chipTitle(ch)
			tone, verb := kit.ChipReady, "Remove "
			switch ch.state {
			case chipUploading, chipChecking:
				tone = kit.ChipBusy
			case chipFailed:
				tone, verb = kit.ChipFail, "Dismiss "
			}
			remove, retry := kit.AttachChip(c, k, kit.Chip{
				Key: "chip:" + strconv.Itoa(ch.id), Badge: chipKind(title), Name: title, Meta: a.chipMeta(ch),
				Remove: verb + title, Tone: tone, Retry: ch.retry && ch.state == chipFailed,
			})
			if remove {
				removed = ch.id
			}
			if retry {
				retried = ch.id
			}
		}
	})
	if removed != 0 {
		a.RemoveChip(removed)
		a.focusComposer = true
	}
	if retried != 0 {
		a.RetryChip(retried)
	}
}
