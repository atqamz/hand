package main

import (
	"os"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/update"
)

func main() {
	exe, _ := os.Executable()
	update.CleanOld(exe)
	os.Exit(cli.Run(os.Args[1:], cli.Env{Name: cli.CommandName(exe), Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: cli.HostGetenv, Environ: os.Environ, Now: time.Now, Getwd: os.Getwd}))
}
