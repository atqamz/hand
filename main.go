package main

import (
	"os"
	"time"

	"github.com/atqamz/hand/internal/cli"
)

func main() {
	exe, _ := os.Executable()
	os.Exit(cli.Run(os.Args[1:], cli.Env{Name: cli.CommandName(exe), Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv, Environ: os.Environ, Now: time.Now, Getwd: os.Getwd}))
}
