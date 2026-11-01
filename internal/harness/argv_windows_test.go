package harness

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func TestArgvRefusesACommandLineWindowsCannotStart(t *testing.T) {
	dir := t.TempDir()
	for name, build := range map[string]func(prompt string) ([]string, error){
		"codex worker": func(p string) ([]string, error) {
			return Argv(`C:\Users\dev\codex.exe`, Spec{"codex", "gpt-6-luna", "low"}, p, dir)
		},
		"codex supervisor": func(p string) ([]string, error) {
			return SupervisorArgv(`C:\Users\dev\codex.exe`, Spec{"codex", "gpt-6-luna", "low"}, "", p, dir, false)
		},
		"claude worker": func(p string) ([]string, error) {
			return Argv(`C:\Users\dev\claude.exe`, Spec{"claude", "sonnet", "low"}, p, dir)
		},
	} {
		if _, err := build(strings.Repeat(`"`, 1000)); err != nil {
			t.Fatalf("%s: a prompt of 1000 quotes: %v", name, err)
		}
		_, err := build(strings.Repeat(`"`, MaxPromptBytes))
		if !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), strconv.Itoa(MaxWindowsCommandLine)) {
			t.Fatalf("%s: a prompt of %d quotes: err = %v", name, MaxPromptBytes, err)
		}
	}
}
