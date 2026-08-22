# Harnesses launch interactively, with liveness owned by herdr

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/hand#28, atqamz/hand#152
- PRs: none single

## Context

Workers must survive multiple steers and gate turns. Interactive launches provide that resident process but expose first-run dialogs. Pane text can outlive a dead process, while herdr can identify a live harness without knowing what dialog blocks it.
Shell syntax also varies across the pane's actual shell, so a host operating-system guess is not sufficient for a safe launch.

## Decision

Every harness template produces a structured launch specification containing its executable, ordered arguments, environment, and working directory.
Herdr observes the idle pane's shell, renders only the executable and arguments for the supported shell family, and carries environment and working directory through resource creation.
Herdr process evidence answers whether the expected process is live. Pane text is used only to detect dialogs, and a launch is confirmed only when both signals are clear.

Launch arguments and dialog signatures belong to [`internal/harness`](../../internal/harness); shell renderers and process observation belong to [`internal/herdr`](../../internal/herdr); confirmation belongs to [`internal/runtime/launch.go`](../../internal/runtime/launch.go). Their tests own supported flags, prompt handling, shell quoting, and failure behavior.

## Rejected alternatives

- Headless reinvocation has no resident session for `hand send`, watching, or multi-turn gates.
- A wrapper around headless runs would reimplement harness continuity and make pane state describe the wrapper.
- Building one opaque POSIX command string cannot preserve process argument boundaries across POSIX and PowerShell.
- Selecting a renderer from host `GOOS`, `$SHELL`, or a configuration default does not prove which shell owns the pane.
- Screen text cannot prove liveness because a dead harness leaves text behind.
- Agent presence alone can confirm a process parked forever on a dialog.

## Consequences

Dialog wording is an external compatibility surface and may need maintenance as harnesses change.
Unknown shells and dialogs fail toward an unconfirmed launch.
Submission remains separately recorded from confirmation so a lost response cannot trigger an unsafe duplicate launch.
