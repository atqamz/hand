# The process exit is how an event is delivered to a supervisory agent

- Date: 2026-08-04
- Status: accepted
- Issues: none
- PRs: atqamz/secondhand#125

## Context

Detecting a fleet event and delivering it to a supervisory agent are separate problems, and streaming solves only the first.

`hand watch` never exits.
A supervisory agent's background-task runner re-invokes the agent when a process *exits*.
So a streaming watcher's stdout is a file that gets read only if the agent independently decides to look, and "remember to check the watcher" is not a mechanism.
It failed on 2026-07-28.

The wrapper that stood in for a mechanism was `hand watch | tee log | grep -m1 <pattern>`, and it failed in four distinct ways.
`grep` matched a worker that was already `done` when the pipeline started, exited, and left the pipeline half-alive with nobody reading the two real events that followed.
It had to exclude `idle-unreported` from its pattern to avoid a wake storm, which is the exact signal it was built for.
A worker whose pane could not be reached at all produced no match and no diagnosis, so the caller waited out the full window.
And the caller had to assemble the pipeline correctly every time.

## Decision

`hand watch --until-event` makes the process exit the delivery.
It arms, takes a silent baseline, polls, and on the first tick that produces any event writes that tick's events to stdout and exits 0.

Four rules make that exit trustworthy, each closing one of the wrapper's failures:

The startup state is never an event.
Only a change from the baseline exits.

Every wake trigger is edge-triggered, `idle-unreported`, `stale` and `parked` included.
A worker fires once on entering a condition and not again until it leaves and re-enters, so no signal has to be excluded to avoid a storm.

Arming can fail loudly and distinctly.
A task whose pane fails its arm-time probe is exit 5 naming that task, because a task invisible to the first probe has no transition to ever fire on.

The exit code says which happened: 0 an event was delivered, 4 no event, 5 a named task's arm probe failed, 3 another watcher owns this home, 1 the watcher itself failed, 2 a usage error.
A caller can never read a crash or a quiet window as fleet news.

Baseline events are withheld from stdout only.
They still reach `state/events.log` and the notify hook, because the report lines behind them are consumed either way.

One invocation delivers one wake.
Re-arming is the caller's own next step after acting on the exit.

## Rejected alternatives

**Keep streaming and rely on the agent to read the log.**
This is the state that failed.
Nothing in the agent's loop obliges it to look, and the failure is silent.

**Wrap streaming in `tee` and `grep -m1`.**
This is what was replaced.
Every one of its four failures is a property of the wrapper rather than of the pattern being matched, so no better pattern fixes it.

**Have the watcher call back into the agent rather than exiting.**
There is nothing to call: an agent with no session running is not addressable, and one with a session running is already re-invoked by the exit.
The unattended case is answered separately by the notify hook; see `notify-is-a-filtered-consumer-of-the-event-stream.md`.

**Return 0 on a timeout and let the caller check stdout for emptiness.**
Then a crash, a quiet window and a delivered event are one exit code apart from each other only by output shape, and a caller that gets it wrong reads a crash as fleet news.
Signals take exit 4 for the same reason: nothing was delivered, so it is not 0.

**Fold the arm-probe failure into the timeout.**
The caller would wait out the full `--timeout` for a cause it can never see on stdout.
A timeout during arming stays 4, because no single task can be named as the cause the way 5 promises.

## Consequences

The exit code table is contract, enforced at the point of exit and tested in `tests/e2e`.

Worst-case delay from a transition to the exit that delivers it is one poll interval plus that tick's bounded work.
The `gh pr view` check for a task with a recorded unmerged PR is the only unbounded-looking piece and is capped per task and run one at a time.

The agent's loop becomes: arm the watcher, read `hand status` and `state/events.log` for current truth, treat the next exit as the answer to what changed since arming.
Anything landing between one exit and the next arming is in those same two places.

Because arming consumes report lines, an arming watcher and a streaming watcher cannot share a fleet home; see `one-watcher-per-fleet-home-guarded-by-an-flock.md`.
