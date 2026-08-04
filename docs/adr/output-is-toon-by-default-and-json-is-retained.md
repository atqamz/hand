# Output is TOON by default and `--json` is retained unchanged

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#45, atqamz/secondhand#100
- PRs: atqamz/secondhand#155

## Context

`hand`'s only consumer is an LLM agent.
The output it shipped before this was a table aligned for a human terminal, which spends context on column padding that carries no information, and a `--json` flag for anyone who wanted structure.

TOON (https://axi.md) is the shape that fits the real consumer: a schema header naming the columns once, then one comma-joined row per item.
That makes the default an easy call.
What is not easy is what happens to `--json`, because it was already there and callers were already passing it.

`--fields <a,b,c>` arrived in the same change, narrowing a row block to the named columns in the order named.
It is defined against the TOON schema header, and JSON has no schema header to narrow.
So the two flags together are a request that cannot be honored as asked, and something has to be decided about it.

## Decision

TOON is the default output of every command, rendered through the single `internal/axi` renderer rather than per command.

`--json` is retained everywhere it already existed, byte for byte unchanged.
It is not deprecated, not reshaped to mirror the TOON blocks, and not warned about.

`--fields` together with `--json` is a usage error, exit 2, naming the reason: `--fields applies to the default TOON output, not --json`.

## Rejected alternatives

**Replace `--json` with TOON.**
Every existing `--json` caller is a script or a hook outside this repo, and none of them is in a position to be migrated by the same commit that breaks them.
The cost of keeping the flag is one branch at the end of each command; the cost of dropping it is a silent break in somebody else's tooling for a benefit the TOON default already delivers.
A caller that wants a parser-backed object should not have to parse TOON in order to build one.

**Make `--json` a reshaped mirror of the TOON document.**
That is the break above wearing a compatible-looking flag name, and it is worse: the caller gets valid JSON of the wrong shape and finds out at the field access rather than at the flag.

**Let `--fields --json` win by precedence, silently.**
Either direction is a lie about what ran.
Ignoring `--fields` returns every column to a caller that asked for three; ignoring `--json` returns TOON to a caller that asked for JSON and will hand it to a parser.
The request is not ambiguous, it is unsatisfiable, and the honest answer to an unsatisfiable request is exit 2.

**Narrow the JSON object by the same field names.**
It would work, and it makes `--fields` mean two different things: a schema-header contract in one mode and a key filter in the other.
The flag's whole definition is that the header narrows with the rows, and JSON has no header.

## Consequences

Every command has two output paths that both have to stay correct, and the TOON one is the one under test by default.

`rejectFieldsWithJSON` in `cmd/fields.go` is called by each command that accepts both flags, so adding `--fields` to a command means adding that call.
A command that forgets it accepts the combination silently, which is exactly the outcome this rejects.

The exit-2 refusal is a contract a caller can depend on, and it is in `SPECS.md`'s "Output shape" and exit-code table.
Softening it later to a precedence rule is a breaking change for any caller that treats exit 2 as a bug in its own invocation, which is what it is.
