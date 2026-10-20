# Safe use

Every worker runs with its permission prompts skipped (see [The supervisor](supervisor.md)), as the operator's own OS user. It can read whatever that account can: `~/.ssh`, `~/.config/gh`, `~/.gnupg`, other fleets and each fleet's `board.token`. A worktree is not a fence, and Hand builds no sandbox. A worker that obeys text planted in an issue, a PR or a review can use all of it. The protection is the setup around Hand:

- **Own account.** Run Hand as a dedicated OS user, or in a VM, with its own `~/.ssh` and `gh` login and none of your other directories.
- **Narrow token.** Log that account in to GitHub with a fine-grained personal access token limited to the repositories in use, with the Contents and Pull requests permissions and an expiry date. GitHub does not require an expiry, so set one. Do not use a broad `gh auth login`.
- **Own signing key.** Sign with a key used only by that account, ideally a FIDO key (`ssh-keygen -t ed25519-sk`) that needs a touch for each signature. An unlocked signing agent signs whatever a process that can reach it asks.
- **Protected default branch.** Require a pull request and refuse force-pushes, so a hijacked worker can propose a change but not land one. GitHub exempts repository admins from these rules unless "Do not allow bypassing the above settings" is on, so give the account write access rather than admin, or turn that setting on. Keep the account, and any role, team or app it belongs to, off every bypass list in branch protection and rulesets.
- **Clean environment.** Start fleets from a shell with no cloud or API secrets. Hand drops only `HAND_HOME` and the Claude, Codex and Luvus variables when it starts the Luvus server, and its panes inherit the rest of the environment.
- **Review and rotate.** Keep production credentials off the machine, read worker PRs before you merge them, and rotate tokens after a suspicious run.
- **Private board.** Keep the board on loopback and reach it with `ssh -L` instead of exposing it.
- **Worker reach.** Every pane's environment carries `LUVUS_SOCKET_PATH` and `LUVUS_API_ADDRESS`, so a worker can reach the fleet's Luvus socket, list the panes and send keys or prompts to the others, the supervisor's included. Hand does not prevent this, so the worker boundary is the account separation above, not a Hand control.
