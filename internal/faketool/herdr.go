package faketool

import (
	"fmt"
	"strings"
	"testing"
)

// One tab of a fake herdr workspace, with the pane herdr creates alongside it.
type HerdrTab struct {
	ID    string
	Label string
	Pane  string
}

// A workspace of the fake herdr, live from installation when listed in
// Herdr.Workspaces and from the matching call when listed in Herdr.Creates.
type HerdrWorkspace struct {
	ID    string
	Label string
	Tabs  []HerdrTab
}

// A fake herdr whose workspaces, tabs and panes are created and closed by the
// commands that create and close them, so closing one is visible to every later
// list, get and close. FIDELITY.md records this against the real tool.
type Herdr struct {
	Workspaces []HerdrWorkspace
	// Handed out by "workspace create", one per call in order.
	Creates []HerdrWorkspace
	// Handed out by "tab create", one per call, attached to the workspace asked for.
	TabCreates  []HerdrTab
	PaneAgent   string
	PaneStatus  string
	PaneReadOut string
	Log         string
}

// Frame "pane read" answers with when a test names none: a settled Claude Code
// prompt with no first-run dialog on it, which is what confirmLaunch waits for.
const herdrDefaultPaneRead = "Welcome to Claude Code\\n> \\n  ? for shortcuts\\n"

type herdrTabRef struct {
	tab   HerdrTab
	index int
}

// Writes the fake into bin, which Bin has put on PATH.
func (h Herdr) Install(t *testing.T, bin string) {
	t.Helper()
	state := stateDir(t, bin, "herdr")

	agent, status, paneRead := h.PaneAgent, h.PaneStatus, h.PaneReadOut
	if agent == "" {
		agent = "claude"
	}
	if status == "" {
		status = "working"
	}
	if paneRead == "" {
		paneRead = herdrDefaultPaneRead
	}

	wsFile := func(i int) string { return quote(fmt.Sprintf("%s/w%d", state, i)) }
	wsLabel := func(i int) string { return quote(fmt.Sprintf("%s/w%d.label", state, i)) }
	tabFile := func(j int) string { return quote(fmt.Sprintf("%s/t%d", state, j)) }
	tabLabel := func(j int) string { return quote(fmt.Sprintf("%s/t%d.label", state, j)) }

	workspaces := append(append([]HerdrWorkspace{}, h.Workspaces...), h.Creates...)
	var tabs []herdrTabRef
	for _, ws := range workspaces {
		for _, tab := range ws.Tabs {
			tabs = append(tabs, herdrTabRef{tab: tab, index: len(tabs)})
		}
	}
	tabIndex := map[string]int{}
	for _, ref := range tabs {
		tabIndex[ref.tab.ID] = ref.index
	}
	for _, tab := range h.TabCreates {
		tabs = append(tabs, herdrTabRef{tab: tab, index: len(tabs)})
		tabIndex[tab.ID] = len(tabs) - 1
	}

	var prelude strings.Builder
	prelude.WriteString(`err() { printf '{"error":{"code":"%s","message":"%s"},"id":"cli:fake"}\n' "$1" "$2" >&2; exit 1; }
argval() { flag="$1"; shift; while [ $# -gt 0 ]; do if [ "$1" = "$flag" ]; then echo "$2"; return; fi; shift; done; }
bump() { n=0; [ -s "$1" ] && read n < "$1"; n=$((n+1)); echo "$n" > "$1"; }
count_tabs() { n=0
`)
	for _, ref := range tabs {
		fmt.Fprintf(&prelude, "  if [ -s %s ]; then read x < %s; [ \"$x\" = \"$1\" ] && n=$((n+1)); fi\n",
			tabFile(ref.index), tabFile(ref.index))
	}
	prelude.WriteString("}\nclose_ws() { case \"$1\" in\n")
	for i, ws := range workspaces {
		fmt.Fprintf(&prelude, "  %s) : > %s ;;\n", quote(ws.ID), wsFile(i))
	}
	prelude.WriteString("esac\n")
	for _, ref := range tabs {
		fmt.Fprintf(&prelude, "  if [ -s %s ]; then read x < %s; [ \"$x\" = \"$1\" ] && : > %s; fi\n",
			tabFile(ref.index), tabFile(ref.index), tabFile(ref.index))
	}
	prelude.WriteString("}\nlist_tabs() { sep=\"\"\n")
	for _, ref := range tabs {
		fmt.Fprintf(&prelude, `  if [ -s %[1]s ]; then read tws < %[1]s
    if [ "$tws" = "$1" ]; then tl=""; [ -s %[2]s ] && read tl < %[2]s
      printf '%%s{"tab_id":%[3]s,"workspace_id":"%%s","label":"%%s","number":%[4]d,"pane_count":1,"agent_status":"unknown","focused":false}' "$sep" "$tws" "$tl"
      sep=","
    fi
  fi
`, tabFile(ref.index), tabLabel(ref.index), jsonQuote(ref.tab.ID), ref.index+1)
	}
	prelude.WriteString("}\n")

	var body strings.Builder

	body.WriteString(`  "workspace list")
    printf '{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":['
    sep=""
`)
	for i, ws := range workspaces {
		fmt.Fprintf(&body, `    if [ -s %[1]s ]; then
      wl=""; [ -s %[2]s ] && read wl < %[2]s
      count_tabs %[3]s
      printf '%%s{"workspace_id":%[4]s,"label":"%%s","tab_count":%%s,"pane_count":%%s,"active_tab_id":"","agent_status":"unknown","focused":false,"number":%[5]d}' "$sep" "$wl" "$n" "$n"
      sep=","
    fi
`, wsFile(i), wsLabel(i), quote(ws.ID), jsonQuote(ws.ID), i+1)
	}
	body.WriteString("    printf ']}}\\n'\n    ;;\n")

	body.WriteString(`  "workspace create")
    label=$(argval --label "$@")
    cwd=$(argval --cwd "$@")
    bump ` + quote(state+"/creates") + `
    case "$n" in
`)
	for c, ws := range h.Creates {
		i := len(h.Workspaces) + c
		root := ws.Tabs[0]
		j := tabIndex[root.ID]
		fmt.Fprintf(&body, `    %d)
      echo live > %[2]s
      printf '%%s\n' "$label" > %[3]s
      printf '%%s\n' %[4]s > %[5]s
      printf '1\n' > %[6]s
      printf '{"id":"cli:workspace:create","result":{"type":"workspace_created","workspace":{"workspace_id":%[7]s,"label":"%%s","tab_count":1,"pane_count":1,"active_tab_id":%[8]s,"agent_status":"unknown","focused":false,"number":%[9]d},"tab":{"tab_id":%[8]s,"workspace_id":%[7]s,"label":"1","number":1,"pane_count":1,"agent_status":"unknown","focused":false},"root_pane":{"pane_id":%[10]s,"tab_id":%[8]s,"workspace_id":%[7]s,"agent":"","agent_status":"unknown","cwd":"%%s"}}}\n' "$label" "$cwd"
      ;;
`, c+1, wsFile(i), wsLabel(i), quote(ws.ID), tabFile(j), tabLabel(j),
			jsonQuote(ws.ID), jsonQuote(root.ID), i+1, jsonQuote(root.Pane))
	}
	body.WriteString("    *) err workspace_create_exhausted \"the fake declares no further workspace create\" ;;\n    esac\n    ;;\n")

	body.WriteString(`  "tab create")
    ws=$(argval --workspace "$@")
    label=$(argval --label "$@")
    cwd=$(argval --cwd "$@")
`)
	body.WriteString(herdrRequireLiveWorkspace(workspaces, wsFile, `"$ws"`))
	body.WriteString("    bump " + quote(state+"/tabcreates") + "\n    case \"$n\" in\n")
	for c, tab := range h.TabCreates {
		j := tabIndex[tab.ID]
		fmt.Fprintf(&body, `    %d)
      printf '%%s\n' "$ws" > %[2]s
      printf '%%s\n' "$label" > %[3]s
      printf '{"id":"cli:tab:create","result":{"type":"tab_created","tab":{"tab_id":%[4]s,"workspace_id":"%%s","label":"%%s","number":%[5]d,"pane_count":1,"agent_status":"unknown","focused":false},"root_pane":{"pane_id":%[6]s,"tab_id":%[4]s,"workspace_id":"%%s","agent":"","agent_status":"unknown","cwd":"%%s"}}}\n' "$ws" "$label" "$ws" "$cwd"
      ;;
`, c+1, tabFile(j), tabLabel(j), jsonQuote(tab.ID), j+1, jsonQuote(tab.Pane))
	}
	body.WriteString("    *) err tab_create_exhausted \"the fake declares no further tab create\" ;;\n    esac\n    ;;\n")

	body.WriteString("  \"tab list\")\n    ws=$(argval --workspace \"$@\")\n")
	body.WriteString(herdrRequireLiveWorkspace(workspaces, wsFile, `"$ws"`))
	body.WriteString(`    printf '{"id":"cli:tab:list","result":{"type":"tab_list","tabs":['
    list_tabs "$ws"
    printf ']}}\n'
    ;;
`)

	body.WriteString("  \"tab rename\")\n    case \"$3\" in\n")
	for _, ref := range tabs {
		fmt.Fprintf(&body, "    %s) [ -s %s ] || err tab_not_found \"tab $3 not found\"; printf '%%s\\n' \"$4\" > %s ;;\n",
			quote(ref.tab.ID), tabFile(ref.index), tabLabel(ref.index))
	}
	body.WriteString(`    *) err tab_not_found "tab $3 not found" ;;
    esac
    printf '{"id":"cli:tab:rename","result":{"type":"tab_info","tab":{"tab_id":"%s","workspace_id":"","label":"%s","number":1,"pane_count":1,"agent_status":"unknown","focused":false}}}\n' "$3" "$4"
    ;;
`)

	// Closing a workspace's last tab closes the workspace with it, which is what
	// real herdr does: nothing refuses the call, and the workspace is simply gone
	// from every later list, get and close.
	body.WriteString("  \"tab close\")\n    case \"$3\" in\n")
	for _, ref := range tabs {
		fmt.Fprintf(&body, `    %[1]s) [ -s %[2]s ] || err tab_not_found "tab $3 not found"
      read tws < %[2]s; : > %[2]s
      count_tabs "$tws"; [ "$n" = 0 ] && close_ws "$tws"
      ;;
`, quote(ref.tab.ID), tabFile(ref.index))
	}
	body.WriteString(`    *) err tab_not_found "tab $3 not found" ;;
    esac
    printf '{"id":"cli:tab:close","result":{"type":"ok"}}\n'
    ;;
  "workspace close")
`)
	body.WriteString(herdrRequireLiveWorkspace(workspaces, wsFile, `"$3"`))
	body.WriteString(`    close_ws "$3"
    printf '{"id":"cli:workspace:close","result":{"type":"ok"}}\n'
    ;;
`)

	panes := herdrRequireLivePane(tabs, tabFile)
	fmt.Fprintf(&body, `  "pane get")
%[1]s    printf '{"id":"cli:pane:get","result":{"type":"pane_info","pane":{"pane_id":"%%s","tab_id":"%%s","workspace_id":"%%s","agent":%[2]s,"agent_status":%[3]s}}}\n' "$3" "$ptab" "$pws"
    ;;
  "pane read")
%[1]s    printf %[4]s
    ;;
  "pane run")
%[1]s    ;;
  "pane send-text")
%[1]s    ;;
  "pane send-keys")
%[1]s    ;;`, panes, jsonQuote(agent), jsonQuote(status), quote(paneRead))

	install(t, bin, "herdr", h.Log, prelude.String(), "$1 $2", body.String())

	for i, ws := range h.Workspaces {
		writeFile(t, fmt.Sprintf("%s/w%d", state, i), "live\n")
		writeFile(t, fmt.Sprintf("%s/w%d.label", state, i), ws.Label+"\n")
		for _, tab := range ws.Tabs {
			j := tabIndex[tab.ID]
			writeFile(t, fmt.Sprintf("%s/t%d", state, j), ws.ID+"\n")
			writeFile(t, fmt.Sprintf("%s/t%d.label", state, j), tab.Label+"\n")
		}
	}
}

// A workspace closed earlier in the test is not merely absent from the listing:
// every command naming it answers workspace_not_found, which is what a rerun of
// teardown actually meets.
func herdrRequireLiveWorkspace(workspaces []HerdrWorkspace, wsFile func(int) string, arg string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "    case %s in\n", arg)
	for i, ws := range workspaces {
		fmt.Fprintf(&b, "    %s) [ -s %s ] || err workspace_not_found \"workspace %s not found\" ;;\n",
			quote(ws.ID), wsFile(i), arg)
	}
	fmt.Fprintf(&b, "    *) err workspace_not_found \"workspace %s not found\" ;;\n    esac\n", arg)
	return b.String()
}

// A pane lives and dies with its tab, so it answers pane_not_found from the
// moment that tab is closed. $ptab and $pws are left set for the caller.
func herdrRequireLivePane(tabs []herdrTabRef, tabFile func(int) string) string {
	var b strings.Builder
	b.WriteString("    case \"$3\" in\n")
	for _, ref := range tabs {
		if ref.tab.Pane == "" {
			continue
		}
		fmt.Fprintf(&b, "    %[1]s) [ -s %[2]s ] || err pane_not_found \"pane $3 not found\"; read pws < %[2]s; ptab=%[3]s ;;\n",
			quote(ref.tab.Pane), tabFile(ref.index), quote(ref.tab.ID))
	}
	b.WriteString("    *) err pane_not_found \"pane $3 not found\" ;;\n    esac\n")
	return b.String()
}

func jsonQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
