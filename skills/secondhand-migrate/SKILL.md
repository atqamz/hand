---
name: secondhand-migrate
description: Move the Hand 0.7 fleet in this folder onto the new Hand. Read the old fleet, propose what to rebuild, stop for the operator's approval, then rebuild it with `hand` commands only. Use only while the folder still holds 0.7 data (state/hand.db, data/, config/).
metadata:
  managed-by: hand
---

# Migrate a Hand 0.7 fleet

This folder holds a Hand 0.7 fleet, and `hand init` has already made it a new fleet as well. Your job is to carry what still matters into the new fleet. Nothing is converted automatically: you read the old files, the operator decides, and you rebuild through `hand` commands.

## First: the 0.7 wiring

If `.claude/settings.json` still has a Stop hook with the arguments `supervision claude-stop`, or if `.pi/extensions/hand-*` or `.opencode/plugins/hand-*` still exist, they keep waking you with 0.7 errors. Before anything else, show the operator exactly those entries and files, and remove them once the operator approves. `hand init` warns about them too.

## Rules

- Change nothing before the operator approves your proposal. Reading is free; writing waits.
- Rebuild only through `hand` commands. Never write `hand.db`, and never query the 0.7 database `state/hand.db`.
- Move or delete 0.7 files only in the archive and cleanup steps, and only with approval.
- When a 0.7 fact is unclear, say so in the proposal instead of guessing.
- If the operator kept the old binary (for example as `hand-0.7`), you may use its read-only commands. Otherwise read the files.
- The migration itself is not captured as a task, even though the `secondhand` skill asks you to capture every request first. `hand project add` has no undo, so run it only for approved projects.

## 1. Inventory (read-only)

Read and summarise:

- **Operator memory:** `data/operator.md` (standing constraints) and `data/learnings.md` (lessons).
- **Projects:** `data/projects.md` has lines like `- name: git-url mode=…`. The clones are in `projects/<name>/`.
- **Queue:** the items under `## Queue` in `data/backlog.md`. List the headings under `## In Progress` as `ask`. Ignore the rest of the file unless an item points to it.
- **Tasks:**
  - `state/<task>.status` is a log. Its lines start with `working:`, `done:`, `failed:`, `blocked:`, `needs-decision:` or `paused:`, and lines without a prefix continue the line above.
  - `state/completions.jsonl` has one JSON object per line with `id` (the task), `outcome` (`merged`, `done`, `delivered`, `torn-down` or `unlanded`) and `torndown_at`.
  - A task is **closed** when its last prefixed status line is `done:` or `failed:`, or when it has a completion record whose `torndown_at` is not older than the status file's modification time (`stat -c %Y`).
  - A closed task whose outcome is `unlanded` becomes `ask`, because its work never landed.
  - A task is **open** otherwise. A log with no prefixed line, or with an unknown prefix such as `done-partial:`, is `ask`, and you quote its last line.
  - A task's brief is `data/<task>/brief.md`, and its report is `data/<task>/report.md` when one exists.
- **Routing:** `config/profiles/<name>/harness`, `model` and `effort`, and `config/routes/<kind>.<class>`, which holds a profile name.
- **0.7 wiring that will break:**
  - `.claude/settings.json` hooks whose command is the 0.7 binary with the arguments `supervision claude-stop`;
  - `.pi/extensions/hand-*`, `.opencode/plugins/hand-*`;
  - `references/` folders under `.claude/skills/secondhand/`, `.agents/…`, `.grok/…` and `.pi/…`.
- **Shared 0.7 infrastructure:** in `~/.secondhand` (or `$SECONDHAND_HOME`), `registry.db`, `pools/`, `runtime/`, `herdr/` and `integrations/`. In each clone, `git worktree list` shows which pool worktrees are still registered.

## 2. Proposal

Write one list for the operator. Each line is one action, marked `create`, `keep in legacy` or `ask`:

| 0.7 | New fleet | Rule |
|---|---|---|
| `data/operator.md` | the operator memory file `hand orient` prints | copy the constraints verbatim; curate the prose; `hand orient` shows only its first 1200 bytes, so say when the constraints alone are longer |
| `data/learnings.md` | the same file, or `memory/projects/<name>.md` for a lesson about one project | reusable lessons only; drop one-off incidents and lessons about 0.7-only commands, and name them in the proposal |
| each project in `data/projects.md` | `hand project add NAME <this folder>/projects/NAME` | the clone stays where it is; note `mode=` in the project's memory when it matters |
| each `## Queue` item | `hand task add --goal "…" PROJECT "title"` | keep dates and conditions in the goal |
| each open task | `hand task add`, then `hand plan set --body-file BRIEF tN` from its brief | one line per task with its last status line, and the operator picks |
| a last line starting `needs-decision:` | `hand decision ask tN "…"` after the task exists | ask the question again in full |
| profiles and routes | profiles in `routing.json` | mechanical/standard/deep become quick/default/deep; the scout/ship split is dropped; show the JSON, and the operator edits the file |
| finished tasks, `done-archive.md`, `note-archive.md`, the rest of `state/` | nothing | stays in `legacy-0.7/` after the archive step |
| 0.7 wiring and shared infrastructure | removed | the wiring first; the rest in step 6; only with approval |

`routing.json` looks like:

```json
{"profiles": {"default": {"harness": "claude", "model": "sonnet", "effort": "medium"}}}
```

opencode profiles have no model or effort.

## 3. Stop

Show the proposal and stop. Continue only with the operator's approval of the whole list or an edited one.

## 4. Execute

Run the approved `hand` commands one by one, in the order projects, tasks, plans, decisions. Tell the operator exactly what to put in `routing.json` and the memory file, or write the memory file yourself if the operator asks you to. A command that fails stops the run: report it rather than work around it.

## 5. Verify

- `hand orient`, `hand project list`, `hand task list --status inbox,active --limit 500` and `hand route list` must match the approved list.
- Report any difference.

## 6. Clean up (with approval)

Do this before the archive, while this skill is still installed.

1. **Wiring:** confirm that the 0.7 hook, extensions and plugins are gone (see "First"). Remove the old `references/` folders under `.claude/skills/secondhand/`, `.agents/…`, `.grok/…` and `.pi/…`, after showing them.
2. **Pool worktrees:** list every folder under `~/.secondhand/pools/*/` (or `$SECONDHAND_HOME/pools/`) directly, not only the worktrees that a clone registers. For each one:
   - it is **clean** only when all of these hold:
     - `git -C <worktree> status --short --ignored` prints nothing;
     - its HEAD is contained in some ref of its clone (`git -C <clone> for-each-ref --contains <HEAD>` prints something);
     - it is not locked;
   - a clean worktree of one of this fleet's clones is removed with `git -C <clone> worktree remove <worktree>`, then `git -C <clone> worktree prune`;
   - anything else (dirty, unreachable, locked, or belonging to another or deleted clone) is `ask`, and stays until the operator decides.

   Never touch worktrees under `~/.secondhand/worktrees/`: they belong to the new Hand.
3. **Shared folder:** only when `pools/` holds nothing the operator wants kept, and only with approval, delete `pools/`, `runtime/`, `herdr/`, `integrations/`, `registry.db` and `registry.db.lock` from `~/.secondhand`. Never touch `fleets/` or `worktrees/` there.

## 7. Archive (with approval)

- Ask first. Then, in this folder: `mkdir legacy-0.7 && mv data state config legacy-0.7/`.
- `projects/` stays: its clones are in use.
- Run `hand init` last. With `state/hand.db` gone, it removes this skill, so do nothing after it that needs this procedure.
