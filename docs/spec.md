# Hand — personal spec

Hand is the operator's personal tool for running a crew of coding agents through one supervisor. It is not a product for other users. Every decision is judged by one question: does this make the operator's daily loop cheaper, more deterministic, or more reliable?

Written 2026-09-26 after freezing the v19 rewrite. The runtime was chosen the same day after two spikes: Orca (`~/orca-spike/REPORT.md`) and Luvus (`~/luvus-spike/REPORT.md`).

## Why this exists

The operator evaluated kunchenguid/firstmate (same concept: one liaison agent running a crew in worktrees) and rejected it: it burns tokens and depends on too many tools (gh-axi, quota-axi, lavish-axi, …). The parts worth keeping are the supervisor model, fleet memory, and determinism. Hand's own v19 work then drifted into product-grade machinery (multi-platform release qualification, npm/edge publishing, versioned contract manifests, live-cutover proofs, exec-guard attestation) that cost days and tokens without improving the daily loop.

## Core

1. **One supervisor.** The operator talks to a single agent. It captures requests, dispatches workers into isolated git worktrees, watches them, and escalates only decisions that genuinely need the operator.
2. **Fleet memory.** Operator context, project context, and task history are durable outside any chat session. A fresh supervisor orients from Hand state alone, cheaply, without re-explanation.
3. **Determinism.** Workflow state lives in a state machine over SQLite, never in LLM memory. The same state renders the same orientation. Idle costs zero tokens: a watcher wakes the supervisor only when something changed.
4. **Board.** One place outside chat where everything is visible:
   - every operator request becomes a durable item (inbox/Task) before work starts, so nothing lives only in chat text;
   - one card per Task: goal, status, current plan, attempts, latest report, pending decisions, PR link;
   - the board is a pure projection of Hand state: deleting or restarting it loses nothing;
   - board actions are narrow and exact (answer a Decision by ID, acknowledge a report by ID); anything needing judgement goes through the supervisor.

## Hard constraints

- **Token-frugal.** Compact output (TOON). `hand orient` has a measured size budget. No LLM polling; event-driven wakes only.
- **Few dependencies.** Required: git, the harness CLIs the operator uses (Claude Code, Codex), and Luvus (github.com/RizRiyz/luvus) as the one runtime backend. Hand uses only Luvus's runtime primitives over UHP 1.0: exact-argv terminals, agent status, fenced input, and events. Hand owns state, worktrees and the board itself, and never uses Luvus's task ledger or worktree commands. `gh` is optional. No `*-axi` tools, no private bundled runtime.
- **Upstream.** Every Luvus gap gets a Hand-side guard first. Issues and pull requests to RizRiyz/luvus run in parallel, and a guard is deleted only after the fix ships in a release Hand pins.
- **Linux only**, the operator's machine and projects.
- **Board** is `hand board`: a local web page from the same Go binary (`net/http` + `html/template`), read-only over state, auto-refresh (SSE or meta-refresh), no JS framework, reachable from a phone over LAN/tunnel. A Luvus dock may mirror one line per Task; it never replaces `hand board`.

## Non-goals

- Windows or macOS support; multi-platform release qualification.
- npm packages, edge channel, public release process.
- Versioned contract manifests, DDL relock ceremony, mutation-score gates.
- Live or reboot-witnessed cutover from old homes: migrate the operator's fleet by hand or start a fresh home.
- Exec-guard, operator attestation, provider identity proofs beyond what the chosen backend gives natively.
- Features for hypothetical other users.

## Decision path

1. Runtime spikes, Orca and then Luvus, chose Luvus on 2026-09-26. It has fenced input that refuses at permission prompts, zero-token event waits, and exact process identity, and it is a 13 MB binary instead of a 556 MB Electron app.
2. Then a focused redesign on an orphan branch against this spec, reusing proven legacy pieces (state machine, fleet memory, watcher, TOON output) rather than designing from zero.
3. The frozen v19 work is reference material: its contracts list exactly what to demand from any runtime (process identity, cessation, safe cleanup, exact input delivery).
