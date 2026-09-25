//go:build linux

package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type doorbellTerminal struct {
	mu     sync.Mutex
	output strings.Builder
}

func (d *doorbellTerminal) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.output.Write(p)
}

// Answers each cursor-position query, as the pane's terminal would, since PSReadLine waits for one.
func (d *doorbellTerminal) serve(master *os.File) {
	buffer, answered := make([]byte, 4096), 0
	for {
		n, err := master.Read(buffer)
		_, _ = d.Write(buffer[:n])
		for queries := strings.Count(d.String(), "\x1b[6n"); answered < queries; answered++ {
			_, _ = master.WriteString("\x1b[1;1R")
		}
		if err != nil {
			return
		}
	}
}

func (d *doorbellTerminal) String() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.output.String()
}

func TestCanonicalV19HerdrWorkerWakeDoorbellIsInertInInteractiveShells(t *testing.T) {
	words := strings.Fields(strings.ReplaceAll(canonicalV19HerdrWorkerWakeDoorbell, "|", " "))
	for _, shell := range []struct {
		name, parseError, prompt, nearMiss string
		args                               []string
	}{
		{"sh", "syntax error", "", "", []string{"-i"}},
		{"bash", "syntax error", "", "", []string{"--norc", "--noprofile", "-i"}},
		{"dash", "syntax error", "", "", []string{"-i"}},
		{"zsh", "parse error", "", "hanf", []string{"-f", "-o", "correct", "-i"}},
		{"pwsh", "empty pipe element", "PS ", "", []string{"-NoLogo", "-NoProfile"}},
	} {
		t.Run(shell.name, func(t *testing.T) {
			path, err := exec.LookPath(shell.name)
			if err != nil {
				t.Skipf("%s is not installed, so the doorbell is unchecked under it", shell.name)
			}
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			stubs := words
			if shell.nearMiss != "" {
				stubs = append(slices.DeleteFunc(slices.Clone(words), func(word string) bool { return word == "hand" }), shell.nearMiss)
			}
			for _, word := range stubs {
				if err := os.WriteFile(filepath.Join(bin, word), []byte("#!/bin/sh\necho \"$0\" >> "+filepath.Join(dir, "ran")+"\n"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			master, tty := openPTY(t)
			if err := unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 50, Col: 400}); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(path, shell.args...)
			cmd.Dir, cmd.Env = dir, []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + dir, "TERM=dumb", "POWERSHELL_TELEMETRY_OPTOUT=1"}
			onTerminal(cmd, tty)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			_ = tty.Close()
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			t.Cleanup(func() { _ = cmd.Process.Kill(); <-exited })
			terminal := &doorbellTerminal{}
			go terminal.serve(master)
			await := func(what string, ready func() bool) {
				t.Helper()
				for deadline := time.Now().Add(30 * time.Second); !ready(); time.Sleep(20 * time.Millisecond) {
					if time.Now().After(deadline) {
						t.Fatalf("%s: timed out waiting for %s; terminal:\n%s", shell.name, what, terminal)
					}
				}
			}
			typed := 0
			typeLine := func(line string) {
				t.Helper()
				settled, idleSince := "", time.Now()
				await("an idle prompt", func() bool {
					if output := terminal.String(); output != settled {
						settled, idleSince = output, time.Now()
					}
					return settled != "" && strings.Count(settled, shell.prompt) > typed && time.Since(idleSince) > 300*time.Millisecond
				})
				typed++
				if _, err := master.WriteString(line + "\r"); err != nil {
					t.Fatal(err)
				}
			}
			typeLine(canonicalV19HerdrWorkerWakeDoorbell)
			await("a parse error", func() bool { return strings.Contains(strings.ToLower(terminal.String()), shell.parseError) })
			typeLine("echo next > next")
			await("the next line to run", func() bool { _, err := os.Stat(filepath.Join(dir, "next")); return err == nil })
			if _, err := os.Stat(filepath.Join(dir, "ran")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("%s ran a doorbell word (%v), want a parse error that executes nothing (EG-14); terminal:\n%s", shell.name, err, terminal)
			}
			if shell.nearMiss == "" {
				return
			}
			if strings.Contains(terminal.String(), "[nyae]") {
				t.Fatalf("%s offered a spelling correction for the doorbell; terminal:\n%s", shell.name, terminal)
			}
			typeLine("hand")
			await("CORRECT to offer "+shell.nearMiss+" for a bare hand, proving the check above could fail", func() bool {
				return strings.Contains(terminal.String(), "'"+shell.nearMiss+"' [nyae]")
			})
		})
	}
}

func openPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("no pseudo-terminal multiplexer")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close() })
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	index, err := unix.IoctlGetUint32(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	tty, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", index), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tty.Close() })
	return master, tty
}

func onTerminal(cmd *exec.Cmd, tty *os.File) {
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Pdeathsig: syscall.SIGKILL}
}
