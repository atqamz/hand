# Preparing a bug report

Preparing a report and publishing one are two different steps. Gathering diagnostics never
implies filing anything; only create a GitHub issue when the operator explicitly asks you to
publish it.

## What to gather

```text
hand --version
hand doctor
hand config
hand status <task>          # only when the report is about a specific task
OS and architecture
the exact failing command
its exit kind (from the error document's `kind` field, not just the message text)
stderr/stdout, as the structured document hand printed
expected behavior
actual behavior
a minimal reproduction
```

Prefer the exact structured output over a paraphrase: paste the real TOON document (or its
`error`/`kind`/`exit` fields on failure) rather than describing it in your own words, since a
paraphrase can drop the one detail that explains the bug.

## Hard privacy rules

- Never dump all environment variables. If an environment variable is genuinely relevant, name
  that one variable and its value, not the whole environment.
- Never print tokens or credentials, from any source - environment, config, or a file.
- Never read arbitrary credential files to "check" something for a report.
- Never include secrets from a brief or a config file in the report.
- Redact a sensitive path when it is not necessary to reproduce or diagnose the bug - a fleet
  home's location is rarely sensitive, but a project's private clone URL or an operator's local
  username in an unrelated path may be worth redacting.

## Assembling the report

Structure it as: what you expected, what actually happened, the minimal reproduction, and the
diagnostics gathered above. Do not speculate about root cause beyond what the evidence actually
shows - a bug report that already contains an unverified guess dressed up as a finding wastes
the maintainer's time confirming or refuting it.

## Publishing

A prepared report stays local until the operator explicitly asks for it to be published. When
asked, file it with `gh issue create` against the correct repository, following this fleet's own
GitHub conventions for issue bodies (`data/operator.md` and the fleet's own `AGENTS.md`/CLAUDE.md
conventions govern this, not this skill).
