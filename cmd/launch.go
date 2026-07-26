package cmd

import (
	"fmt"
	"time"

	"github.com/atqamz/secondhand/internal/harness"
	"github.com/atqamz/secondhand/internal/herdr"
)

// confirmLaunch waits for a freshly launched pane to clear any first-run dialog before
// reporting the spawn/promote as successful. herdr's agent_status reports "idle" the instant
// the harness process starts, even while it's sitting unanswered on a dialog, so it cannot
// distinguish "stuck" from "done" - only the pane's own text can. A pane is judged started once
// it goes quiet: launchQuietReads consecutive reads with neither a known prompt nor the
// harness's generic unrecognized-dialog fallback matching. Any known prompt is answered and
// resets the quiet count; an unrecognized match also resets it without being answered, so a
// dialog no one has catalogued yet still eventually fails loudly instead of being reported as a
// successful launch. launchQuietReads * launchPollInterval (5s) covers the slowest first paint
// seen in testing with real claude on a cold start, and launchTimeout (60s) is the outer bound
// for a pane stuck repeating the same prompt.
const (
	launchPollInterval = 1 * time.Second
	launchQuietReads   = 6
	launchTimeout      = 60 * time.Second
	launchReadLines    = 60
	// launchKeySettle gives the harness's TUI time to re-render a focus change before the next
	// key arrives; sending a multi-key answer (e.g. Down, Enter) in one burst can land the
	// confirm key before the focus move is drawn, confirming the wrong option.
	launchKeySettle = 300 * time.Millisecond
)

func confirmLaunch(client *herdr.Client, paneID, harnessName string) error {
	prompts := harness.FirstRunPromptsFor(harnessName)
	deadline := time.Now().Add(launchTimeout)
	quiet := 0

	for {
		text, err := client.PaneRead(paneID, launchReadLines)
		if err != nil {
			return err
		}

		answered, err := answerKnownPrompt(client, paneID, prompts, text)
		if err != nil {
			return err
		}

		switch {
		case answered:
			quiet = 0
		case prompts.Unrecognized != nil && prompts.Unrecognized.MatchString(text):
			quiet = 0
		default:
			quiet++
			if quiet >= launchQuietReads {
				return nil
			}
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("worker still on an unrecognized prompt after %s:\n%s", launchTimeout, text)
		}
		time.Sleep(launchPollInterval)
	}
}

func answerKnownPrompt(client *herdr.Client, paneID string, prompts harness.FirstRunPrompts, text string) (bool, error) {
	for _, prompt := range prompts.Known {
		if !prompt.Match.MatchString(text) {
			continue
		}
		for _, key := range prompt.Keys {
			if err := client.PaneSendKeys(paneID, key); err != nil {
				return true, fmt.Errorf("answer %s prompt: %w", prompt.Name, err)
			}
			time.Sleep(launchKeySettle)
		}
		return true, nil
	}
	return false, nil
}
