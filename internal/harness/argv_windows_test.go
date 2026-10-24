package harness

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func TestArgvRefusesACommandLineWindowsCannotStart(t *testing.T) {
	spec, dir := Spec{"codex", "gpt-6-luna", "low"}, t.TempDir()
	bin := `C:\Users\dev\codex.exe`
	for name, build := range map[string]func(prompt string) ([]string, error){
		"worker":     func(p string) ([]string, error) { return Argv(bin, spec, p, dir) },
		"supervisor": func(p string) ([]string, error) { return SupervisorArgv(bin, spec, "", p, dir, false) },
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
