# Fake fidelity: recorded behaviour of the external tools

This package installs one stateful fake per external CLI, shared by every suite.
This file records what the real tool does for the calls the suite actually depends on, so each fake can be checked against a transcript rather than against somebody's memory of the tool.

Only the calls `hand` makes are recorded.
A behaviour no test exercises does not belong here.

Every entry was observed by running the real binary, not read off its documentation.
Versions probed: `treehouse v2.1.0`, `herdr 0.7.5`, `gh 2.97.0`.

The rule these records serve is SPECS.md's "Testing strategy": a fake that answers a state-changing command identically before and after that command cannot test anything about the state change.
So each record notes what the call leaves behind, not only what it prints.

## treehouse

Driven by `internal/worktree`, and through it by `hand spawn`, `hand promote` and `hand teardown`.

### `treehouse get --lease --json [--lease-holder <holder>]`

Exit 0.
A version banner goes to **stderr**, the JSON payload to **stdout**, which is why `worktree.Get` reads stdout alone - `CombinedOutput` here corrupts every parse.

```
{"path":"/home/atqa/.treehouse/secondhand-fcde6a/3/secondhand","lease_id":"5fe5412a4aabdeb85a148d6d73eb42d8","lease_holder":"hand:task-1","leased_at":"2026-08-04T09:12:31Z"}
```

`lease_id` is fresh on every acquisition, including a slot that was just returned and handed straight back out.
A treehouse older than v2.1.0 reports `path` alone with no identity, which stays a usable lease: `worktree.CheckCollision` falls back to comparing paths for it.

State left behind: the slot is leased and is not handed out again until it is returned.

### `treehouse get` with every slot leased or dirty

Exit 1, stderr:

```
all 4 worktrees are in use or dirty (max_trees = 4). Run 'treehouse status' to see details, or increase max_trees in treehouse.toml
```

### `treehouse return <path>`

Exit 0.
The success line goes to **stderr**, not stdout:

```
Worktree returned to pool.
```

State left behind: the slot flips from leased to available and **its directory stays in place**.
So no path-existence test can tell a returned worktree from a leased one, and nothing may infer "already returned" from the path being gone.

### `treehouse return <path>` repeated

Exit 0 again, same stderr line, with and without `--force`.
This idempotency is treehouse's own, and `hand teardown` depends on it: teardown returns the worktree before it removes the task's state, so a fault in a later step has to leave the whole sequence retryable.

### `treehouse return <path>` on a path no pool manages

Exit 1, stderr:

```
worktree /tmp/elsewhere is not managed by treehouse
```

### `treehouse return <path>` with uncommitted changes and no `--force`

**Exit 0**, with the return never performed.
treehouse prompts before cleaning a dirty worktree, nothing answers the prompt when stdin is not a terminal, and it aborts:

```
Worktree has uncommitted changes. Clean and return? [Y/n] Aborted.
```

The prompt and the abort both go to stderr.
State left behind: the slot is **still leased and still dirty**.

This is the one failure the exit status does not report, and it is why `worktree.Return` inspects the output for the abort instead of trusting the exit code.
Reporting it as success leaks the lease: the caller goes on to delete the task row that is the only remaining record of the holder.
`TestReturnFailsWhenAnUnforcedDirtyReturnAborts` is the check that fails if that guard is removed.

### `treehouse return <path> --force` with uncommitted changes

Exit 0.
The changes are discarded, the slot is freed, and the directory is left clean.

### `treehouse init`

Creates `treehouse.toml` in the working directory and reports the path it wrote on stdout.
Exit 1 with `treehouse.toml already exists` on stderr when one is already there.
