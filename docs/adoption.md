# Secondhand adoption

How to get from a machine with a coding-agent harness to a ready-to-use Hand fleet, and the boundary between the surfaces that make that possible.

## Architectural boundary

Four surfaces, four questions, never blurred together:

```text
install.sh / install.ps1   -> "How do I install the hand binary?"
bootstrap.sh / bootstrap.ps1 -> "How do I make this machine ready to use Secondhand?"
hand init                  -> "Initialize or reconcile a fleet home."
hand doctor                -> "Is this fleet ready, and what is blocking it?"
```

`install.*` (owned by [atqamz/hand#177](https://github.com/atqamz/hand/issues/177)) installs `hand` only.
It never installs Treehouse, Herdr, `gh`, a coding-agent harness, or no-mistakes.

`bootstrap.*` is optional and explicitly opt-in.
It may install missing foundational dependencies (Git, Treehouse, Herdr) with consent, because the operator explicitly chose an adoption workflow by running it.
It never installs a coding-agent harness or no-mistakes: those require account, provider, and authentication choices only the operator can make.

`hand init` is the only fleet initialize/reconcile operation.
Bootstrap calls it; it never reimplements fleet setup in shell or PowerShell.

`hand doctor` is the only readiness and diagnostic authority.
Bootstrap, a human, and a supervising agent all read the same structured output rather than a second schema invented in a script.

## Fastest path

```sh
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/atqamz/hand/main/bootstrap.sh | sh -s -- --yes
```

```powershell
# Windows
irm https://raw.githubusercontent.com/atqamz/hand/main/bootstrap.ps1 -OutFile bootstrap.ps1
Set-ExecutionPolicy -Scope Process Bypass
.\bootstrap.ps1
```

Both scripts acquire `hand` if it is missing, detect and offer to install missing foundational dependencies, choose a safe fleet-home target (default `~/secondhand-fleet`), run `hand init` and `hand doctor`, and print the exact next command:

```text
Secondhand is ready.

Next:

  cd ~/secondhand-fleet
  claude
```

If no supported coding-agent harness is installed, the printed next step says so instead of guessing one.

Flags, identical in effect on both platforms:

| Shell | PowerShell | Effect |
| --- | --- | --- |
| `--fleet PATH` | `-Fleet PATH` | fleet home to create or reconcile (default `~/secondhand-fleet`) |
| `--yes` | `-Yes` | explicit non-interactive consent to install missing foundational dependencies |
| `--check` | `-Check` | read-only: report readiness, install or mutate nothing |

A target that already exists, is non-empty, and is not a recognized Hand fleet is refused rather than silently adopted.

## Install `hand` only

Already have Git, Treehouse, Herdr, and a coding-agent harness, and only want the binary?
See [Installation](../README.md#installation) in the README for Homebrew, Nix, `go install`, the install script, and release binaries.

## Manual adoption

For operators who prefer to run each step themselves, using whichever package manager they already trust:

```sh
# install git, treehouse, herdr, and a coding-agent harness with your preferred package managers
mkdir -p ~/secondhand-fleet
cd ~/secondhand-fleet
hand init
hand doctor
```

## Agent-assisted adoption

For an operator who already has a capable coding agent and prefers to delegate setup, a small prompt is enough - the immutable, Hand-owned `AGENTS.md` and bundled `secondhand` skill that `hand init` installs teach the newly opened supervisor how to operate Hand from there:

```text
Set up Secondhand from atqamz/hand on this machine using its documented
bootstrap workflow. Inspect first, explain any third-party installations
before performing them, preserve existing credentials/configuration, and
finish by running hand doctor.
```

If adoption ever needs a longer, more specialized prompt than this, that is a signal to improve the bootstrap scripts or `hand` itself, not to grow the prompt.

## Consent and security model

Bootstrap runs at a high-trust boundary and is deliberately conservative:

- Every missing foundational dependency is listed with its source and install method before anything runs; `--yes` is explicit non-interactive consent, and a non-interactive invocation without it declines rather than guessing.
- `--check` is a pure read-only dry run; it never installs or mutates anything, including the fleet target.
- A dependency fetched from a URL is downloaded to a file and its download verified before that file is ever run - never `curl | sh` or `irm | iex` piped directly, which would treat a failed or interrupted fetch as an empty, successful script.
- `hand` itself, when bootstrap must acquire it, is verified against the same checksummed GitHub release artifact `install.sh`/`install.ps1` use.
- No step escalates privilege for the whole run; a package-manager action that needs it is scoped to that action alone.
- Bootstrap never asks for provider tokens, never prints an existing token, and never dumps the environment.
- A coding-agent harness and no-mistakes are only ever detected, never installed - account, provider, and authentication choices stay the operator's.

## Readiness contract

`hand doctor` exposes the same readiness contract bootstrap reads, so a human or a supervising agent gets identical answers:

```text
tools[4]{tool,installed,required}:
  git,true,true
  treehouse,false,true
  herdr,true,true
  gh,false,false
harnesses[5]{name,installed}:
  claude,true
  codex,false
  grok,false
  pi,false
  opencode,false
ready: false
blocking[1]:
  - treehouse
next[1]:
  - install treehouse
```

`git`, `treehouse`, and `herdr` are always required; `gh` is required only when a registered project's delivery mode is `direct-pr` or `no-mistakes`.
Harnesses are reported purely as installed or not - `hand` never picks one on the operator's behalf.

## Idempotency and recovery

Repeated bootstrap runs are safe:

| Fleet target state | Bootstrap behavior |
| --- | --- |
| absent | created and initialized |
| empty directory | initialized |
| existing valid Hand fleet | reconciled by `hand init`, no destructive rewrite |
| existing, non-empty, not a recognized fleet | refused |
| already healthy | reports ready, mutates nothing |

A partial failure is never reported as success.
If a dependency install fails or `hand init`/`hand doctor` reports a blocker, bootstrap exits non-zero with the exact recovery command - typically resolving the reported blocker, then rerunning `hand doctor` or the bootstrap script itself.
