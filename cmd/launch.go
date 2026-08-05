package cmd

import (
	"fmt"
	"time"

	"github.com/atqamz/secondhand/internal/harness"
	"github.com/atqamz/secondhand/internal/herdr"
)

// Holds confirmLaunch's timings. They are a package var rather than consts so tests can shrink the
// poll window instead of sleeping through a real one.
type launchPoll struct {
	Interval   time.Duration
	QuietReads int
	Timeout    time.Duration
	KeySettle  time.Duration
	ReadLines  int
}

// QuietReads * Interval is the settle window a pane must stay dialog-free for, sized to the slowest gap
// between frames seen in testing with real claude, and Timeout is the outer bound for a pane that never
// starts or never clears a dialog.
var launchPolling = launchPoll{
	Interval:   1 * time.Second,
	QuietReads: 6,
	Timeout:    60 * time.Second,
	// Gives the harness's TUI time to re-render a focus change before the next key arrives; a multi-key
	// answer (e.g. Down, Enter) sent in one burst can land the confirm key before the focus move is
	// drawn, confirming the wrong option.
	KeySettle: 300 * time.Millisecond,
	ReadLines: 60,
}

// Waits for a freshly launched pane to hold a live harness with no first-run dialog left on it before
// the spawn/promote is reported successful.
func confirmLaunch(client *herdr.Client, paneID, harnessName string) error {
	prompts := harness.FirstRunPromptsFor(harnessName)
	deadline := time.Now().Add(launchPolling.Timeout)
	quiet := 0
	// A dialog's keys go out once per launch whatever later reads hold: a second send lands in a
	// live agent's composer, so retained text can cost a timeout but never injected keystrokes.
	answered := map[string]bool{}
	sawAgent := false
	// The gate is only as good as herdr's labeling of the harness, which is why a pane that is never
	// labeled says so by name instead of failing as a bare timeout.
	detected := harness.AgentDetectionVerified(harnessName)
	noAgent := "the harness never started in the pane"
	if !detected {
		noAgent = fmt.Sprintf("no agent detected in pane; herdr agent detection for harness %s has not been exercised", harnessName)
	}
	var stall string

	for {
		pane, err := client.PaneGet(paneID)
		if err != nil {
			return err
		}
		// Recent scrollback rather than the visible viewport: an unattached pane is too short to show a
		// whole dialog and would clip the very lines that identify it (see herdr.PaneRead).
		text, err := client.PaneRead(paneID, launchPolling.ReadLines)
		if err != nil {
			return err
		}

		sawAgent = sawAgent || pane.Agent != ""

		prompt, known := matchFirstRunPrompt(prompts.Known, text)
		switch {
		// Liveness is herdr's answer, not the screen's: herdr reports an agent on a pane only while a
		// harness process runs in it, so a harness that painted a dialog and then exited leaves its text
		// on screen but no agent, and can never be mistaken for a started worker.
		case pane.Agent == "":
			quiet = 0
			switch {
			case (known || len(answered) > 0) && (sawAgent || detected):
				stall = "the harness exited on a first-run dialog instead of starting up"
			case known:
				stall = fmt.Sprintf("%s, so the %s dialog on screen may be a harness herdr never recognized rather than one that exited", noAgent, prompt.Name)
			case sawAgent:
				stall = "the harness started and then exited"
			default:
				stall = noAgent
			}
		// A prompt catalogued as refused fails immediately with its own reason.
		case known && prompt.Refuse != "":
			return fmt.Errorf("worker is waiting on the %s prompt: %s", prompt.Name, prompt.Refuse)
		// Pane text has exactly one job here, spotting dialogs. Matching it is safe because claude erases
		// an answered first-run dialog in place rather than leaving it behind in scrollback, measured
		// against a real spawned worker pane.
		case known:
			if !answered[prompt.Name] {
				if err := answerFirstRunPrompt(client, paneID, prompt); err != nil {
					return err
				}
				answered[prompt.Name] = true
			}
			quiet = 0
			// A dialog still matching after it was answered is treated as a dialog still up: the launch
			// runs out its deadline instead of being confirmed.
			stall = fmt.Sprintf("the %s prompt was answered, its text is still in the pane, and the harness never became ready", prompt.Name)
		// The generic fallback resets the quiet count without answering, so an uncatalogued dialog fails
		// loudly rather than being confirmed.
		case prompts.Unrecognized != nil && prompts.Unrecognized.MatchString(text):
			quiet = 0
			stall = "the pane is showing a dialog no known signature covers"
		// A cheap secondary signal: with the agent already confirmed present, the harness's own paint
		// leaves the quiet window nothing to wait for.
		case prompts.Ready != nil && prompts.Ready.MatchString(text):
			return nil
		default:
			quiet++
			if quiet >= launchPolling.QuietReads {
				return nil
			}
			stall = "the harness started but its pane never went quiet"
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("not confirmed started within %s: %s:\n%s", launchPolling.Timeout, stall, text)
		}
		time.Sleep(launchPolling.Interval)
	}
}

// Reports which catalogued dialog the pane read is showing. A refused prompt wins over an answerable
// one wherever either sits in the catalogue, so a read carrying both signatures is never answered into.
func matchFirstRunPrompt(known []harness.FirstRunPrompt, text string) (harness.FirstRunPrompt, bool) {
	var match harness.FirstRunPrompt
	found := false
	for _, prompt := range known {
		if !prompt.Match.MatchString(text) {
			continue
		}
		if prompt.Refuse != "" {
			return prompt, true
		}
		// Answerable entries are taken in catalogue order, which for claude is the order it paints them in.
		if !found {
			match, found = prompt, true
		}
	}
	return match, found
}

func answerFirstRunPrompt(client *herdr.Client, paneID string, prompt harness.FirstRunPrompt) error {
	for _, key := range prompt.Keys {
		if err := client.PaneSendKeys(paneID, key); err != nil {
			return fmt.Errorf("answer %s prompt: %w", prompt.Name, err)
		}
		time.Sleep(launchPolling.KeySettle)
	}
	return nil
}
