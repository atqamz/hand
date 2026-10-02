package cli

func notifyArgv(fleet, text string) (string, []string) {
	return "osascript", []string{"-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", fleet, text}
}
