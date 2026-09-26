---
name: secondhand-migrate
description: Move the Hand 0.7 fleet in this folder onto the new Hand. Read the old fleet, propose what to rebuild, stop for the operator's approval, then rebuild it with `hand` commands only. Use only while the folder still holds 0.7 data (state/hand.db, data/, config/).
metadata:
  managed-by: hand
---

# Migrate a Hand 0.7 fleet

This folder holds a Hand 0.7 fleet, and `hand init` has already made it a new fleet as well. Your job is to carry what still matters into the new fleet. Nothing is converted automatically: you read the old files, the operator decides, and you rebuild through `hand` commands.

## Rules

- Change nothing before the operator approves your proposal. Reading is free; writing waits.
- Rebuild only through `hand` commands. Never write `hand.db`, and never query the 0.7 database `state/hand.db`.
- Move or delete 0.7 files only in the archive and cleanup steps, and only with approval.
- When a 0.7 fact is unclear, say so in the proposal instead of guessing.
- If the operator kept the old binary (for example as `hand-0.7`), you may use its read-only commands. Otherwise read the files.

## 1. Inventory (read-only)

Read and summarise:

- **Operator memory:** `data/operator.md` (standing constraints) and `data/learnings.md` (lessons).
- **Projects:** `data/projects.md` has lines like `- name: git-url mode=…`. The clones are in `projects/<name>/`.
- **Queue:** the items under `## Queue` in `data/backlog.md`. Ignore the rest of the file unless an item points to it.
- **Tasks:** `state/<task>.status` is a log. Its lines start with `working:`, `done:`, `failed:`, `blocked:`, `needs-decision:` or `paused:`, and lines without a prefix continue the line above. A task is open when its last prefixed line is not `done:` or `failed:`. Its brief is `data/<task>/brief.md`, and its report is `data/<task>/report.md` when one exists.
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
| `data/operator.md` | the operator memory file `hand orient` prints | copy the constraints verbatim; curate the prose |
| `data/learnings.md` | the same file, or `memory/projects/<name>.md` for a lesson about one project | reusable lessons only; drop one-off incidents and lessons about 0.7-only commands (name them in the proposal); keep within orient's memory budget |
| each project in `data/projects.md` | `hand project add NAME <this folder>/projects/NAME` | the clone stays where it is; note `mode=` in the project's memory when it matters |
| each `## Queue` item | `hand task add --goal "…" PROJECT "title"` | keep dates and conditions in the goal |
| each open task | `hand task add`, then `hand plan set --body-file BRIEF tN` from its brief | one line per task with its last status line, and the operator picks |
| a last line starting `needs-decision:` | `hand decision ask tN "…"` after the task exists | ask the question again in full |
| profiles and routes | profiles in `routing.json` | mechanical/standard/deep become quick/default/deep; the scout/ship split is dropped; show the JSON, and the operator edits the file |
| finished tasks, `done-archive.md`, `note-archive.md`, the rest of `state/` | nothing | stays in `legacy-0.7/` after the archive step |
| 0.7 wiring and shared infrastructure | removed | only in step 7, only with approval |

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

- `hand orient`, `hand project list`, `hand task list` and `hand route list` must match the approved list.
- Report any difference.

## 6. Archive (with approval)

- Ask first. Then, in this folder: `mkdir legacy-0.7 && mv data state config legacy-0.7/`.
- `projects/` stays: its clones are in use.
- Run `hand init` afterwards. With `state/hand.db` gone, it removes this skill.

## 7. Clean up (with approval)

1. **Wiring:** remove the 0.7 hook entries from `.claude/settings.json`, the `hand-*` extensions and plugins, and the old `references/` folders. Show the exact files first.
2. **Pool worktrees:**
   1. For each pool worktree registered in a clone, run `git -C <worktree> status --short`.
   2. Show a dirty worktree to the operator and keep it until they decide.
   3. Remove a clean one with `git -C <clone> worktree remove <worktree>`, then run `git -C <clone> worktree prune`.
3. **Shared folder:** only when no pool worktree is left, and only with approval, delete `pools/`, `runtime/`, `herdr/`, `integrations/`, `registry.db` and `registry.db.lock` from `~/.secondhand`. Never touch `fleets/` or `worktrees/` there: those belong to the new Hand.
