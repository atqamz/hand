# Antigravity uses qualified headless worker execution

- Date: 2026-08-22
- Status: accepted
- Issues: atqamz/hand#336
- PRs: none single

## Context

The qualified Antigravity CLI contract exposes `agy -p` for one non-interactive prompt and exits after the response.
The same contract documents model selection, effort selection, cached credentials, machine-readable output, and external conversation IDs.
It does not establish a resident interactive session that can consume Hand supervisor turns or provider-neutral pane steering.

## Decision

The Antigravity adapter launches `agy` with a structured `LaunchSpec` and a semantic prompt argument.
The adapter maps model values to `--model` and effort values to `--effort`, validating the documented effort set before launch.
Model values are checked against the read-only `agy models` result when a model is requested.
Missing executable, unsupported platform, authentication/configuration failure, and unknown model capability remain bounded capability results without credential persistence or logging.
Antigravity is a worker Harness only until a qualified resident supervisor contract exists.
Supervisor bootstrap refuses an Antigravity supervisor explicitly, while `HAND_ROLE=worker` retains the shared recursive-bootstrap guard.

## Consequences

Antigravity remains optional and operator-owned, with no automatic installation or credential storage.
Worker routing, LaunchSpec transport, Herdr ownership, lifecycle persistence, retry, replan, watcher, and shell behavior remain shared Hand behavior.
The one-shot worker contract does not promise additional `hand send` turns or Antigravity conversation persistence.
Any future supervisor support must qualify a stronger upstream session contract and reuse the existing Hand protocol.
