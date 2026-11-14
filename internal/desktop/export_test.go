package desktop

import (
	"time"
)

func SetOpenTimeout(d time.Duration) func() {
	old := openTimeout
	openTimeout = d
	return func() { openTimeout = old }
}

var LocalRoot = localRoot

func DraftNote(a *App) string { return a.draftNote() }

func NeedChosen(a *App, key string) bool { return a.needChosen[key] }

func SetLatestNew(a *App, n int) { a.latestNew = n }

func LatestNew(a *App) int { return a.latestNew }

func ViewerName(a *App) string { return a.viewerName() }

func ViewerScroll(a *App) (y, maxY float32) { return a.viewerScroll.Y, a.viewerScroll.MaxY }

func BoardScroll(a *App) (y, maxY float32) { return a.boardScroll.Y, a.boardScroll.MaxY }
