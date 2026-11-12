# Product

<!-- impeccable:product-schema 1 -->

## Platform

web and native desktop (Linux in v1)

## Users

Hand has one user: the operator who built it for personal use. They run a few fleets side by side, for example work, personal and research. Each fleet is a folder with its own supervisor agent and its own workers.

The board is the one thing they open; a terminal is optional. They use it as much on a desktop as on a phone, which reaches it through the Hand app or `ssh -L`. Neither device is secondary. The native window is the second local client: it opens on the same fleet, reads the same state and never becomes a second owner of it.

## Product Purpose

Hand is a personal supervisor layer for agents. The operator talks to one supervisor agent per fleet. The supervisor:
- captures every request as a task;
- dispatches Claude Code, Codex or opencode workers into isolated git worktrees through Luvus;
- waits for them at zero token cost;
- reads their reports;
- asks the operator only through decisions.

The board (`hand board`) is the operator's interface to all of this. From it, the operator starts, resumes, stops and chats with the background supervisor, answers its blocked screens, and follows the fleet's tasks.

Success means two things: daily work never needs a terminal, and nothing important sinks into chat history.

## Positioning

- Workflow state lives in a SQLite state machine, never in an agent's memory. The same state renders the same view, and idle costs zero tokens.
- The conversation is read from each harness's own session record, filtered with calm rules:
  - final replies always;
  - substantive mid-turn text;
  - never tool calls or thinking.

  Nothing is installed into, or changed in, any harness.
- One supervisor per fleet stays a live terminal session in the background. The board is a window onto it, not a replacement chat engine.

## Operating Context

- **Platform:** Linux, macOS (Apple silicon) and native Windows (amd64), without WSL.
- **Fleets:** they are registered under `~/.secondhand/fleets`. One board process serves every fleet, each at its own path, `127.0.0.1:7777/<fleet-id>/`. Each page shows one fleet; there is no combined cross-fleet page.
- **Surfaces:**
  - the rail (a sheet on a phone): Board, Tasks, Conversation, Screen, Phone and the fleets this browser can open;
  - Needs: the open items in severity bands, with the keys of a blocked screen and the choice list of a decision;
  - the supervisor pane: Start, Resume, New session, Stop, Interrupt, the conversation (the calm view, plus messages still queued) and the message box, with a separator that resizes it;
  - the Screen page: the supervisor's pane as it is, with its revision and digest, the fixed keys and an Ask card that presses a numbered option or moves the cursor to one;
  - Tasks: the task list on the fleet page, and the Tasks page that searches every task;
  - the task page: plan, reports and events;
  - the attempt page: its brief, launch line, reports and events;
  - the decision page;
  - the conversation page;
  - error pages;
  - a JSON API under `/<fleet id>/api/v1` for a local client: status and Needs, the timeline, the tasks list, task, attempt, decision and report detail, with an opaque change cursor, plus the actions a local client may take: a message to the supervisor, an answer to a decision, an acknowledgement of a report, and the supervisor controls (start, resume, stop, interrupt, force and keys), with the blocked supervisor's revision, digest and pressed sequence so a key press keeps the stale-screen guard;
  - the native desktop window (`hand-desktop`): a fleet switch, the status with the supervisor controls and a blocked supervisor's question and option buttons (the key toolbar when there is none), Needs, the timeline with its composer, the tasks list and the task, attempt, decision and report detail, read through the JSON API over loopback, and the actions the API allows (a message, an answer, an acknowledgement and the supervisor controls), with a per-fleet draft that survives a closed window. It is a client of the board, never a second owner of state: its local files are that draft and `prefs.json` beside the drafts folder, and closing it stops neither the board nor a watcher.
- **What needs the operator:**
  - open decisions;
  - blocked supervisor screens;
  - unread reports;
  - failed, exited or interrupted attempts;
  - a supervisor left interrupted after a reboot.
- **Refs and states:**
  - Refs: `tN` task, `aN` attempt, `rN` report, `dN` decision, `sN` supervisor.
  - Task states: inbox, active, done, abandoned.
  - Attempt and supervisor states: launching, running, exited, interrupted, stopped, failed.
  - Agent states: idle, working, done, blocked.
- **Terminal counterpart:** the `hand` CLI, `hand open` (browser deep links) and `hand attach` (the live TUI).

## Capabilities and Constraints

- **Server:** Go standard library (`net/http`, `html/template`), in one binary with few dependencies.
- **Desktop:** MyGo v0.7.4, pinned and cgo-free, draws the window from Go with no webview; it is a separate binary and reads the board over loopback like any local client.
- **JavaScript:** allowed since 2026-09-28, as small first-party code with no framework and no build step, embedded in the binary. The CSP allows `script-src 'self'`. Live updates use Server-Sent Events from the Go server.
- **Security:**
  - the board token is exchanged for an `HttpOnly`, `SameSite=Strict` cookie named for one fleet, which only that fleet accepts;
  - the JSON API needs that same session cookie, answers only on a loopback board, to the laptop's session or to a phone paired over the peer listener, and refuses a mutation without the session's CSRF token; v1 permits a message to the supervisor, an answer to a decision, an acknowledgement of a report and the supervisor controls, keys included;
  - CSRF protects every form;
  - supervisor controls work only on a loopback listener. On a network address the controls are off, while answering decisions and acknowledging reports still work;
  - error pages reveal no paths;
  - message text allows no control characters other than newline and tab, is at most 16 KB, and must not start with `[hand v1`.
- **Data sources:**
  - supervisor and worker status comes live from Luvus;
  - the conversation comes from harness transcripts;
  - everything else comes from SQLite.
- **Decided on 2026-09-28:** there is no cross-fleet overview. Each page shows one fleet, and `/` only lists the fleets. The rail names the fleets this browser holds a valid token for, or this phone is paired with, so it switches fleets, and it never shows another fleet's counts.

## Brand Commitments

- **Name:** the product is Hand. Its CLI is `hand`, with `hand-next` as the development build. The family and storage name is Secondhand (`~/.secondhand`, `secondhand-*` units).
- **Copy:** interface copy is in English, matching the CLI and the docs.
- **Terms:** task, attempt, report, decision, supervisor, fleet, worker.
- **Assets:** no logo or visual assets exist.

## Evidence on Hand

This is a personal tool: it has no testimonials, customers or usage metrics, and none may be invented. The real content is live fleet data: tasks, plans, reports (often with deliverable links), decisions, events, and supervisor transcripts.

## Product Principles

1. What needs the operator comes first: open decisions, blocked screens, unread reports and failures, before anything else.
2. Calm by default: show the supervisor's words, not its machinery, and give Hand's own events one line each.
3. Actions are exact and fenced: every control acts on one known ref or screen revision, and refuses on stale state.
4. Desktop and phone are equal first-class scenes.
5. Personal and frugal: few dependencies, and no product-grade ceremony.
