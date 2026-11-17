package desktop

import (
	"github.com/egoist/mygo/ui"
	"strings"
	"time"
)

func SetOpenTimeout(d time.Duration) func() {
	old := openTimeout
	openTimeout = d
	return func() { openTimeout = old }
}

func SetBoardStartTimeout(d time.Duration) func() {
	old := boardStartTimeout
	boardStartTimeout = d
	return func() { boardStartTimeout = old }
}

var LocalRoot = localRoot

func DraftNote(a *App) string { return a.draftNote() }

func NeedChosen(a *App, key string) bool { return a.needChosen[key] }

func SetLatestNew(a *App, n int) { a.latestNew = n }

func LatestNew(a *App) int { return a.latestNew }

func ViewerName(a *App) string { return a.viewerName() }

func AckDisabled(a *App, ref string) bool { return a.ackDisabled(ref) }

func ViewerScroll(a *App) (y, maxY float32) { return a.viewerScroll.Y, a.viewerScroll.MaxY }

func PageReport(a *App) string { return a.pageReport }

func TabFor(a *App) string { return a.tabFor }

func ScrollTarget(a *App) string { return a.scrollTo }

func AsideScroll(a *App) (y, maxY float32) { return a.asideScroll.Y, a.asideScroll.MaxY }

func BoardScroll(a *App) (y, maxY float32) { return a.boardScroll.Y, a.boardScroll.MaxY }

func ChatWidth(pref, total, viewport float32) float32 { return chatWidth(pref, total, viewport) }

func SheetOpen(a *App) bool { return a.sheetOpen }

func ModelsGen(a *App) int { return a.modelsGen }

func ShowReceiptKept(a *App, receipt, warn, key string) bool {
	a.showReceipt(receipt, warn, key)
	return a.receiptKey != ""
}

func StoresCursor(c *HTTPClient, path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.cursors {
		if strings.Contains(key, "\x00"+path) {
			return true
		}
	}
	return false
}

func DocShown(a *App) string {
	if a.doc.ok {
		return a.doc.doc.Path
	}
	return ""
}

func SetShotBudget(n int) func() {
	old := shotBudget
	shotBudget = n
	return func() { shotBudget = old }
}

func DrawBitmap(b []byte, most int64) (*ui.Bitmap, string) { return drawBitmap(b, most) }

func SetShotDecode(f func([]byte, int64) (*ui.Bitmap, string)) func() {
	old := shotDecode
	shotDecode = f
	return func() { shotDecode = old }
}
