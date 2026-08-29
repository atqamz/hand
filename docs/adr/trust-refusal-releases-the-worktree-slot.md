# Trust refusal releases the worktree slot

- Date: 2026-08-29
- Status: accepted
- Issues: atqamz/hand#491
- PRs: none

## Context

Codex and Claude can stop a launch on a first-run security prompt that Hand must not answer.
The refusal needs to identify the concrete checkout so an operator can act on it.
The launch can already have acquired a Treehouse worktree before the prompt is observed, and the
normal failed-launch unwind currently returns that worktree and clears its persisted ownership.

Holding the slot would preserve a durable path for `hand status`, but a prompt may never receive an
operator response and a held pool slot would then leak indefinitely.

## Decision

Trust refusals include the launch specification's concrete checkout path at the refusal call site.
The prompt catalogue remains static and continues to contain only harness-specific security guidance.
The existing unwind returns the acquired worktree and clears its persisted ownership after the error
is composed, so the refusal itself carries the complete path needed for operator action.

## Rejected alternatives

- Holding the worktree until an operator acts would retain status evidence, but would require a new
  release lifecycle and could strand a scarce pool slot indefinitely when the prompt is ignored.
- Putting a path placeholder in the prompt catalogue would turn static security-prompt signatures
  into a formatting template and couple harness definitions to runtime state.
- Keeping the slot without naming the path would preserve an inaccessible record and would not fix
  the immediate refusal message.

## Consequences

The launch error names the exact checkout for both managed-settings and directory-trust refusals,
and the existing cleanup remains bounded and leak-free.
After cleanup, `hand status` no longer retains the released path, so the error must be preserved by
the operator while acting on the checkout.
