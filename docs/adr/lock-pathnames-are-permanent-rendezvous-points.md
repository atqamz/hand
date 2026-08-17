# Lock pathnames are permanent rendezvous points

- Date: 2026-08-17
- Status: accepted
- Issues: atqamz/hand#224, after atqamz/hand#194
- PRs: atqamz/hand#239

## Context

`internal/store/lock.go:24-43` maps a logical lock key to a stable pathname: `sha256(key)` becomes
`state/.<64hex>.lock`, opened with `O_CREATE|O_RDWR` and locked with `internal/filelock`. The mapping is
one-way and permanent. No code path in the tree unlinks a hashed lock file, and the two wrappers above
it, `internal/state/task.go`'s `Lock`/`TryLock`/`Claim` and `internal/routing/lock.go`'s `withLock`,
inherit that behavior rather than overriding it.

A long-lived fleet home accumulates hundreds of these files. A census of `/home/atqa/hand-fleet/state/`
on 2026-08-17 found 254 hashed lock files, all zero bytes, spanning 2026-07-27 to 2026-08-17: 139
`task:<id>`, 62 `send:<taskID>`, 30 `worktree:<path>`, 11 `project:<name>`, and 4 shared by the global
constants `config:routing`, `completions`, `migration`, and `schema`. That is the field evidence behind
atqamz/hand#224. The growth law is reassuring rather than alarming: the namespace grows with distinct
task identity and distinct worktree path, not with attempt or send count, because no `attempt:` lock key
exists anywhere in the tree and `send:` is keyed by task ID rather than send ID. Measured growth was 12.1
files per day, 1.81 per task identity, over the 21 days sampled.

atqamz/hand#194 made the durable lifecycle half of several of these critical sections redundant: its
conditional SQL transitions and its unique-active-attempt constraint mean a stale caller loses in SQL
rather than needing a lock to have excluded it beforehand. It did not make any lock removable, because
every one of the nine logical lock families audited still encloses at least one call site with an
external side effect SQLite cannot see: a `gh` call, a Treehouse lease operation, a Herdr pane send, a
`git checkout`/`git merge` against the shared clone, an `O_APPEND` write to `completions.jsonl`, a
multi-statement schema migration across a whole `store.Open`, or a readdir-then-insert-then-archive sweep
over legacy JSON. `internal/store/store.go:1230-1233`'s guarded active-attempt check and
`internal/state/task.go`'s conditional transitions removed the lock's durable-lifecycle burden without
removing the lock itself.

## Decision

The pathname for a logical lock key is permanent for the life of the fleet home. It is created on first
acquisition and is never unlinked, by any caller, for any reason. Zero-byte size and an old modification
time carry no information about whether the lock is currently held. The only authoritative signal of
ownership is the kernel lock itself, `flock` on unix or `LockFileEx` on windows, which is released when
every file descriptor or handle referencing it closes: on `internal/store/lock.go:39-42`'s ordinary
release path, or on process death.

This holds for all nine audited lock families, all classified `retain`:

- `task:<id>` and `send:<taskID>` are `retain`, high-cardinality: one pathname per task identity, plus
  one more if that task ever received a send. They serialize `gh`, Treehouse, Herdr, and `git` operations
  a stale-loses SQL transition cannot order, and `PaneSendText` followed by `PaneSendKeys Enter` is a
  two-call external mutation no transaction can enclose.
- `project:<name>` and `worktree:<path>` are `retain`, bounded/global in practice: bounded by the
  operator's project list and by Treehouse's slot reuse, respectively. They serialize `git` and lease
  operations against a physical directory SQLite has no view of.
- `config:routing`, `completions`, `migration`, `schema`, and the project registry projection
  (`data/projects.md.lock`, a fixed sibling pathname outside the hashed namespace) are `retain`,
  bounded/global: exactly one pathname each, forever. Each serializes a read-modify-write or a
  multi-statement sequence over a plain file or a whole `store.Open` that SQLite's per-statement locking
  cannot make atomic as a unit.

`internal/store/lock.go:21-23`'s existing comment explains why file locks exist at all next to SQLite.
This record is the answer to the different question of why the resulting pathname outlives every holder.

Namespace growth was measured, not assumed. The one in-tree cost that scales with `state/` cardinality is
`legacyTaskFiles`'s `os.ReadDir` (`internal/store/migrate.go:20-34`), run on every `store.Open`. Measured
at 94.5 microseconds for 265 entries and 28 milliseconds for 100,000, linear at about 0.33 microseconds
per entry. At the measured 12.1 files/day, reaching 10,000 entries (the first user-perceptible effect)
takes about 2 years, and 100,000 (the first effect worth engineering against) takes about 22 years. Lock
acquisition itself is O(1) in namespace size: `store.Lock` computes its target path directly from the
key and never enumerates `state/`.

No sweeper is introduced. Reclaiming a rendezvous pathname would require proof that no current or future
process can still hold or open the old inode, and no such proof exists for a filesystem shared across
independently-started `hand` processes.

## Rejected alternatives

- **Unlink the lock file on unlock.** A live rendezvous pathname is a shared identifier between
  processes that hold no other connection to each other. Unlinking it on ordinary release admits a
  split-inode race:

  ```text
  process A holds/opens old inode
  process B opens old inode
  A unlocks + unlinks pathname
  process C recreates same pathname -> new inode
  B locks old inode
  C locks new inode
  -> two processes both believe they own the same logical lock
  ```

  This is unsafe on unix, where `flock` ownership belongs to the open file description and resolves to
  the inode, so a later `O_CREATE` genuinely produces a different inode under the same name. It is not a
  workable substitute on windows either: `LockFileEx` ownership belongs to the handle, and windows
  normally refuses to delete a file with an open handle, so the same "cleanup" fails loudly instead of
  silently splitting. Neither platform makes the idea correct; one makes it dangerous and the other makes
  it inert.

- **A periodic sweeper for old `.lock` files.** An old modification time is not evidence that a lock is
  unheld; every hashed lock file in the fleet home is zero bytes and has been since creation, and a
  process can hold one for the duration of a `gh pr merge` or an 8-iteration reconciliation loop. A
  sweeper has no portable way to prove a pathname's inode is unopened everywhere, so it is the split-inode
  race in the first alternative with a longer fuse rather than a fix for it.

- **A bounded namespace, for example one lock per project instead of per task.** This would shrink today's
  254 files to roughly 15, but at the cost of serializing the fleet's actual purpose. The `task:<id>` lock
  is held across a 30-second `gh` timeout and a full provisioning sequence including a Herdr pane launch;
  coarsening it to a project-scoped lock would make one task's `hand merge` block an unrelated task's
  `hand teardown` in the same project for the duration of a network call, which already happens
  concurrently in this fleet home today. It would also remove the disjointness that keeps the current
  lock order deadlock-free (`task:a` never waits on `task:b`) and would turn `state.Claim`'s
  `ErrTaskActive` from "that task is in use" into "something in this project is busy", a worse and
  API-breaking error for the operator. Not worth trading a measured 94.5-microsecond `os.ReadDir` for
  fleet-wide serialization and a new deadlock surface, at 254 files, at 10,000, or at 100,000.

- **Replace the file locks with SQLite transactions.** Rejected per family: every retained family has at
  least one call site with an external side effect while the lock is held. `send:<taskID>` is the
  clearest case, since no transaction can enclose `PaneSendText` followed by `PaneSendKeys Enter` against
  a live terminal composer; see
  [Steering records terminal submission uncertainty](steering-records-terminal-submission-uncertainty.md).

- **A generic lock service or distributed lock manager.** Explicit non-goal of atqamz/hand#224, and
  unjustified for a single-host fleet home where a hashed pathname under `state/` already gives every
  process the same rendezvous point for free.

This record does not restate or amend
[One flock owns watching for a fleet home](one-watcher-per-fleet-home-guarded-by-an-flock.md), which
covers the separate `watch.pid.lock` singleton and is atqamz/hand#232's territory.

## Consequences

A long-lived fleet home accumulates roughly 1.8 zero-byte lock files per task identity, on the order of a
dozen per day at typical fleet activity. This is the designed behavior of a permanent rendezvous
namespace, not a leak or an incident. `internal/store/lock_test.go` pins the pathname-derivation and
persistence contract this record describes, so a future refactor that tries to "clean up" the namespace
fails a test before it fails in production.

The one cost that scales with namespace size is `legacyTaskFiles`'s `os.ReadDir` on every `store.Open`.
Its threshold is about 10,000 entries for a first perceptible effect and about 100,000 for one worth
engineering against; at the measured growth rate those are roughly 2 and 22 years out. If that threshold
is ever approached, the correct remedy is to gate the legacy readdir on a persisted "no legacy files
remain" fact rather than to delete or reclaim lock files. That is a distinct piece of work with its own
crash-safety questions and is not undertaken here.

Any future tool that enumerates `state/` (a support bundle, a doctor check) should skip `.*.lock` rather
than treat the count as an anomaly. Any future lock key shape added to the tree should have its
cardinality considered before it lands; `TestEveryLogicalKeyShapeInTheTreeIsCovered` in
`internal/store/lock_test.go` is the tripwire that sends the author here.
