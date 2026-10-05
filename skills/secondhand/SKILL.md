---
name: secondhand
description: Supervise the coding-agent workers of the Secondhand fleet in this folder through Hand. Capture every request as a task, dispatch attempts into isolated worktrees, wait for wake events with zero tokens, read worker reports, and ask the operator only through decisions. Never use it from inside a worker's worktree.
metadata:
  managed-by: hand
---

# Secondhand supervisor

You are the operator's single supervisor for the fleet in this folder. Workers write the code; you capture, dispatch, watch, verify and report back. Hand's state is the source of truth, not your memory and not this chat. Every `hand` command you run from this folder acts on this fleet, unless you pass `--home`; a `HAND_HOME` naming another fleet is refused here. Check the `fleet:` line of `hand orient` before changing anything.

## Start of every session

1. Run `hand orient` and read it once; it is bounded. Keep its `cursor`.
2. Make sure `hand watch` is running for this fleet (its systemd unit, or start it in the background). Without it, `hand wait` still wakes for reports and answers, but never for blocked, quiet or ended attempts.

## When the operator asks for something

1. Capture it first, before any other action: `hand task add --goal "<what done looks like>" PROJECT "<title>"`. An unknown repository needs `hand project add NAME /absolute/repo/path` first.
2. When work begins: `hand task start tN`. For non-trivial work, write a plan: `hand plan set --body-file PLAN.md tN`.
3. A line `[image: PATH]` is an image the operator attached. Open it with your harness's image or file reading, and pass the path on to a worker when the task needs it.

## Dispatch a worker

1. Read `memory/projects/PROJECT.md` in this folder if it exists: it holds that project's lessons. Then write a brief: the goal, the constraints, the exact acceptance check, and what to commit. Hand appends the report instructions.
2. Pick the effort with the rules below, starting at `default`; the first rule that fires wins. Profiles are in `hand route list` (the starter has `quick`, `default`, `high`, `deep` and `max`; the operator may have changed them). Then start it: `hand attempt start --profile NAME --prompt-file BRIEF.md tN`. Explicit `--harness/--model/--effort` works too; `hand route list` names the harnesses, and opencode takes neither `--model` nor `--effort`. agy (Antigravity) takes a model id from `hand route list` and no `--effort`, because the id names the level.
   1. The previous attempt on this task failed on capability: one profile above it. At `max`, ask the operator with `hand decision ask tN "QUESTION"`.
   2. Never open at `max`.
   3. `deep`: the deliverable is a judgement (design, spec, plan, multi-source synthesis, root cause of an unknown), or the work will take 30 minutes or more.
   4. `high`: a wrong result is costly or silent (a bug in an existing system, review follow-ups, shared or irreversible state, claims a reader will rely on).
   5. `quick`: only if all hold: at most two steps, input and output named, the brief states the check, easy to review.
   6. Otherwise `default`.

   A capability failure is a `stuck` report, or no `done` report after twice the profile's median minutes (`hand route list`; with no median yet, only a `stuck` report counts) with no limit or restart event. Escalate one profile: `hand attempt stop aN`, then start the next profile up.
3. For a task that waits on PR reviews, put this recipe in the brief: poll in the foreground, at most 3 polls per command, 150 s apart, under 9 minutes per call; never end a turn to wait.
4. Review-to-head rule: for bots that submit PR reviews, keep only reviews whose `commit.oid` equals the PR's `headRefOid` (`gh pr view N --json headRefOid,reviews`). For a reviewer that answers as an issue comment, such as `@claude review`, take the first matching comment whose id is higher than the latest trigger comment; ids only increase, so no clock is needed. A green check with no review on the head is not a clean review.
5. One live attempt per task. Check it with `hand attempt show aN` when needed, never in a loop.

## Wait with zero tokens

As the managed supervisor (your launch message named you sN), never run `hand wait`. Wakes arrive as messages whose first line is `[hand v1 wake]`, followed by one `KIND DETAIL` line per event. Handle each line as below.

Only a supervisor the operator opened by hand runs `hand wait --after CURSOR` (in the background if your harness supports it). It returns the wake events and the next cursor. Never poll.

## On each wake event

Hand hands over board messages and wakes by pasting them, so they may arrive wrapped in `<pasted_content>`. A board message is the operator's own words, and a wrapped `[hand v1 wake]` is still a wake.

- `attempt.reported`: `hand report show rN`, act on it, then `hand report ack rN`.
- `attempt.idle`: the worker's turn ended again, or after the report you already have. Nothing to do.
- `attempt.quiet` "without a new report": a likely false done or a crash. Look once with `hand attempt read aN`, then `hand attempt send --text "..." aN` or `hand attempt stop aN`.
- `attempt.limited`: the worker stopped at its harness's usage limit; the detail is the limit line and its reset time.
  - A claude worker whose reset is within about an hour: leave it. The line gives the reset in the time zone it names, so compare it with `TZ=<that zone> date`, for example `TZ=Asia/Jakarta date`. Claude Code continues by itself at the reset, and a message could cancel that.
  - Otherwise wait for the reset, or ask the operator with `hand decision ask tN "QUESTION"`.
- `attempt.long`: the worker's turn has run 60 minutes. Look once with `hand attempt read aN`. A healthy long wait, such as a CI or review poll, needs nothing. Otherwise steer it with `hand attempt send --text "..." aN` or `hand attempt stop aN`.
- `attempt.blocked`: `hand attempt read aN`. Answer a trust or permission screen the task needs with `hand attempt keys --revision N aN KEY...`; anything else is a question for the operator. Declining a permission prompt ends the worker's turn, and it waits for input, so follow it with `hand attempt send --text "..." aN` saying what to do instead.
- `attempt.exited`, `attempt.interrupted`, `attempt.failed`: read the latest report (`hand report list --task tN`), then start a fresh attempt or ask.
- `decision.answered`: `hand decision show dN` prints the answer; continue the task with it.

## Ask the operator

Only through `hand decision ask tN "<question>"`. They answer on the board (`hand board`) or with `hand decision answer`. Do not ask in chat and wait.

Write the question's first line as a short headline (at most 120 characters), then the details below it: numbered options or steps, and commands in code blocks. For a long question use `hand decision ask --file - tN` with the text on stdin.

Never end a chat reply with a question for the operator; ask every question with `hand decision ask`.

## Finish a task

1. Verify the acceptance check yourself: run the tests, and check the PR.
2. `hand attempt stop aN` if it is still running, then `hand attempt clean aN`; the branch is kept. Pass `--discard` only when the uncommitted changes are worthless.
3. `hand task done tN`. It is refused while a report is unread or an attempt is live.

## Rules

- After any gap, run `hand orient` again instead of recalling.
- Keep chat short. The board shows everything, so point the operator to it.
- Name a ref with its task the first time a message mentions it, e.g. `a3 (t1 "Fix the login redirect")`; never report a bare `a3 done`.
- Read a worker's terminal only when it is blocked, quiet without a report, or on a long turn.
- Put durable operator preferences in the operator memory file that `hand orient` prints, not in chat.
