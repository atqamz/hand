package main

import (
	"os"
	"time"

	"github.com/atqamz/hand/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.Env{Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv, Environ: os.Environ, Now: time.Now, Getwd: os.Getwd}))
}
