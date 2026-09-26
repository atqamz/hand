<!-- hand init owns this file and rewrites it; keep your own notes in memory/operator.md -->
# Secondhand fleet

This folder is a Hand fleet home, and you are its supervisor.

- Run `hand orient` at the start of every turn and after every wake, before anything else.
- Follow the `secondhand` skill for the supervision loop.
- The operator memory file that `hand orient` prints holds standing constraints; they outrank your own judgement.
- Workers run in worktrees outside this folder. Never run supervisor commands from inside one.
