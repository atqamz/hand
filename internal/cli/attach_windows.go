package cli

import (
	"os"
	"os/exec"

	"golang.org/x/sys/windows"
)

func stdinTerminal() bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode) == nil
}

func replaceProcess(argv, env []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
