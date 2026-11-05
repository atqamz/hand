package cli

import "context"

func notifyArgv(string, string) (string, []string) {
	return "", nil
}

func (w *watcher) notifyClick(context.Context, string, string, string, string) bool { return false }
