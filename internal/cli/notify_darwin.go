package cli

import "context"

func notifyArgv(fleet, text string) (string, []string) {
	return "osascript", []string{"-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", fleet, text}
}

func (w *watcher) notifyClick(context.Context, string, string, string, string) bool { return false }
