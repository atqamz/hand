# Antigravity uses one-shot headless worker execution

- Date: 2026-08-23
- Status: accepted
- Issues: atqamz/hand#336, atqamz/hand#338, atqamz/hand#353, atqamz/hand#355
- PRs: atqamz/hand#351, atqamz/hand#356

## Context

Antigravity CLI documents `agy -p` as headless one-shot execution: one prompt runs, emits a response, and the process exits. `--output-format stream-json` emits a typed `init` event before step updates and one terminal `result` event. `--input-format stream-json` is a different persistent-stdin protocol; when it is used, a prompt supplied with `-p` is dropped and prompts must instead be submitted as JSON messages on stdin.

Hand's existing `LaunchSpec` deliberately describes executable, argv, environment, and cwd only. It has no durable stdin-submission transaction. Pretending the persistent-stdin protocol fits that boundary would introduce a new crash window where Hand could not prove whether the initial prompt was submitted, so #351 does not do that.

A one-shot worker also cannot safely consume `hand send` through its pane after exit: the pane has returned to its shell, so arbitrary operator text would become shell input. Process disappearance itself is not success evidence, and Antigravity's provider `result.status` is not Hand task outcome. Hand's report file remains the sole worker outcome channel.

PR #356 separately owns Supervisor Harness qualification and wake/re-entry under `internal/supervision`. Worker process residency must not become a second Supervisor registry.

## Decision

Antigravity remains a worker Harness implemented through the shared structured `LaunchSpec` boundary.

The adapter launches `agy` with:

- `-p <semantic worker prompt>` for one-shot execution;
- `--output-format stream-json` so startup has typed machine-readable evidence;
- `--dangerously-skip-permissions` because an unattended worker must not soft-deny required tool actions behind an approval request; this bypasses Antigravity approvals but does **not** enable `--sandbox` or create a Hand isolation boundary;
- `--print-timeout 24h` to replace Antigravity's five-minute headless default with an explicit finite Worker runtime envelope; timeout is liveness/failure evidence, never task outcome;
- `--model <slug>` and `--effort <low|medium|high>` only when selected.

Routing qualifies the installed Antigravity runtime before dispatch. The probe requires the executable, supported platform, documented headless/output/permission/timeout flags, and a non-empty `agy models` result. An explicit authentication/configuration error from qualification is unavailable; otherwise successful model discovery is not treated as proof that credentials cannot expire or fail on the later model request. Unknown contract or observation stays unknown and fails closed; Hand does not install the CLI or persist credentials.

Launch confirmation means only that the intended worker initialized. For a resident observation, seeing the intended executable is enough. For a one-shot process that starts and exits between polls, an Antigravity `event: "init"` line is equivalent startup evidence. Merely observing that the process disappeared is never confirmation. A terminal provider `result` is deliberately ignored for Hand outcome semantics.

Before durable launch confirmation, a fast one-shot may be confirmed from a typed `init` event only when its documented `init.cwd` exactly matches the LaunchSpec CWD. After confirmation, pane scrollback is no longer identity evidence: reconciliation, status, and watch normalize one-shot liveness from the persisted Attempt harness plus live process information. A confirmed Attempt with no `agy` process is `done` only in Herdr liveness terms; Hand still requires the report channel for task outcome.

`hand send` refuses a one-shot Antigravity Attempt before creating a SendAttempt or mutating the pane. Follow-up work requires the worker's report to be observed and then a new Attempt; conversation IDs are not canonical Hand state.

Supervisor support is not decided here. `internal/supervision` owns the qualified Supervisor Harness registry and its runtime/wake capabilities. Antigravity may be added there only when that separate contract is qualified; the worker package exposes no `SupportsSupervisor` mirror.

## Consequences

Antigravity stays optional; provider-specific behavior remains isolated behind the Worker Harness adapter and compatible with the shared LaunchSpec -> Session transport architecture.

Hand does not:

- infer success from process exit;
- infer task success from Antigravity `SUCCESS`;
- treat permission bypass or the WorktreeBinding CWD as a filesystem sandbox/isolation boundary;
- send operator text into a shell after a one-shot process exits;
- persist Antigravity conversation IDs or provider lifecycle state;
- create a fake resident process;
- add a second Supervisor capability registry;
- adopt persistent stdin streaming without a durable submission protocol.

A fast one-shot whose process is never observed and whose typed `init` evidence is unavailable remains unconfirmed rather than being guessed successful. That false negative is intentional: unknown is safer than fabricated launch truth.
