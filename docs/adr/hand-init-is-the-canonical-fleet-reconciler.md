# hand init is the canonical fleet reconciler

- Date: 2026-08-20
- Status: accepted
- Issues: atqamz/hand#218
- PRs: none

## Context

`hand init` already served as an idempotent initialization and migration boundary before this record: it seeds `data/**` skeleton files that are missing, restores `AGENTS.md` (see [AGENTS.md is fully Hand-owned and immutable](agents-md-is-fully-hand-owned-and-immutable.md)), now installs the bundled `secondhand` Agent Skill (see the skill-bundling PR in atqamz/hand#218's stack), migrates legacy worker settings, and runs `project.Migrate` to open the durable store and apply schema/legacy-registry migration. What it does *not* do had never been written down anywhere as a contract: a fleet home's `data/**` content beyond the skeletons, `config/**` values, registered projects, and Task/Attempt history all already survived every `hand init` call, but only as an emergent property of what the command happened not to touch.

## Decision

`hand init`'s contract is now explicit: initialize a new fleet home, or reconcile an existing one to the canonical layout the running binary expects. Every surface it touches falls into exactly one of two behaviors:

| Surface | Owner | `hand init` behavior |
|---|---|---|
| `AGENTS.md` / `CLAUDE.md` reference | Hand | restore canonical, byte-for-byte |
| bundled `secondhand` skill | Hand | install missing, refresh stale, refuse foreign |
| `data/operator.md`, `data/backlog.md`, `data/learnings.md`, `data/done-archive.md`, `data/note-archive.md`, `data/projects.md` | Supervisor/operator | create if missing, otherwise preserve |
| other `data/**` | Fleet | preserve |
| `config/**` | Operator, via `hand config` | preserve; migrate representation only |
| project registration | Fleet/runtime | preserve |
| `state/hand.db` (Task/Attempt history) | Hand runtime | schema-migrate, never reset |

Running `hand init` repeatedly is safe and idempotent: a home already on the canonical layout converges to the identical state on every subsequent call, and nothing durable it did not create is ever reset, regardless of how many times it runs.

This ownership split is enforced by what `hand init` calls, not by a new reconciliation engine: `agentsmd.Refresh` and `skill.Refresh` own the restore column, `initSkeletonFiles` only writes a file that does not already exist, `project.Migrate` opens the store and only imports a legacy registry once, and nothing in the command's `RunE` ever deletes or overwrites a project row, a config value, or a Task/Attempt record.

## Rejected alternatives

- A dedicated `hand reconcile-home` command separate from `init` duplicates the same idempotent-restore logic under a second name, and gives an operator two commands to remember for the same operation `hand init` already owns.
- Resetting `config/**`, project registration, or Task/Attempt history on `hand init` to guarantee a "clean" reconciliation destroys exactly the durable state the issue this record closes explicitly forbids resetting.
- Auto-classifying a fleet home's living `data/**` content into a fresh shape on every init treats memory as regenerable, which it is not: only the skeleton's *presence* is Hand's to guarantee, never its content once a human or supervisor has written to it.

## Consequences

An operator or supervisor can always run `hand init` to bring a fleet home's generated surfaces current without fear of losing configuration, project registrations, or task history - the same guarantee `hand update`'s direct self-update path depends on when it hands reconciliation to the newly installed binary. `cmd/init_reconciler_test.go` asserts each preserve-column of the table directly (project registration, config profiles/routes, Task/Attempt history) and that three consecutive `hand init` runs converge to the same steady state, so a regression that started resetting durable state on init fails a focused test instead of only surfacing as a field report.
