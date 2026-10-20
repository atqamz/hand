# The board

The board listens on `127.0.0.1:7777`. Each fleet has its own page at `http://127.0.0.1:7777/<fleet id>/`, and `/` lists the fleets. Each fleet keeps its own token in `board.token` in its folder, so logging in to one fleet never logs you out of another. A fleet you add or move shows up without a restart. The board also keeps each fleet's watcher alive (see [Keep the watcher and the board running](autostart.md#keep-the-watcher-and-the-board-running)). To reach the board from a phone, use a tunnel such as `ssh -L` or `tailscale serve`.

A fleet's page has a slim rail of icons at the left edge (Board, Tasks, Conversation and the fleets this browser can open) and, from 900px wide, two panes. The work pane lists what waits on you (**Needs**), grouped by severity with the most severe first, then the tasks with a search field (**Tasks**). A decision shows its options as a choice list: pick one, add a note (required when the option starts with "Other"), and send. The supervisor pane on the right shows the supervisor controls, the conversation without tool calls or thinking, and the message box. Drag the line between the panes, or focus it and use the arrow keys, to resize the chat; the board remembers the width, and a double-click resets it. Below 900px the page has three tabs, **Needs**, **Tasks** and **Chat**, and the rail opens from a menu button as a sheet. Tasks, decisions and reports open in a new tab; the rail and the page's own links stay in place. The **Tasks** page searches every task. Each task and each decision also has its own page. The board is a projection of Hand's state, so restarting it loses nothing, and it updates itself as things change.

`hand open` opens a page in your browser, from inside a fleet:

| Command | Opens |
|---|---|
| `hand open`, `hand open supervisor` | the fleet page |
| `hand open tN` | the task |
| `hand open dN` | the decision |
| `hand open rN`, `hand open aN` | the report or attempt, on its task's page |
| `hand open tN --delivered` | the task's newest deliverable: a PR, a document or a file |

`--delivered` never opens a file itself, because the path is text from a worker's report or `hand task done --deliverable` and the OS opener runs programs: it opens the folder that holds a file, or the folder itself, and opens a URL only when its scheme is `http` or `https`; `--print` prints the path or URL as it is. It never opens a UNC path (`\\host\share\...`), because Windows would connect to the host it names and could leak credentials: it exits with an error that prints the path, and when the newest deliverable is a UNC path it does not fall back to an older one. It logs the browser in with the fleet's token through `xdg-open` (on macOS, `open`) and never prints the token; `hand open --print` prints the login link instead, for a phone or another browser, so keep that output private. It finds the board through `~/.secondhand/board.addr`, which the running board writes, and starts a board first when none answers.

The board is where you start, chat with and resume the supervisor. Those controls work only while the board listens on a loopback address, which a tunnel keeps true. On a network address such as `0.0.0.0` the supervisor controls are off; answering decisions and marking reports read still work.
