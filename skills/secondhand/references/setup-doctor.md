# Setup and doctor

## The one command

Run `hand doctor` whenever you start a session, after `hand update`, after a merge that touched
fleet-generated surfaces, or whenever something feels off. It is report-only: it never repairs
anything, so every finding it prints is yours to act on, not something it will fix on a later run.

`hand doctor` prints a readiness contract alongside the `findings` table.
The contract uses flat TOON blocks: runtime fields, `tools`, `integrations`, `harnesses`, `ready`, `blocking`, and `next`.
The `findings` table has `line`, `severity`, and `finding` columns.
Severity `error` means the run fails (nonzero exit); `warning` and `info` do not. Read every
line, not just the exit code: a clean exit with warnings still means something is worth a look.

## What doctor actually checks today

- `AGENTS.md` against the canonical Hand-owned content, byte for byte. Any difference is one
  `error` finding naming the exact remedy (`hand init <home>`); there is no partial-drift
  detail because the whole file is generated, so there is nothing partial to report.
- The bundled `secondhand` skill at every supported destination: missing (`error`), drifted from
  the canonical content (`error`), or a foreign/unmanaged file occupying the destination
  (`error`, refuse-to-overwrite) - each names its own remedy, and a collision is distinguished
  from ordinary drift because a collision needs a human to move something aside first.
- Windows only: the `CLAUDE.md` pointer file's presence and exact content.
- For each registered Project whose current v0.7 configuration selects no-mistakes, whether that
  exact Project repository is ready for the integration, or is not initialized or unreachable.
  This Project-readiness observation is separate from exact work/PR gate-run evidence.
- Routing/configuration validity: missing Profiles or Routes, and whether the fleet is running
  on explicit configuration or falling back to legacy defaults.
- The selected private Git, Treehouse, and Herdr runtime, including its target, runtime ID,
  bundle, component paths and versions, and readiness reason. Core execution does not fall back
  to machine `PATH` in production.
- The closed optional capability catalog in `integrations`; these capabilities are selected
  explicitly and are not downloaded by core bootstrap. A registered project's delivery mode can
  make a capability such as `gh` required.
- The `tools` block reports the core components and optional tool requirements as `installed` and
  `required`; `harnesses` reports supported coding-agent harnesses reachable on `PATH`.
- Every supported coding-agent harness is listed in `harnesses` as installed or not installed.
  No harness is preferred by this contract.
- `ready` is false when fleet health has errors, a required tool is missing, or no supported
  harness is installed.
  `blocking` names those conditions and `next` gives one corresponding recovery action for each.
- The running binary's `version`, `channel`, `commit`, and `distribution`, as plain fields
  rather than findings - useful context when comparing a fleet's state against a bug report.

Do not assume categories beyond what a given `hand doctor` build actually reports. If this
reference and a live run disagree, the live run is right; report the mismatch rather than
trusting the stale prose.

`hand fleet` is the discovery view for users with more than one Fleet home.
It reads the user-local registry without selecting an active Fleet or changing any home.
Read a `duplicate`, `identity-mismatch`, `ambiguous`, or `unreadable` state as an ownership boundary, not as permission to choose a path yourself.

## Distinguishing what a finding actually means

Doctor's findings distinguish concepts that are easy to blur:

```text
supported by Hand       - the harness/tool is one Hand knows how to drive
installed in private store - the capability or core runtime is available to Hand
installed on PATH       - a harness is actually reachable from this environment
configured              - an operator explicitly persisted a choice through hand config
required                - this fleet or project cannot proceed without it
optional                - useful but not blocking (no-mistakes is optional, never assumed)
```

wrong: treating an optional integration such as `no-mistakes` as though every project must
have it configured before work can proceed.

right: reading whether a *specific registered project* declared `no-mistakes` mode, and only
then treating its gate as required for that project.

Project integration readiness is not exact work/PR gate-run evidence:

- Project readiness is the existing Project-level `GateStatus` observation used by `project list`,
  `doctor`, and dispatch/integration preflight.
- Exact work/PR evidence is the shared `GateRunObservation` used by status/watch/orient. It keeps
  the `found | absent | unknown` vocabulary for the exact PR recorded by completed `no-mistakes runs`.
  `gate-absent` and `gate-unknown` belong to this work evidence, not to Project setup; `absent` is
  distinct from `unknown`.
- Doctor is read-only and does not inspect `no-mistakes runs` to decide Project integration readiness.

Doctor never invents local installation state, account entitlement, provider billing state, or
model availability. If a finding does not name one of those things explicitly, do not infer it.

## Acting on findings

- A generated-surface drift finding always names its own remedy command. Run exactly that
  command; do not hand-edit the file it names, since it will be overwritten again on the next
  `hand init` regardless.
- A Project-readiness finding means that exact registered Project's no-mistakes integration state,
  not work/PR gate-run evidence and not the whole Fleet. Read `references/task-lifecycle.md` before
  dispatching further into that project.
- A routing finding means read `references/configuration.md` before dispatching a task whose
  Profile or Route is missing or invalid; do not guess a Profile to unblock a single dispatch.

## First session in a new home

`hand init` is non-interactive: it never asks a question or invents a worker default. On the
first supervising session, run `hand config` to see the effective harness/model/effort state,
then read `references/configuration.md` for how to propose and persist Profiles and Routes
before the first dispatch.
