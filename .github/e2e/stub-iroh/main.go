package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var version = "0.0.0"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("hand-iroh " + version + " iroh 0.0.0-stub")
		return
	}
	dir := ""
	for i, a := range os.Args {
		if a == "--key-dir" && i+1 < len(os.Args) {
			dir = os.Args[i+1]
		}
	}
	if dir == "" {
		fmt.Fprintln(os.Stderr, "hand-iroh stub: --key-dir is required")
		os.Exit(2)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "hand-iroh stub:", err)
		os.Exit(1)
	}
	env := strings.Join(os.Environ(), "\n")
	_ = os.WriteFile(filepath.Join(dir, "stub.args"), []byte(strings.Join(os.Args[1:], "\n")+"\n"+env), 0o600)
	fmt.Println("ready id=" + strings.Repeat("ab", 32) + " relay=https://relay.example.test./")
	_, _ = io.Copy(io.Discard, os.Stdin)
	_ = os.WriteFile(filepath.Join(dir, "stub.stopped"), nil, 0o600)
}
