# Fake fidelity: recorded behaviour of the external tools

This package installs one stateful fake per external CLI, shared by every suite.
This file records what the real tool does for the calls the suite actually depends on, so each fake can be checked against a transcript rather than against somebody's memory of the tool.

Only the calls `hand` makes are recorded, plus the release writes `.github/scripts/edge-publish.sh` makes, which `tests/edgepublish` drives through the same fake.
A behaviour no test exercises does not belong here.

Every entry was observed by running the real binary, not read off its documentation.
Versions probed: `treehouse v2.1.0`, `herdr 0.7.5`, `gh 2.97.0`.

`tests/contract` re-runs these calls against the real tools under the `contract` build tag, skipping where a binary is absent, so a record that has gone stale against a newer tool is discoverable by running them.
It covers no call that would change anything an operator owns: a scratch treehouse pool and a scratch herdr workspace are created and destroyed, and every `gh` call is read-only.

Decorative glyphs appear in several stderr lines below and are omitted from the transcripts.
No matcher may depend on one.

A fake that answers a state-changing command identically before and after that command cannot test anything about the state change.
Each record therefore notes what the call leaves behind, not only what it prints.

## treehouse

Driven by `internal/worktree`, and through it by `hand spawn`, `hand promote` and `hand teardown`.

### `treehouse get --lease --json [--lease-holder <holder>]`

Exit 0.
A version banner goes to **stderr**, the JSON payload to **stdout**, which is why `worktree.Get` reads stdout alone - `CombinedOutput` here corrupts every parse.

```
{"path":"/tmp/pool/.treehouse/pool-52a8ff/1/pool","lease_id":"def81bb8adcc86f4c5d50233cf3ba0c7","lease_holder":"hand:task-1","leased_at":"2026-08-04T17:35:19.869690786+07:00"}
```

`lease_id` is fresh on every acquisition, including a slot that was just returned and handed straight back out.
`lease_holder` and `leased_at` are read by nothing in `hand`, so only `path` and `lease_id` are load-bearing.
An update-available notice shares stderr with the banner when one is due, which is the second reason nothing may read this call's output as a whole.
A treehouse older than v2.1.0 reports `path` alone with no identity, which stays a usable lease: `worktree.CheckCollision` falls back to comparing paths for it.

State left behind: the slot is leased and is not handed out again until it is returned.

When `origin` is configured, acquisition can fetch the remote and reset the leased worktree to the farther-ahead default-branch tip even while the registered clone's local default branch remains behind.
The acquired worktree's `HEAD` is therefore load-bearing state, not an incidental property of the returned path.
Mechanical dispatch verifies that `HEAD` still equals `planned_against` immediately after acquisition and returns a mismatched lease before any Herdr or worker launch.
`Treehouse.AcquireHeads` models this reset in the shared fake, and `tests/contract` verifies the behavior against the real binary.

### `treehouse status --json`

Exit 0 with a JSON array on stdout.
Each pool entry includes `path`, `status`, `lease_id`, `lease_holder` and `leased_at`.
Hand uses `path`, requires `status` to be `leased`, and compares `lease_id` exactly before retrying a forced return of a previously aborted lease.
An available entry has an empty `lease_id` and a null `leased_at`.
A leased entry from a backend without lease identities still has `status` `leased`, with an empty `lease_id`.
The command requires a git repository as its working directory.

State left behind: the command only observes the current pool state.
The lease identity is durable for the current acquisition and changes when a returned slot is handed out again.

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

Creates `treehouse.toml` in the working directory, reporting `Created <path>` on **stderr** with nothing on stdout.
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
A void command's error envelope carries `"id":"cli:request"` rather than a per-command id, so nothing may key on the id.
`pane read` is the one command whose success is bare text on stdout rather than an envelope.

Identifiers are assigned by herdr: workspaces `wX`, their tabs `wX:t1`, their panes `wX:p1`.

### `herdr workspace create --cwd <dir> --label <label>`

Exit 0.
The result carries the workspace, the root tab herdr creates with it, and that tab's root pane, all three in one response.
The root tab's label is `1`, not the label the workspace was given, which is why `hand spawn` renames it.
A workspace a human opens instead gets its label from its root directory's basename, so a directory named after a project produces a workspace whose bare label is the key `hand` searches on.

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

### `herdr pane get <id>`

Exit 0 with the pane, carrying `agent` (the detected harness name, empty when no harness runs in it) and `agent_status`, one of `working`, `idle`, `blocked`, `done`, `unknown`.
`pane_not_found` with exit 1 for a pane whose tab has been closed.

`idle` and `done` are one transition, not two states: when a pane goes from `working` or `blocked` to not-busy, herdr reports `idle` only if a live OS-focused client had that pane's tab active at the instant of the transition (its internal `seen` flag), and `done` otherwise.
`hand` polls the API and never focuses a client on a worker's pane, so it observes `done` essentially always for this transition and `idle` essentially never.
Neither value carries task-outcome information for a headless fleet; see `docs/adr/the-report-channel-is-the-only-outcome-signal.md`.

### `herdr pane read <id> --source recent --lines <n>`

Exit 0 with bare text, and **empty for a pane whose own shell has not painted yet** - a read taken immediately after `workspace create` returns nothing at all.
The two sources also disagree in that window: `visible` can carry the screen while `recent` is still empty.
They disagree on height too: `visible` answers the current viewport, 23 rows in an unattached session against 61 in an attached one, so anything painted above that window is absent from it while `recent` still carries it.
So a single read proves nothing about a pane, which is why `confirmLaunch` polls rather than reading once, and why the fake answering the same text on every read is only ever the settled case.

Text `pane run` typed into the pane appears in a later read, so a read reflects what herdr sent as well as what the command produced.

## gh

Driven by `internal/ghutil`, and through it by teardown's landed-work check and the gate's PR detection.

### `gh pr list --repo <slug> --head <branch> --state all --limit 200 --json number,url,state,headRepository`

Exit 0 with a JSON array on stdout, `[]` when nothing matches - an empty result is not an error.
`headRepository` carries `id`, `name` and `nameWithOwner`.

`--state all` is what makes a merged PR findable at all, and `--repo` narrows the search independently of `--head`: a fork project searches the upstream and its own fork for one branch and has to see a different answer from each.

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

Both merge entries were observed on a throwaway PR and are the one part of this file `tests/contract` does not re-run: merging is not reversible, and no contract run may land a real PR.

### Slug case

GitHub serves a repo under any casing of its slug and answers with the canonical one: `--repo AtqaMZ/Hand` succeeds and reports `"nameWithOwner":"atqamz/hand"`.
So `FindPRByBranch` folds case when comparing a `gh` answer against a git remote, and the fake matches `--repo` case-insensitively - a case-sensitive fake would answer a double search of one repo with one hit and hide the duplicate the real `gh` returns.

`internal/ghutil/pr_test.go` and `tests/e2e/slug_case_test.go` cover the comparison itself.
The latter's own `gh` fake is the one place a fake's fidelity had already been thought about; it is deliberately left as it is.

### `gh repo view <slug> --json nameWithOwner,url`

Exit 0 with a JSON object on stdout containing the canonical `nameWithOwner` and `url` fields.
The call follows a renamed repository: `atqamz/secondhand` answers `atqamz/hand` and `https://github.com/atqamz/hand`.

An unknown or inaccessible repository exits nonzero and writes its diagnostic to stderr.
`internal/ghutil` reads stdout separately from stderr so warnings cannot corrupt the JSON payload.


### `gh release view --repo <slug> --json <field> --jq <jq>`

The tag lookup omits the positional tag and requests `tagName`.
It answers the latest normal release; the prerelease `edge` release is never selected this way.

The notes lookup includes the release tag before `--repo` and requests `body`.
That tag is a stable release tag or the mutable `edge` tag.

Exit 0 with the selected field as raw text on stdout and no JSON envelope.
`hand update` reads `.tagName` and `.body` this way.

### `gh api repos/<owner>/<repo>/commits/edge --jq .sha`

Exit 0 with the full commit SHA alone on stdout when the mutable edge ref exists.
An absent edge ref exits nonzero and writes its diagnostic to stderr.
The self-updater treats the returned full SHA as the edge freshness identity.

### `gh release download <tag> --repo <slug> --dir <directory> --clobber --pattern <asset>...`

Exit 0 with the requested release assets copied into the directory.
Download progress belongs on stderr and is ignored by `hand update`.
The requested hand asset is `hand-<goos>-<goarch>.tar.gz` on Unix and `hand-windows-<goarch>.zip` on Windows, followed by `checksums.txt`.
The tag selects the same archive and checksum path for stable and edge releases.
Missing releases or assets exit nonzero and write a diagnostic to stderr without silently leaving a partial success.

## gh release writes

Driven by `.github/scripts/edge-publish.sh` alone.
`GHReleaseStore` models them as a mutable store so `tests/edgepublish` can assert the asset set a publish run leaves behind.

Observed with `gh 2.97.0` against disposable private repositories that the probe created and deleted in the same run, so no release an operator owns was written to.
`tests/contract` does not re-run them, for the reason the `pr merge` entries are not re-run: every call changes a release, and a contract run may change nothing an operator owns.
Re-probe the same way after a `gh` upgrade, because the script's ordering depends on each result below.

### `gh release create <tag> --repo <slug> --target <sha> --title Edge --prerelease --draft --notes-file <file>`

Exit 0.
Stdout is a release URL naming a placeholder rather than the requested tag: `https://github.com/<slug>/releases/tag/untagged-877fe33df70e192ddff9`.

The draft **reserves its tag name without creating the git ref**.
`gh api repos/<slug>/git/matching-refs/tags/<tag>` answered `[]` while the draft existed, so a draft's temporary tag never has to be deleted from the remote.

Repeating the call with a tag an existing draft already reserves also exits 0 and creates a **second** draft.
`gh release list --json tagName,isDraft` then reported two entries carrying that one tag name, which is why the script drains stale bootstrap drafts before creating its own.

### `gh release view <tag> --repo <slug> --json databaseId --jq .databaseId`

Exit 0 with the release id alone on stdout (`369645024`) for a draft.

This is why the script resolves the bootstrap release by id.
`gh api repos/<slug>/releases/tags/<tag>` answered `{"message":"Not Found","status":"404"}` and exit 1 for that same draft.

### `gh release list --repo <slug> --limit 100 --json tagName,isDraft`

Exit 0 with drafts included: `[{"isDraft":true,"name":"Edge","tagName":"edge-bootstrap-<sha>"}]`.

`databaseId` is **not** an available field, so a caller needing the id resolves the tag through `gh release view`.
Available fields are `createdAt`, `isDraft`, `isImmutable`, `isLatest`, `isPrerelease`, `name`, `publishedAt`, and `tagName`.

### `gh release delete <tag> --repo <slug> --yes`

Exit 0, deleting one release per call.
Run against a tag two drafts shared, two calls were needed and the third lookup answered empty.

### `gh release upload <tag> --repo <slug> <file>...`

Exit 0 with the assets attached to a draft under their base names, addressed by the draft's reserved tag.

Without `--clobber` an upload whose name is already taken exits 1 with `asset under the same name already exists: [hand-linux-amd64.tar.gz]` on stderr.
That is what makes staging names the safe way to land a candidate beside the previous set.

### `gh api --method PATCH repos/<owner>/<repo>/releases/assets/<id> -f name=<name>`

Exit 0 with the updated asset JSON on stdout, renaming the asset in place and keeping its id: `512416076` before and after.

A name already in use on the same release answers **HTTP 422** and exits 1, so a rename can never silently replace another asset.
The body carries `{"resource":"ReleaseAsset","code":"already_exists","field":"name"}` and stderr reads `gh: Validation Failed (HTTP 422)`.

### `gh api --method DELETE repos/<owner>/<repo>/releases/assets/<id>` and `.../releases/<id>`

Exit 0 with an empty stdout on 204.

### `gh api --paginate repos/<owner>/<repo>/releases?per_page=100` and `.../releases/<id>/assets?per_page=100`

Exit 0 with a JSON array on stdout, `[]` when nothing matches.

The release list **omits drafts**: with one draft present it answered `[]`, and the same call after publication listed the release.
A draft is therefore reachable only through `gh release list` or `gh release view`.
`--jq` renders with jq's own semantics, which is why the fake shells out to `jq` instead of reimplementing the programs the script relies on.

### `gh release edit <tag> --repo <slug> --tag <new> --title Edge --prerelease --draft=false --notes-file <file>`

Exit 0, retagging and publishing in one call, with `https://github.com/<slug>/releases/tag/edge` on stdout.

Publishing is what creates the git ref for a draft's tag.
Afterwards `matching-refs/tags/edge` held one ref and `matching-refs/tags/<bootstrap tag>` held none, so the assets are already in place when `edge` first resolves to anything and the temporary tag never reaches the remote.
