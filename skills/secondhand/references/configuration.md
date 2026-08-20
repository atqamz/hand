# Profiles and routes

## Read hand config first, always

`hand config` reports the fleet's effective worker configuration: the detected supervisor
harness, the `harnesses` table (supported harnesses, each with its own `installed`,
`model`-capable, and `effort`-capable columns), configured `profiles`, configured `routes`, and
any `problems`.

```text
wrong: assuming a harness is usable because Hand supports it
right: reading the harnesses table's own installed column for that harness
```

`installed` is about this environment's `PATH`, not about Hand's support matrix; a harness can
be supported and not installed, or installed and not model/effort-capable. Never conflate the
three.

## Profile and Route, precisely

A **Profile** is an operator-named bundle: a harness, plus an optional model and effort
override. A **Route** maps one `(kind, execution class)` pair - `scout`/`ship` crossed with
`mechanical`/`standard`/`deep` - to exactly one Profile. `hand config` reports every route,
including the ones still `missing`.

Profile names, model identifiers, and which routes exist are entirely operator-defined. Do not
hard-code a mapping such as "mechanical uses model X, deep uses model Y" anywhere, including in
a brief: that is a policy decision for the operator to make once, through `hand config`, not a
default this skill or a supervisor session invents per dispatch.

## When a Profile or Route is missing

Classify the task's execution class first (see `references/planning-and-briefs.md`), then check
whether a route already covers it. If `hand config`'s `routes` table shows that pair as
`missing`, or `problems` names it:

```text
wrong: "I'll just use claude-sonnet-5 for this, that's usually the good default"
right: propose a structural option - "no route exists for ship/standard; here are the
       supported, installed harnesses; which should the ship/standard profile use?" - with no
       claim about which harness or model is better, cheaper, or higher quality
```

Ask the operator only the genuinely unresolved policy question: which harness, and whether a
model/effort override is wanted. Do not ask for confirmation on anything routine, and do not
present entitlement, quality, or cost claims about any harness or model - you do not have
grounds to make them, and the operator does.

Persist an accepted choice immediately through the CLI, never by writing a config file by hand:

```sh
hand config profile set <name> --harness <harness> [--model <model>] [--effort <effort>]
hand config route set <kind> <execution-class> <profile>
```

Then run `hand config` again to confirm the route now resolves and no new `problems` appeared.

## Routine vs operator-owned

Classifying the task's kind and execution class is routine: do it yourself, every time, without
asking. Whether a Profile/Route already exists and resolves is a fact you read, not a judgment
call. The only thing that is genuinely the operator's to decide is *which* harness/model a new
Profile should use when none of the existing ones fit - ask exactly that, once, and move on.
