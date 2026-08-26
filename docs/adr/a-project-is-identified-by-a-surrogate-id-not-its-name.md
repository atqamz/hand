# A project is identified by a surrogate id, not its name

- Date: 2026-08-26
- Status: accepted
- Issues: atqamz/hand#388, atqamz/hand#396, atqamz/hand#383
- PRs: atqamz/hand#399, atqamz/hand#400

## Context

A project's name was its only identity: the `project` table's primary key, the value every task row stored, and the field completion records matched on. Names are operator-facing and mutable, and `hand project remove` frees one while leaving the removed project's task rows and `state/completions.jsonl` records behind under it. A later project taking that name inherited them, indistinguishable from its own.

Rename made the same gap worse in a second way. It rewrote every task and every completion record whose stored name matched, which is a search across unrelated history rather than a change to one project, and its rollback re-ran that search backwards over records that may never have belonged to the rename at all.

## Decision

A project carries an opaque `p_`-prefixed id, minted when it is registered and never reissued. Tasks reference the id, completion records carry it, and `name` is only the live label: unique among registered projects, the key `data/projects.md` renders, and reusable once a project is removed.

Rename therefore relabels one row, found by id, and writes nothing else. A task's project name is resolved through its identity when it is read, with the name stored on the row kept only as the fallback for a project no longer registered, and a completion record keeps the label it was written with because an append-only audit line records what was true when it was written.

This removes the project-rename exception [Completions use an uncapped JSONL file](the-completion-store-is-an-uncapped-append-only-sibling.md) carved out of the append-only rule. What replaces it is not a wider licence to rewrite but a narrower one: a single one-time migration that stamps the record version onto every existing line, replacing the whole file through a temp file and a rename or leaving it exactly as it was.

Lineage that the old data cannot settle is marked, not guessed. A task naming no registered project keeps no identity; a completion record that neither its task row nor the live registry can place is written as `completion.ProjectIDUnknown`, which is deliberately visible rather than empty. This is the same fail-closed reading of unknown identity that atqamz/hand#384 applies to runtime identity.

[`internal/store/schemaversion.go`](../../internal/store/schemaversion.go) owns the schema migration, [`internal/completion/completion.go`](../../internal/completion/completion.go) the record format version and its one-time file migration, and [`internal/project/project.go`](../../internal/project/project.go) the resolution order between the two.

## Rejected alternatives

- Tombstoning a removed name reserves it forever, which makes names globally non-reusable without giving history a stable thing to point at.
- Purging a removed project's task rows and completion records contradicts the uncapped append-only completion store and would break `hand status` and `hand reopen` on terminal work.
- Rejecting any name a task or completion record has ever used is a scan that grows without bound and still cannot repair history already conflated.
- Using the repository URL as identity fails on transfers and renames, and `hand project set-url` exists precisely so a project survives its URL changing.
- Keeping the live name denormalized on every task row would make rename a multi-row write again, which is the shape both atqamz/hand#388 and atqamz/hand#396 came out of.

## Consequences

Adding a project surface means deciding what it keys on: the id for anything durable, the name only for what an operator types or reads. `state/completions.jsonl` now carries a record version, so any reader of that file has to tolerate both shapes and treat a missing `project_id` as version 1 rather than as an absent project. A completion record's `project` field is a historical label and can disagree with the project's current name; the id is what a reader joins on.
