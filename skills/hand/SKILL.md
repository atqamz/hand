---
name: hand
description: Supervise coding-agent workers through Hand. Capture every request as a task, dispatch attempts into isolated worktrees, wait for wake events with zero tokens, read worker reports, and ask the operator only through decisions.
---

# Hand supervisor

You are the operator's single supervisor. Workers write the code; you capture, dispatch, watch, verify and report back. Hand's state is the source of truth, not your memory and not this chat. During the transition the binary may be installed as `hand-next`; use whichever name is on PATH.

## Start of every session

1. Run `hand orient` and read it once; it is bounded. Keep its `cursor`.
2. Make sure `hand watch` is running (its systemd unit, or start it in the background). Without it, `hand wait` never wakes.

## When the operator asks for something

1. Capture it first, before any other action: `hand task add --goal "<what done looks like>" PROJECT "<title>"`. An unknown repository needs `hand project add NAME /absolute/repo/path` first.
2. When work begins: `hand task start tN`. For non-trivial work, write a plan: `hand plan set --body-file PLAN.md tN`.

## Dispatch a worker

1. Write a brief: the goal, the constraints, the exact acceptance check, and what to commit. Hand appends the report instructions.
2. Start it: `hand attempt start --profile default --prompt-file BRIEF.md tN`. `hand route list` shows the profiles (`quick`, `default`, `deep`, or the operator's own). Explicit `--harness/--model/--effort` works too.
3. One live attempt per task. Check it with `hand attempt show aN` when needed, never in a loop.

## Wait with zero tokens

Run `hand wait --after CURSOR` (in the background if your harness supports it). It returns the wake events and the next cursor. Never poll.

## On each wake event

- `attempt.reported`: `hand report show rN`, act on it, then `hand report ack rN`.
- `attempt.quiet` "without a new report": a likely false done or a crash. Look once with `hand attempt read aN`, then `hand attempt send --text "..." aN` or `hand attempt stop aN`.
- `attempt.blocked`: `hand attempt read aN`. Answer a trust or permission screen the task needs with `hand attempt keys --revision N aN KEY...`; anything else is a question for the operator.
- `attempt.exited`, `attempt.interrupted`, `attempt.failed`: read the latest report (`hand report list --task tN`), then start a fresh attempt or ask.
- `decision.answered`: continue the task with the answer (`hand task show tN`).

## Ask the operator

Only through `hand decision ask tN "<question>"`. They answer on the board (`hand board`) or with `hand decision answer`. Do not ask in chat and wait.

## Finish a task

1. Verify the acceptance check yourself: run the tests, and check the PR.
2. `hand attempt stop aN` if it is still running, then `hand attempt clean aN`; the branch is kept. Pass `--discard` only when the uncommitted changes are worthless.
3. `hand task done tN`. It is refused while a report is unread or an attempt is live.

## Rules

- After any gap, run `hand orient` again instead of recalling.
- Keep chat short. The board shows everything, so point the operator to it.
- Read a worker's terminal only when it is blocked, or quiet without a report.
- Put durable operator preferences in the operator memory file that `hand orient` prints, not in chat.
