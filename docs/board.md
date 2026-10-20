# The board

The board listens on `127.0.0.1:7777`. Each fleet has its own page at `http://127.0.0.1:7777/<fleet id>/`, and `/` lists the fleets. Each fleet keeps its own token in `board.token` in its folder, so logging in to one fleet never logs you out of another. A fleet you add or move shows up without a restart. The board also keeps each fleet's watcher alive (see [Keep the watcher and the board running](autostart.md#keep-the-watcher-and-the-board-running)). To reach the board from a phone, use a tunnel such as `ssh -L` or `tailscale serve`.

A fleet's page has a slim rail of icons at the left edge (Board, Tasks, Conversation and the fleets this browser can open) and, from 900px wide, two panes. The work pane lists what waits on you (**Needs**), grouped by severity with the most severe first, then the tasks with a search field (**Tasks**). A decision shows its options as a choice list: pick one, add a note (required when the option starts with "Other"), and send. The supervisor pane on the right shows the supervisor controls, the conversation without tool calls or thinking, and the message box. Drag the line between the panes, or focus it and use the arrow keys, to resize the chat; the board remembers the width, and a double-click resets it. Below 900px the page has three tabs, **Needs**, **Tasks** and **Chat**, and the rail opens from a menu button as a sheet. Tasks, decisions and reports open in a new tab; the rail and the page's own links stay in place. The **Tasks** page searches every task. Each task and each decision also has its own page. The board is a projection of Hand's state, so restarting it loses nothing, and it updates itself as things change.

A file deliverable inside the fleet folder opens in the Board's document viewer, `/<fleet id>/doc/<path inside the fleet folder>`, in a new tab; a path outside the folder, one that is gone, or one the viewer refuses stays text. The same holds for an absolute path written in a supervisor reply, a decision, a report, a plan or a document, and for a code span that holds only a path: it links to the viewer when the viewer would show it, and otherwise stays text. The viewer only reads:

- **Markdown** up to 1 MiB renders with tables, code blocks, task lists, and relative links and images that resolve inside the fleet folder; raw HTML stays text, and a remote image stays a link.
- **Other text** up to 1 MiB shows as preformatted text.
- **An image** shows inline only when both its bytes and its name say PNG, JPEG, WebP or GIF.
- **A folder** lists its folders first, then its files, with a thumbnail for each image; dot entries are hidden, and past 500 entries the rest are counted, not listed.
- **Everything else**, a file over 1 MiB and a binary file, offers a Download, which the browser saves (`Content-Disposition: attachment`, `nosniff`, a sandbox CSP) and never runs.

It never shows dot files and dot folders, the files at the top of the fleet folder (`hand.db`, `board.token`, `AGENTS.md` and the rest of Hand's own), `luvus/`, `locks/`, `projects/`, a FIFO or device, or a path whose real path, after symlinks, leaves the fleet folder, and it says which rule refused the path. It works on a network address too, so the fleet's token also reads the fleet folder's files: keep the board on loopback and reach it through a tunnel.

`hand open` opens a page in your browser, from inside a fleet:

| Command | Opens |
|---|---|
| `hand open`, `hand open supervisor` | the fleet page |
| `hand open tN` | the task |
| `hand open dN` | the decision |
| `hand open rN`, `hand open aN` | the report or attempt, on its task's page |
| `hand open tN --delivered` | the task's newest deliverable: a PR, a document, or a file or folder, in the document viewer when it is inside the fleet folder |

`--delivered` opens a file or folder inside the fleet folder in the document viewer when the viewer serves it, through the same login link as the other pages, and `--print` prints that link.

`--delivered` never opens any other file itself, because the path is text from a worker's report or `hand task done --deliverable` and the OS opener runs programs: for a path outside the fleet folder, gone or refused, it opens the folder that holds the file, or the folder itself, and it opens a URL only when its scheme is `http` or `https`; `--print` prints that path or URL as it is. It never opens a UNC path (`\\host\share\...`), because Windows would connect to the host it names and could leak credentials: it exits with an error that prints the path, and when the newest deliverable is a UNC path it does not fall back to an older one.

`hand open` logs the browser in with the fleet's token through `xdg-open` (on macOS, `open`) and never prints the token; `hand open --print` prints the login link instead, for a phone or another browser, so keep that output private. It finds the board through `~/.secondhand/board.addr`, which the running board writes, and starts a board first when none answers.

The board is where you start, chat with and resume the supervisor. Those controls work only while the board listens on a loopback address, which a tunnel keeps true. On a network address such as `0.0.0.0` the supervisor controls are off; answering decisions and marking reports read still work.
