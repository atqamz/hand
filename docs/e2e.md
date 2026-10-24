# End-to-end tests with the real harnesses

`internal/e2e` runs Hand against the real `claude` and `codex` binaries, the real Luvus and a throw-away fleet, on Linux, macOS and Windows. Only the model is fake. [`internal/e2e/mockllm`](../internal/e2e/mockllm) is a local server that speaks the Anthropic Messages API and the OpenAI Responses API, streaming, and answers from a script. No account, token or money is involved, and every key is invented.

The install, first run, Board and upgrade tests are a separate workflow, described in [What CI covers end to end](dogfood/README.md#what-ci-covers-end-to-end); it runs with no harness. The unit tests use fakes for the harness. These tests exist for what a fake cannot show: the real terminal screens, the real trust screens, Luvus recognizing a real `claude.exe` or `codex.exe`, the folder the harness starts in, and the briefing reaching the model unchanged.

## What the tests prove

Each scenario runs once per harness, with a fleet in a temporary folder, its own `SECONDHAND_HOME`, `LUVUS_HOME`, `HOME` and `CODEX_HOME`, and a repository the test creates.

| Test | What it checks, in order |
|---|---|
| `TestClaudeAttempt`, `TestCodexAttempt` | `hand attempt start` reports `trust: accepted` and, for Codex, the `trusted:` line and the `attempt.trusted` event for the worktree. The harness's first model request names the worktree as its working directory. The user message that holds the briefing starts with the whole 15 KB brief (quotes, backslashes, `%VARS%`, Unicode, tabs), and its length is the `prompt_bytes` Hand stored. The scripted shell call runs `hand report add`, and a `done` report of `a1` on `t1` arrives in `hand.db`. The command's output reaches the model. The final message shows on `hand attempt read`. `hand attempt keys` is accepted by the real agent. `hand attempt stop` ends the attempt, and `hand attempt clean` removes the worktree and keeps the branch. |
| `TestClaudeSupervisor`, `TestCodexSupervisor` | `hand supervisor start --profile` starts the supervisor in the fleet home. The launch message arrives with the sentence `Your fleet home is ...`, and `hand orient` run by the supervisor's own tool call prints `supervisor: s1 <harness> running`. |
| `TestClaudeSupervisorWake`, `TestCodexSupervisorWake` | The same start, then the operator answers a decision, the watcher sends the wake, and the model receives a message that starts with `[hand v1 wake]` and names `decision.answered`. The supervisor answers it, and Hand records `supervisor.delivered`. |
| `TestCodexLeavesAnotherRootsTrustScreenAlone` | A project registered through a linked worktree makes Codex's real screen name another repository root. Hand answers `trust: not pressed`, sends no key, records `attempt.blocked`, and the model is never asked. |
| `TestCodexWindowsRefusesACmdShim` (Windows only) | With only a `codex.cmd` on `PATH`, `hand supervisor start` and `hand attempt start` exit 2 with the batch-file message, and no supervisor exists. |

The mock log of each test lists every request, and a failing test also saves a dump (see [Debug a failing run](#debug-a-failing-run)).

## What stays manual

These tests do not replace the checks in [`dogfood/`](dogfood/README.md). They do not cover a real login or subscription, what a real model does with a briefing, usage limits, the Board in a browser, a phone, desktop notifications, `hand attach` in a real terminal, autostart at login, or `hand update` on a real machine. agy and opencode are not covered either.

## How the mock drives each harness

The mock answers by turn. A request carries the whole conversation, so the number of assistant turns in it picks the next step of the script: a step is a shell command to run, a text to say, or both. A request that offers no tools, such as the session title that each harness asks for, gets a fixed title. Every request is logged with its credentials masked.

- **Claude Code** sends `POST /v1/messages?beta=true` with `stream: true`. The mock answers with server-sent events: a `tool_use` of the `Bash` tool, then after the `tool_result` a text. Claude Code is configured in the throw-away `HOME`, because Hand scrubs `CLAUDE_CODE_*` variables from the Luvus server's environment: `~/.claude.json` marks onboarding done and approves the invented API key, and `~/.claude/settings.json` skips the bypass-permissions screen and sets, in its `env` block, `ANTHROPIC_BASE_URL`, `ANTHROPIC_API_KEY`, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`, `DISABLE_AUTOUPDATER`, `DISABLE_TELEMETRY` and `DISABLE_ERROR_REPORTING` (variables from Claude Code's [environment variable reference](https://code.claude.com/docs/en/env-vars), read on 2026-10-24). With them, a session sends nothing but `/v1/messages`.
- **Codex** sends `POST /v1/responses` over HTTP. A `config.toml` in the throw-away `CODEX_HOME` selects a custom provider with `wire_api = "responses"` and `supports_websockets = false`, and sets `daemon_auto_start = false`. The mock answers with a `function_call` of the `exec_command` tool (or `shell_command` on a version that offers that instead), then a message.

The tests leave the trust screens to Hand: neither harness's folder is trusted beforehand, so the real screens appear and Hand has to answer them.

## Run them on your machine

You need `git`, a real `claude`, a real `codex` and the tested Luvus on `PATH`, in this form:

- `claude` and `codex` as real executables, not a version manager's shim (a shim may start installing tools). On Windows, `claude.exe` and the standalone `codex.exe`: Hand refuses a `.cmd`.
- the Luvus version in `internal/luvus/pin.go` (`Tested`), so that `hand init` pins it instead of downloading it.

```sh
export HAND_E2E=1
export PATH="/path/to/claude/dir:/path/to/codex/dir:/path/to/luvus/dir:$PATH"
go test -count=1 -v ./internal/e2e/
```

Without `HAND_E2E=1` every scenario is skipped, so `go test ./...` stays fast and offline. To run one harness, add `-run '^TestCodex'` or `-run '^TestClaude'`, and to run one scenario, its name. The test builds `hand` itself and never touches your own `~/.claude`, `~/.codex`, fleets or Board: everything lives in a folder under the system temporary directory that the test removes. It stops the Luvus server and the watcher it started.

Set `HAND_E2E_ARTIFACTS=DIR` to keep the mock log of every scenario in `DIR/<test name>/mock.log`, and a dump `hand.txt` of the screens and state when a scenario fails.

The suite needs no terminal, display or network once the harnesses are installed.

## Pinned versions and how to bump them

`.github/e2e/versions.env` pins what the workflow installs: the Claude Code version, and the Codex version with the SHA-256 of its release archive for each of the three runners. The workflow installs the harnesses with `.github/e2e/install-claude.sh` (Claude Code's own installer, `install.sh` or `install.ps1`, given the version) and `.github/e2e/install-codex.sh` (the release archive of that Codex version, checked against the pinned SHA-256), then prints each version and path. Luvus comes from `.github/e2e/install-luvus.sh`, at the version in `internal/luvus/pin.go`.

To bump a harness:

1. Edit the version in `.github/e2e/versions.env`. For Codex, take the three digests from the release:

   ```sh
   version=0.164.0
   gh release view "rust-v$version" --repo openai/codex --json assets --jq '.assets[] | select(.name | test("^codex-(x86_64-unknown-linux-musl.tar.gz|aarch64-apple-darwin.tar.gz|x86_64-pc-windows-msvc.exe.zip)$")) | .name + " " + .digest'
   ```

2. Open a pull request. The workflow runs on a change to `.github/e2e/versions.env`, and each matrix leg prints the installed version.
3. If a test fails, read the dump first. A harness that rewords a trust screen is a change in `trustScreens` in `internal/cli/attempt.go` and in [`supervisor.md`](supervisor.md), and a changed request shape is a change in `internal/e2e/mockllm`.

## The workflow

`.github/workflows/e2e-harness.yaml` runs one job per harness and system: `claude` and `codex`, each on `ubuntu-latest`, `macos-latest` and `windows-latest`. No leg stops the others (`fail-fast` is off), and the permissions are `contents: read`.

| Trigger | Versions |
|---|---|
| a pull request that touches `internal/harness`, `internal/luvus`, `internal/cli/attempt*.go`, `internal/cli/supervisor*.go`, `internal/e2e`, the workflow itself, `.github/e2e/versions.env` or an install script | pinned |
| `workflow_dispatch` | the `versions` input: `pinned` or `latest` |
| the nightly schedule (02:23 UTC) | latest: the newest Claude Code and the newest Codex release, to see drift early |
| `workflow_call` | the `versions` input, `pinned` by default |

The `e2e harness` job at the end succeeds only when every leg did, so a caller can require it. `release.yaml` can run the suite before it publishes with `uses: ./.github/workflows/e2e-harness.yaml` and `with: {versions: pinned}`. A failing nightly run reports drift and blocks nothing, because a release calls the pinned suite.

Each leg uploads `e2e-<harness>-<system>`, with the `go test` log, and for each scenario its `mock.log` and, when it failed, `hand.txt`.

## Debug a failing run

1. Open the failing leg's log. The `go test -v` lines say which scenario and which assertion failed, with Hand's output.
2. Download its artifact: `gh run download RUN_ID --repo atqamz/hand -n e2e-codex-windows-latest`. `hand.txt` holds, from the moment of the failure: `hand orient`, `hand attempt list` and `show`, `hand attempt read`, `hand supervisor show`, the watcher's log, the events with their details, and for each pane Luvus's own `agent explain`, `pane processes` and `pane read`. `mock.log` shows each request the harness sent, so you can tell whether the briefing, the wake or a tool result reached the model.
3. Reproduce locally with the commands above, on the same versions: install them with the scripts, for example `CODEX_DIR=/tmp/codex .github/e2e/install-codex.sh 0.163.0` after `set -a; . .github/e2e/versions.env; set +a`, and run the one scenario with `HAND_E2E_ARTIFACTS=/tmp/e2e-out`.

## Known gaps

- Codex 0.164.0 stops on a `Background server has incompatible feature settings` screen, before its trust screen, when it starts with Hand's `--disable hooks` and no matching background server runs. The tests set `daemon_auto_start = false` in the throw-away `config.toml`, so they pass on 0.163.0 and 0.164.0 and do not exercise that screen.
- The tests start workers and supervisors with Hand's own commands. They do not send a worker a message with `hand attempt send`, which goes through the same Luvus prompt as a wake but is not run against the real harnesses.
