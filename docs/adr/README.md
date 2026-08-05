# Architecture decision records

This directory keeps rationale for durable architectural boundaries that are easy to reverse accidentally.

Behavioral contracts belong with their implementation, command help, and focused tests. External-tool observations belong in `internal/faketool/FIDELITY.md`. User and contributor workflows belong in README.md, AGENTS.md, or CONTRIBUTING.md. Issues and pull requests retain incident history.

## When a record belongs here

Keep an ADR only when all three are true:

1. It describes a stable architectural boundary rather than current implementation detail.
2. It rejects a realistic alternative a future contributor might reintroduce.
3. Its consequences are not already expressed adequately by local code and tests.

Each record uses one file with a present-tense title, date, status, relevant issues and pull requests, then Context, Decision, Rejected alternatives, and Consequences. Link directly to the owning code or tests where that helps navigation without restating their contract.

`none` means no reference exists. `none single` means the decision accumulated across changes and no one reference represents it.

Do not number records or maintain a duplicate index. Filenames are descriptive slugs.

## Later changes

An accepted record describes the decision made on its date. Reversing the boundary requires a new record that names the earlier one; update the earlier status to point to its replacement without rewriting its historical reasoning. Typo and broken-link fixes are fine.
