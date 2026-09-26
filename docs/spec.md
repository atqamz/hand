# Hand — personal spec

Hand is the operator's personal tool for running a crew of coding agents through one supervisor. It is not a product for other users. Every decision is judged by one question: does this make the operator's daily loop cheaper, more deterministic, or more reliable?

Written 2026-09-26 after freezing the v19 rewrite and before the Orca spike (`orca-spike-prompt.md`, report at `~/orca-spike/REPORT.md`).

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
- **Few dependencies.** Required: git and the harness CLIs the operator uses (Claude Code, Codex). One runtime backend: Orca, or Herdr with native `git worktree`. `gh` optional. No `*-axi` tools, no private bundled runtime.
- **Linux only**, the operator's machine and projects.
- **Board** is `hand board`: a local web page from the same Go binary (`net/http` + `html/template`), read-only over state, auto-refresh (SSE or meta-refresh), no JS framework, reachable from a phone over LAN/tunnel. Use Orca's UI instead only if the spike shows it can render Hand's own state.

## Non-goals

- Windows or macOS support; multi-platform release qualification.
- npm packages, edge channel, public release process.
- Versioned contract manifests, DDL relock ceremony, mutation-score gates.
- Live or reboot-witnessed cutover from old homes: migrate the operator's fleet by hand or start a fresh home.
- Exec-guard, operator attestation, provider identity proofs beyond what the chosen backend gives natively.
- Features for hypothetical other users.

## Decision path

1. Orca spike decides the runtime backend. Judge it on capability and on cost: Orca is one large GUI dependency replacing Herdr, Treehouse and the bundled runtime; plain Herdr + `git worktree` is the minimal alternative.
2. Then a focused redesign on an orphan branch against this spec, reusing proven legacy pieces (state machine, fleet memory, watcher, TOON output) rather than designing from zero.
3. The frozen v19 work is reference material: its contracts list exactly what to demand from any runtime (process identity, cessation, safe cleanup, exact input delivery).
