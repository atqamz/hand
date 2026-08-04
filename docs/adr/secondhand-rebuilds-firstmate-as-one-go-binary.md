# Secondhand keeps firstmate's concept and rebuilds its execution as one Go binary

- Date: 2026-08-04
- Status: accepted
- Issues: none single
- PRs: none single

## Context

Running one coding agent is easy.
Running three in parallel on different tasks across different projects turns an operator into a tab-juggler: babysitting sessions, copy-pasting context, forgetting which terminal had the failing test.

Firstmate solved that with an "agent distro": a directory of instructions and shell scripts that turns a general-purpose agent into a fleet supervisor.
The concept worked.
The execution ballooned in six days to 34k lines of shell across 89 scripts, 1,082 functions, 8k lines of prose instructions, 5 backend adapters, a Twitter bot, and a multi-home federation system.

Three failures were structural rather than incidental, and each one is a property of the shape rather than of any particular script.

**Session clobbering.**
The supervisory agent's main session drowned in operational noise: bootstrap digests, hook injections, guard warnings, status polling, watcher rearms.
A 21K-token always-loaded instruction file ate context every turn, and long sessions produced malformed tool calls at around 500k context.
The operator's chat became a system log instead of a command center.

**Shell brittleness.**
macOS bash 3.2 caused silent failures in spawn and brief scaffolding.
Locale inheritance broke checksums and state reads.
BSD versus GNU tool detection failed under mixed toolchains.
Content-hashing terminal panes to detect agent state was fragile at the premise, not in the implementation.

**Self-imposed complexity traps.**
Session locks deadlocked recovery.
Continuity hooks blocked the very commands needed to fix the problems they detected.
The watcher, guard and hook system created more problems than it solved.

## Decision

The concept is kept and the execution is rebuilt as a single Go CLI binary, `hand`.

Everything the three failures point at becomes a standing constraint rather than a preference, and the "Core principles" section of SPECS.md is the contract form of them:

- One binary owns orchestration, and there are no shell scripts in the orchestration path.
- AGENTS.md stays tiny, on the order of 25 lines, with operational detail in `--help` instead.
- Agent state comes from herdr's semantic states, never from scraping or hashing terminal output.
- A feature with no proven use case is cut, and gets added when its absence causes real pain.
- The CLI fails closed and reports errors as its own output; it installs no guards, no callbacks and no continuity hooks.

## Rejected alternatives

**Keep firstmate and pay down its debt incrementally.**
Two of the three failures are properties of the distro shape.
The context cost is inherent to instructions that must be loaded for the agent to operate at all, and the portability failures are inherent to 34k lines of shell across two toolchains.
Neither is reachable by refactoring within that shape.

**Rewrite in a scripting language with better portability than bash, such as Python.**
It fixes the bash 3.2 and BSD-versus-GNU class of failure and leaves the rest: a runtime to install, a dependency set to resolve per host, and no single artifact to ship.
A static binary is the property being bought here, not the language.

**Keep the agent-distro model and shrink the instruction file.**
The instruction file is large because the agent is the orchestrator.
Shrinking it without moving orchestration out of the agent trades context cost for an agent that no longer knows how to operate the fleet.

**Build a daemon with an API, so the supervisory session holds no operational state.**
It removes the noise from the session and adds a process that must be running, supervised and upgraded in step with its clients.
Every command being short-lived is what keeps a fleet home a directory that ordinary tools can copy, back up and inspect.
See `believe-the-status-file-and-ship-no-hand-dump.md`.

**Keep the hook, guard and continuity machinery, fixed.**
The trap was not that the hooks had bugs.
It was that a guard which blocks a command is a guard that blocks the recovery for the condition it detected.
The one hook `hand` installs is deliberately the opposite: a `SessionStart` entry that runs the bare command, policing nothing.
See `ambient-context-is-a-session-hook-not-a-file.md`.

## Consequences

The tool ships as one artifact, so a fleet home carries no orchestration code of its own and an upgrade is one binary replacement.

The supervisory agent is a client rather than the implementation, which is what makes the harness interchangeable at all.

Every later "should `hand` grow this?" question resolves against principle 5, and the answer for anything speculative is no.
