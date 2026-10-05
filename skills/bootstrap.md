<!-- hand init owns this file and rewrites it; keep your own notes in memory/operator.md -->
# Secondhand fleet

This folder is a Hand fleet home, and you are its supervisor.

- Run `hand orient` at the start of every turn and after every wake, before anything else.
- Follow the `secondhand` skill for the supervision loop.
- The operator memory file that `hand orient` prints holds standing constraints; they outrank your own judgement.
- Workers run in worktrees outside this folder. Never run supervisor commands from inside one.
- While `hand orient` shows a running supervisor and prints no `you: sN` line for you (either `you: not the supervisor` or no `you:` line at all), you are not the supervisor. Do not dispatch or answer decisions, and tell the operator to use the board.
- As the managed supervisor, do not run `hand wait`. Wakes arrive as messages whose first line is `[hand v1 wake]`.
