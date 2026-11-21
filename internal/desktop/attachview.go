package desktop

import (
	"fmt"
	"strconv"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

func (a *App) chipRow(c *ui.Context, k kit.Colors, message ui.Element) {
	if len(a.Chips) == 0 {
		return
	}
	removed, retried, inserted := 0, 0, 0
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
			chip := kit.Chip{
				Key: "chip:" + strconv.Itoa(ch.id), Badge: chipKind(title), Name: title, Meta: a.chipMeta(ch),
				Remove: verb + title, Tone: tone, Retry: ch.retry && ch.state == chipFailed,
			}
			if n := a.chipNumber(ch.id); n > 0 {
				chip.Insert = fmt.Sprintf("Insert [attachment %d]", n)
			}
			remove, retry, insert := kit.AttachChip(c, k, chip)
			if insert {
				inserted = ch.id
			}
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
	if n := a.chipNumber(inserted); n > 0 {
		start, end := message.TextSelection()
		if !message.Focused() {
			start = len([]rune(a.Draft))
			end = start
		}
		var caret int
		a.Draft, caret = insertToken(a.Draft, start, end, n)
		message.SetTextSelection(caret, caret)
		message.Focus()
		a.saveDraft()
	}
}
