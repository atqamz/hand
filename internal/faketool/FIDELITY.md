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

State left behind: `treehouse.toml` is **untracked and excluded nowhere**.
`git status --porcelain` in the clone reports `?? treehouse.toml` from then on, and nothing is added to `.gitignore` or `.git/info/exclude`.
Excluding it is therefore `hand project add`'s job, which `excludeLocally` in `cmd/project.go` does; without that every later `hand project sync` reports `skipped (dirty working tree)` for the project it just registered.
`TestProjectLifecycle` is the check that fails without it.

## herdr

Driven by `internal/herdr`, and through it by every command that touches a task's tab.

Recorded in a scratch workspace created for the purpose and closed again, not in the operator's own.
Two behaviours below could not be observed without creating and destroying a real workspace, which is why the fake models them rather than guessing: closing a sole tab, and what happens to a closed workspace's tabs and panes.

### Success and failure shapes

A query command answers a JSON envelope on **stdout** with exit 0:

```
{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":[...]}}
```

A command naming something that does not exist answers an error envelope on **stderr** with **exit 1**, and writes nothing to stdout:

```
{"error":{"code":"tab_not_found","message":"tab wY:t2 not found"},"id":"cli:tab:close"}
```

The codes observed are `workspace_not_found`, `tab_not_found` and `pane_not_found`.

`pane run`, `pane send-text` and `pane send-keys` are void: empty stdout on success, and the same error envelope on stderr with exit 1 for a pane that is gone.
`pane read` is the one command whose success is bare text on stdout rather than an envelope.

Identifiers are assigned by herdr: workspaces `wX`, their tabs `wX:t1`, their panes `wX:p1`.

### `herdr workspace create --cwd <dir> --label <label>`

Exit 0.
The result carries the workspace, the root tab herdr creates with it, and that tab's root pane, all three in one response.
The root tab's label is `1`, not the label the workspace was given, which is why `hand spawn` renames it.

State left behind: the workspace is listed by `workspace list` from this point on.

### `herdr tab close <id>` on a workspace's only tab

**Exit 0, and it takes the workspace with it.**
Nothing refuses the call.
Afterwards `tab list --workspace <ws>` is `workspace_not_found`, `pane get` on its pane is `pane_not_found`, and the workspace is gone from `workspace list`.

`closeTaskTab` still issues `workspace close` explicitly for a sole tab: the end state is the same either way, and saying so beats depending on a side effect.

### `herdr tab close <id>` repeated

The second close is `tab_not_found` on stderr with exit 1.
Nothing in `hand` may rely on a repeated close succeeding.
This is what makes `closeTaskTab`'s absent-tab guard load-bearing, and `TestCloseTaskTabRerunLeavesASharedWorkspaceAlone` is the check that fails without it.

### `herdr workspace close <id>`

Exit 0.
State left behind: the workspace, every tab in it and every pane in those tabs are gone.
Each answers its own `*_not_found` afterwards, and a repeated `workspace close` is `workspace_not_found` with exit 1.

### `herdr tab list --workspace <id>`

Exit 0 with the workspace's live tabs in creation order, each carrying its current label.
A closed workspace is `workspace_not_found` with exit 1, not an empty list - the distinction a rerun of teardown actually meets.

### `herdr tab create --workspace <id> --cwd <dir> --label <label>`

Exit 0, with the new tab and its root pane.
`workspace_not_found` with exit 1 once that workspace has been closed.

### `herdr tab rename <id> <label>`

Exit 0, and the new label is what `tab list` reports from then on.
`tab_not_found` with exit 1 for a tab that has been closed.

## gh

Driven by `internal/ghutil`, and through it by teardown's landed-work check and the gate's PR detection.

### `gh pr list --head <branch> --json number,url,state,headRepository`

Exit 0 with a JSON array on stdout, `[]` when nothing matches - an empty result is not an error.
`headRepository` carries `id`, `name` and `nameWithOwner`.

### `gh pr view <url-or-number> --json state`

Exit 0 with the JSON object on stdout.
A PR that does not exist is exit 1 with a GraphQL error on stderr.
Real `gh` also writes warnings to stderr ahead of the JSON, which is why callers read stdout alone.

### `gh pr checks <url-or-number> --json bucket`

Exit 0 with every check's bucket on stdout when they all pass.
The exit code carries the verdict too: **1** when any bucket is `fail`, **8** while any is still pending.
`prChecksGreen` reads the buckets and deliberately ignores the exit code once the JSON parses, so the fake reports buckets at exit 0 and still exercises the path a nonzero exit would.
The buckets `hand` treats as green are `pass` and `skipping`.

### `gh pr merge <url-or-number> --squash|--merge|--rebase`

Exit 0, with the merge reported on stdout.

State left behind: `pr view` answers `MERGED` from this point on, and so does `pr list` for the branch.
This is the transition `hand merge` and `hand teardown` are sequenced around - merge lands the PR, and teardown's landed-work check reads the state merge left.

### `gh pr merge` on an already-merged PR

**Exit 0**, with nothing on stdout and a warning on stderr:

```
! Pull request #42 was already merged
```

So the exit status cannot say whether a merge happened, and a rerun of `hand merge` would report success for a merge it did not perform.
`runPRMerge` checks `PRIsMerged` before the merge for that reason, and `TestMergeRefusesAPRAnEarlierRunAlreadyMerged` is the check that fails without it.

### Slug case

Repository slugs from `gh` do not agree with git remotes on case, and `FindPRByBranch` folds case when comparing them.
That behaviour is covered by `internal/ghutil/pr_test.go` and `tests/e2e/slug_case_test.go`, whose own `gh` fake is the one place a fake's fidelity had already been thought about; it is deliberately left as it is.
