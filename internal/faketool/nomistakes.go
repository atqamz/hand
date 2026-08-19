package faketool

import (
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"
)

// NoMistakes models the text and command-specific exit status hand consumes. Hang names
// subcommands (e.g. "runs") the fake never answers, for a caller's own timeout and cancellation
// paths - same shape and purpose as GH.Hang and Herdr.Hang.
type NoMistakes struct {
	Stdout     string
	Exit       int
	Status     string
	StatusExit int
	Runs       string
	RunsExit   int
	Init       string
	InitExit   int
	Hang       []string
	Log        string
	CountLog   string
}

type noMistakesSpec struct {
	Stdout     string
	Exit       int
	Status     string
	StatusExit int
	Runs       string
	RunsExit   int
	Init       string
	InitExit   int
	Hang       []string
	Log        string
	CountLog   string
}

func (n NoMistakes) Install(t *testing.T, bin string) {
	t.Helper()
	installConfig(t, bin, "no-mistakes", "no-mistakes", noMistakesSpec(n))
}

func runNoMistakesFromPayload(payload json.RawMessage, args []string) int {
	var spec noMistakesSpec
	if err := json.Unmarshal(payload, &spec); err != nil {
		return fail("decode no-mistakes config: %v", err)
	}
	if spec.Log != "" {
		if err := appendInvocation(spec.Log, "no-mistakes", args); err != nil {
			return fail("log no-mistakes invocation: %v", err)
		}
	}
	if spec.CountLog != "" {
		if err := appendRawLine(spec.CountLog, "x\n"); err != nil {
			return fail("count no-mistakes invocation: %v", err)
		}
	}
	stdout, exit := spec.Stdout, spec.Exit
	if len(args) > 0 {
		for _, blocked := range spec.Hang {
			if blocked == args[0] {
				for {
					time.Sleep(time.Hour)
				}
			}
		}
		switch args[0] {
		case "status":
			if spec.Status != "" {
				stdout, exit = spec.Status, spec.StatusExit
			}
		case "runs":
			if spec.Runs != "" {
				stdout, exit = spec.Runs, spec.RunsExit
			}
		case "init":
			if spec.Init != "" {
				stdout, exit = spec.Init, spec.InitExit
			}
		}
	}
	_, _ = io.WriteString(os.Stdout, stdout)
	return exit
}
