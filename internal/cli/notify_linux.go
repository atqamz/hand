package cli

func notifyArgv(fleet, text string) (string, []string) {
	return "notify-send", []string{"--app-name=hand", fleet, text}
}
