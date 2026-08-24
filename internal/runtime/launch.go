package runtime

import (
	"fmt"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/launch"
)

type launchPoll struct {
	Interval   time.Duration
	QuietReads int
	Timeout    time.Duration
	KeySettle  time.Duration
	ReadLines  int
}

var launchPolling = launchPoll{
	Interval:   time.Second,
	QuietReads: 6,
	Timeout:    time.Minute,
	KeySettle:  300 * time.Millisecond,
	ReadLines:  60,
}

func ConfigureLaunchPollingForTest(interval, timeout, keySettle time.Duration, quietReads, readLines int) func() {
	previous := launchPolling
	launchPolling = launchPoll{
		Interval:   interval,
		QuietReads: quietReads,
		Timeout:    timeout,
		KeySettle:  keySettle,
		ReadLines:  readLines,
	}
	return func() { launchPolling = previous }
}

func SetLaunchTimeoutForTest(timeout time.Duration) { launchPolling.Timeout = timeout }

func confirmLaunch(client herdrClient, paneID, harnessName string, spec launch.LaunchSpec) error {
	prompts := harness.FirstRunPromptsFor(harnessName)
	deadline := time.Now().Add(launchPolling.Timeout)
	quiet := 0
	answered := map[string]bool{}
	sawAgent := false
	detected := harness.AgentDetectionVerified(harnessName)
	noAgent := "the harness never started in the pane"
	if !detected {
		noAgent = fmt.Sprintf("no agent detected in pane; herdr agent detection for harness %s has not been exercised", harnessName)
	}
	var stall string

	for {
		processInfo, err := client.PaneProcessInfo(paneID)
		if err != nil {
			return fmt.Errorf("observe worker process: %w", err)
		}
		processPresent := processInfo.HasExecutable(spec.Executable)
		pane, err := client.PaneGet(paneID)
		if err != nil {
			return err
		}
		text, err := client.PaneRead(paneID, launchPolling.ReadLines)
		if err != nil {
			return err
		}

		sawAgent = sawAgent || pane.Agent != "" || processPresent
		prompt, known := matchFirstRunPrompt(prompts.Known, text)
		switch {
		case harness.IsOneShot(harnessName) && processPresent:
			// Seeing the intended executable is direct start evidence. Do not wait for a one-shot
			// process to disappear: disappearance says nothing about whether its turn succeeded.
			return nil
		case harness.IsOneShot(harnessName) && harness.LaunchEvidenceInOutput(harnessName, text, spec.Cwd):
			// A very short run can initialize and exit between polls. Its typed init event proves
			// startup without treating a provider result/status as Hand task outcome.
			return nil
		case !processPresent:
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
		case known && prompt.Refuse != "":
			return fmt.Errorf("worker is waiting on the %s prompt: %s", prompt.Name, prompt.Refuse)
		case known:
			if !answered[prompt.Name] {
				if err := answerFirstRunPrompt(client, paneID, prompt); err != nil {
					return err
				}
				answered[prompt.Name] = true
			}
			quiet = 0
			stall = fmt.Sprintf("the %s prompt was answered, its text is still in the pane, and the harness never became ready", prompt.Name)
		case prompts.Unrecognized != nil && prompts.Unrecognized.MatchString(text):
			quiet = 0
			stall = "the pane is showing a dialog no known signature covers"
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
		if !found {
			match, found = prompt, true
		}
	}
	return match, found
}

func answerFirstRunPrompt(client herdrClient, paneID string, prompt harness.FirstRunPrompt) error {
	for _, key := range prompt.Keys {
		if err := client.PaneSendKeys(paneID, key); err != nil {
			return fmt.Errorf("answer %s prompt: %w", prompt.Name, err)
		}
		time.Sleep(launchPolling.KeySettle)
	}
	return nil
}
