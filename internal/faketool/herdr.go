package faketool

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// HerdrTab describes a tab and the pane herdr creates with it.
type HerdrTab struct {
	ID    string
	Label string
	Pane  string
}

// HerdrWorkspace describes one workspace and its tabs.
type HerdrWorkspace struct {
	ID    string
	Label string
	Tabs  []HerdrTab
}

// HerdrResponse overrides one command while keeping the shared executable and
// invocation logging. It is useful for deliberately malformed or failed calls.
type HerdrResponse struct {
	Command              string
	Args                 []string
	Stdout               string
	Stderr               string
	Exit                 int
	MutateBeforeResponse bool
}

// HerdrFrame supplies successive pane get/read observations.
type HerdrFrame struct {
	Text   string
	Agent  string
	Status string
}

// Herdr models workspace, tab and pane state changes made by hand.
type Herdr struct {
	Workspaces         []HerdrWorkspace
	Creates            []HerdrWorkspace
	TabCreates         []HerdrTab
	PaneAgent          string
	ProcessAgent       string
	ProcessAgents      []string
	PaneStatus         string
	PaneStatusSequence []string
	PaneReadOut        string
	// PaneReadUnwrappedOut answers `pane read --source recent-unwrapped` specifically, for a test that
	// needs the post-send confirmation read to see different text than PaneReadOut/Frames answer the
	// default `recent` source with. Empty falls back to the same PaneReadOut/Frames logic as `recent`.
	PaneReadUnwrappedOut string
	// PaneReadUnwrappedSequence answers successive `pane read --source recent-unwrapped` calls in order,
	// holding the last entry once exhausted - like PaneStatusSequence, but keyed to its own counter since
	// one send does one PaneGet but several PaneReadUnwrapped calls. Wins over PaneReadUnwrappedOut.
	PaneReadUnwrappedSequence []string
	PaneStatusFile            string
	Frames                    []HerdrFrame
	Responses                 []HerdrResponse
	Hang                      []string
	Unreachable               bool
	KeyLog                    string
	TextLog                   string
	Log                       string
	LogCommands               []string
	PaneAgentEnv              bool
	PaneReadFileEnv           bool
	KeyLogEnv                 bool
	TextLogEnv                bool
	ReadLogEnv                bool
	AllowUnknownPane          bool
	MutateBeforeHang          bool
}

const herdrDefaultPaneRead = "Welcome to Claude Code\n> \n  ? for shortcuts\n"

type herdrSpec struct {
	Workspaces                []HerdrWorkspace
	Creates                   []HerdrWorkspace
	TabCreates                []HerdrTab
	PaneAgent                 string
	ProcessAgent              string
	ProcessAgents             []string
	PaneStatus                string
	PaneStatusSequence        []string
	PaneReadOut               string
	PaneReadUnwrappedOut      string
	PaneReadUnwrappedSequence []string
	PaneStatusFile            string
	Frames                    []HerdrFrame
	Responses                 []HerdrResponse
	Hang                      []string
	Unreachable               bool
	KeyLog                    string
	TextLog                   string
	StateDir                  string
	Log                       string
	LogCommands               []string
	PaneAgentEnv              bool
	PaneReadFileEnv           bool
	KeyLogEnv                 bool
	TextLogEnv                bool
	ReadLogEnv                bool
	AllowUnknownPane          bool
	MutateBeforeHang          bool
	activeSession             string
}

type herdrTabRef struct {
	Tab   HerdrTab
	Index int
}

func (h Herdr) Install(t *testing.T, bin string) {
	t.Helper()
	t.Setenv("HAND_TEST_HERDR_PATH", filepath.Join(bin, executableName("herdr")))
	state := stateDir(t, bin, "herdr")
	workspaces := append(append([]HerdrWorkspace{}, h.Workspaces...), h.Creates...)
	tabs := herdrTabs(workspaces, h.TabCreates)
	for i, ws := range h.Workspaces {
		ensureFile(t, herdrWorkspacePath(state, i), "live\n")
		ensureFile(t, herdrLabelPath(state, "w", i), ws.Label+"\n")
		for _, tab := range ws.Tabs {
			ref := herdrTabIndex(tabs, tab.ID)
			ensureFile(t, herdrTabPath(state, ref.Index), ws.ID+"\n")
			ensureFile(t, herdrLabelPath(state, "t", ref.Index), tab.Label+"\n")
		}
	}
	installConfig(t, bin, "herdr", "herdr", herdrSpec{
		Workspaces: h.Workspaces, Creates: h.Creates, TabCreates: h.TabCreates,
		PaneAgent: h.PaneAgent, ProcessAgent: h.ProcessAgent, ProcessAgents: h.ProcessAgents, PaneStatus: h.PaneStatus, PaneReadOut: h.PaneReadOut,
		PaneReadUnwrappedOut:      h.PaneReadUnwrappedOut,
		PaneReadUnwrappedSequence: h.PaneReadUnwrappedSequence,
		PaneStatusSequence:        h.PaneStatusSequence,
		PaneStatusFile:            h.PaneStatusFile, Frames: h.Frames, Responses: h.Responses,
		Hang: h.Hang, Unreachable: h.Unreachable, KeyLog: h.KeyLog, TextLog: h.TextLog,
		StateDir: state, Log: h.Log, LogCommands: h.LogCommands,
		PaneAgentEnv: h.PaneAgentEnv, PaneReadFileEnv: h.PaneReadFileEnv,
		KeyLogEnv: h.KeyLogEnv, TextLogEnv: h.TextLogEnv,
		ReadLogEnv: h.ReadLogEnv, AllowUnknownPane: h.AllowUnknownPane, MutateBeforeHang: h.MutateBeforeHang,
	})
}

func runHerdrFromPayload(payload json.RawMessage, args []string) int {
	var spec herdrSpec
	if err := json.Unmarshal(payload, &spec); err != nil {
		return fail("decode herdr config: %v", err)
	}
	stateDir, err := herdrStateDirForSession(spec.StateDir, args)
	if err != nil {
		return fail("prepare Herdr session state: %v", err)
	}
	spec.StateDir = stateDir
	spec.activeSession = herdrSession(args)
	logicalArgs := herdrLogicalArgs(args)
	command, commandArgs := herdrCommand(logicalArgs)
	if command == "" {
		return fail("unexpected herdr invocation: %s", strings.Join(args, " "))
	}
	for _, blocked := range spec.Hang {
		if blocked == command {
			if spec.MutateBeforeHang {
				if exit := runHerdrState(spec, command, logicalArgs); exit != 0 {
					return exit
				}
			}
			if err := logHerdrInvocation(spec, args); err != nil {
				return fail("log herdr invocation: %v", err)
			}
			for {
				time.Sleep(time.Hour)
			}
		}
	}
	if spec.Unreachable {
		if err := logHerdrInvocation(spec, args); err != nil {
			return fail("log herdr invocation: %v", err)
		}
		return 1
	}
	for _, response := range spec.Responses {
		if response.Command == command && (response.Args == nil || sameArgs(response.Args, commandArgs)) {
			if response.MutateBeforeResponse {
				if exit := runHerdrState(spec, command, logicalArgs); exit != 0 {
					return exit
				}
			}
			_, _ = io.WriteString(os.Stdout, response.Stdout)
			_, _ = io.WriteString(os.Stderr, response.Stderr)
			if err := logHerdrInvocation(spec, args); err != nil {
				return fail("log herdr invocation: %v", err)
			}
			return response.Exit
		}
	}
	exit := runHerdrState(spec, command, logicalArgs)
	if err := logHerdrInvocation(spec, args); err != nil {
		return fail("log herdr invocation: %v", err)
	}
	return exit
}

func herdrStateDirForSession(base string, args []string) (string, error) {
	session := herdrSession(args)
	if session == "" || session == "default" {
		return base, nil
	}
	safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(session)
	dir := filepath.Join(base, "sessions", safe)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ready := filepath.Join(dir, ".ready")
	if _, err := os.Stat(ready); err == nil {
		return dir, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "sessions" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(base, entry.Name()))
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0o600); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(ready, nil, 0o600); err != nil && !os.IsExist(err) {
		return "", err
	}
	return dir, nil
}

func herdrSession(args []string) string {
	if len(args) >= 2 && args[0] == "--session" {
		return args[1]
	}
	return ""
}

func herdrLogicalArgs(args []string) []string {
	if len(args) >= 2 && args[0] == "--session" {
		return args[2:]
	}
	return args
}

func herdrCommand(args []string) (string, []string) {
	if len(args) == 0 {
		return "", nil
	}
	if args[0] == "server" {
		return "server", args[1:]
	}
	if len(args) < 2 {
		return "", nil
	}
	return args[0] + " " + args[1], args[2:]
}

func logHerdrInvocation(spec herdrSpec, args []string) error {
	if spec.Log == "" || (len(spec.LogCommands) > 0 && !containsString(spec.LogCommands, commandName(args))) {
		return nil
	}
	return appendInvocation(spec.Log, "herdr", args)
}

func commandName(args []string) string {
	command, _ := herdrCommand(herdrLogicalArgs(args))
	return command
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func runHerdrState(spec herdrSpec, command string, args []string) int {
	workspaces := append(append([]HerdrWorkspace{}, spec.Workspaces...), spec.Creates...)
	tabs := herdrTabs(workspaces, spec.TabCreates)
	switch command {
	case "server":
		return 0
	case "session list":
		return herdrSessionList(spec, args)
	case "session attach":
		return 0
	case "status server":
		return herdrServerStatus(spec, args)
	case "workspace list":
		return herdrWorkspaceList(spec, workspaces)
	case "workspace create":
		return herdrWorkspaceCreate(spec, workspaces, tabs, args)
	case "workspace close":
		return herdrWorkspaceClose(spec, workspaces, tabs, args)
	case "tab list":
		return herdrTabList(spec, workspaces, tabs, args)
	case "tab create":
		return herdrTabCreate(spec, workspaces, tabs, args)
	case "tab rename":
		return herdrTabRename(spec, tabs, args)
	case "tab close":
		return herdrTabClose(spec, workspaces, tabs, args)
	case "pane get":
		return herdrPaneGet(spec, tabs, args)
	case "pane process-info":
		return herdrPaneProcessInfo(spec, tabs, args)
	case "pane read":
		return herdrPaneRead(spec, tabs, args)
	case "pane run", "pane send-text", "pane send-keys":
		return herdrPaneVoid(spec, tabs, command, args)
	default:
		return fail("unexpected herdr invocation: %s", strings.Join(args, " "))
	}
}

func herdrSessionList(spec herdrSpec, args []string) int {
	session := spec.activeSession
	if session == "" {
		_, _ = io.WriteString(os.Stdout, "{\"sessions\":[]}\n")
		return 0
	}
	_, _ = fmt.Fprintf(os.Stdout, "{\"sessions\":[{\"name\":%s,\"running\":true}]}\n", jsonQuote(session))
	return 0
}

func herdrServerStatus(spec herdrSpec, args []string) int {
	session := spec.activeSession
	_, _ = fmt.Fprintf(os.Stdout, "{\"status\":\"running\",\"running\":true,\"compatible\":true,\"session\":%s}\n", jsonQuote(session))
	return 0
}

func herdrWorkspaceList(spec herdrSpec, workspaces []HerdrWorkspace) int {
	var out strings.Builder
	out.WriteString("{\"id\":\"cli:workspace:list\",\"result\":{\"type\":\"workspace_list\",\"workspaces\":[")
	sep := ""
	for i, ws := range workspaces {
		if !herdrLive(spec.StateDir, herdrWorkspacePath(spec.StateDir, i)) {
			continue
		}
		count := herdrWorkspaceTabCount(spec, ws.ID, workspaces, herdrTabs(workspaces, spec.TabCreates))
		fmt.Fprintf(&out, "%s{\"workspace_id\":%s,\"label\":%s,\"tab_count\":%d,\"pane_count\":%d,\"active_tab_id\":\"\",\"agent_status\":\"unknown\",\"focused\":false,\"number\":%d}",
			sep, jsonQuote(ws.ID), jsonQuote(herdrLabel(spec.StateDir, "w", i)), count, count, i+1)
		sep = ","
	}
	out.WriteString("]}}\n")
	_, _ = io.WriteString(os.Stdout, out.String())
	return 0
}

func herdrWorkspaceCreate(spec herdrSpec, workspaces []HerdrWorkspace, tabs []herdrTabRef, args []string) int {
	index := herdrCounter(spec.StateDir, "creates")
	if index >= len(spec.Creates) {
		return fail("the fake declares no further workspace create")
	}
	ws := spec.Creates[index]
	if len(ws.Tabs) == 0 {
		return fail("the fake workspace has no root tab")
	}
	root := ws.Tabs[0]
	tab := herdrTabIndex(tabs, root.ID)
	label := flagValue(args, "--label")
	cwd := flagValue(args, "--cwd")
	if err := atomicWrite(herdrCounterPath(spec.StateDir, "creates"), strconv.Itoa(index+1)+"\n"); err != nil {
		return fail("write workspace create counter: %v", err)
	}
	if err := atomicWrite(herdrWorkspacePath(spec.StateDir, len(spec.Workspaces)+index), "live\n"); err != nil {
		return fail("write workspace state: %v", err)
	}
	if err := atomicWrite(herdrLabelPath(spec.StateDir, "w", len(spec.Workspaces)+index), label+"\n"); err != nil {
		return fail("write workspace label: %v", err)
	}
	if err := atomicWrite(herdrTabPath(spec.StateDir, tab.Index), ws.ID+"\n"); err != nil {
		return fail("write root tab state: %v", err)
	}
	if err := atomicWrite(herdrLabelPath(spec.StateDir, "t", tab.Index), "1\n"); err != nil {
		return fail("write root tab label: %v", err)
	}
	out := fmt.Sprintf("{\"id\":\"cli:workspace:create\",\"result\":{\"type\":\"workspace_created\",\"workspace\":{\"workspace_id\":%s,\"label\":%s,\"tab_count\":1,\"pane_count\":1,\"active_tab_id\":%s,\"agent_status\":\"unknown\",\"focused\":false,\"number\":%d},\"tab\":{\"tab_id\":%s,\"workspace_id\":%s,\"label\":\"1\",\"number\":1,\"pane_count\":1,\"agent_status\":\"unknown\",\"focused\":false},\"root_pane\":{\"pane_id\":%s,\"tab_id\":%s,\"workspace_id\":%s,\"agent\":\"\",\"agent_status\":\"unknown\",\"cwd\":%s}}}\n",
		jsonQuote(ws.ID), jsonQuote(label), jsonQuote(root.ID), len(spec.Workspaces)+index+1,
		jsonQuote(root.ID), jsonQuote(ws.ID), jsonQuote(root.Pane), jsonQuote(root.ID), jsonQuote(ws.ID), jsonQuote(cwd))
	_, _ = io.WriteString(os.Stdout, out)
	return 0
}

func herdrWorkspaceClose(spec herdrSpec, workspaces []HerdrWorkspace, tabs []herdrTabRef, args []string) int {
	if len(args) < 3 {
		return fail("workspace_not_found")
	}
	index := herdrWorkspaceIndex(workspaces, args[2])
	if index < 0 || !herdrLive(spec.StateDir, herdrWorkspacePath(spec.StateDir, index)) {
		return herdrError("workspace_not_found", "workspace "+args[2]+" not found", "cli:workspace:close")
	}
	if err := herdrCloseWorkspace(spec.StateDir, workspaces[index].ID, index, tabs); err != nil {
		return fail("close workspace: %v", err)
	}
	_, _ = io.WriteString(os.Stdout, "{\"id\":\"cli:workspace:close\",\"result\":{\"type\":\"ok\"}}\n")
	return 0
}

func herdrTabList(spec herdrSpec, workspaces []HerdrWorkspace, tabs []herdrTabRef, args []string) int {
	ws := flagValue(args, "--workspace")
	if herdrWorkspaceIndexLive(spec, workspaces, ws) < 0 {
		return herdrError("workspace_not_found", "workspace "+ws+" not found", "cli:tab:list")
	}
	var out strings.Builder
	out.WriteString("{\"id\":\"cli:tab:list\",\"result\":{\"type\":\"tab_list\",\"tabs\":[")
	sep := ""
	for _, ref := range tabs {
		owner, ok := herdrTabWorkspace(spec, ref)
		if !ok || owner != ws {
			continue
		}
		fmt.Fprintf(&out, "%s{\"tab_id\":%s,\"workspace_id\":%s,\"label\":%s,\"number\":%d,\"pane_count\":1,\"agent_status\":\"unknown\",\"focused\":false}",
			sep, jsonQuote(ref.Tab.ID), jsonQuote(ws), jsonQuote(herdrLabel(spec.StateDir, "t", ref.Index)), ref.Index+1)
		sep = ","
	}
	out.WriteString("]}}\n")
	_, _ = io.WriteString(os.Stdout, out.String())
	return 0
}

func herdrTabCreate(spec herdrSpec, workspaces []HerdrWorkspace, tabs []herdrTabRef, args []string) int {
	ws := flagValue(args, "--workspace")
	if herdrWorkspaceIndexLive(spec, workspaces, ws) < 0 {
		return herdrError("workspace_not_found", "workspace "+ws+" not found", "cli:tab:create")
	}
	index := herdrCounter(spec.StateDir, "tabcreates")
	if index >= len(spec.TabCreates) {
		return fail("the fake declares no further tab create")
	}
	tab := spec.TabCreates[index]
	ref := herdrTabIndex(tabs, tab.ID)
	if err := atomicWrite(herdrCounterPath(spec.StateDir, "tabcreates"), strconv.Itoa(index+1)+"\n"); err != nil {
		return fail("write tab create counter: %v", err)
	}
	if err := atomicWrite(herdrTabPath(spec.StateDir, ref.Index), ws+"\n"); err != nil {
		return fail("write tab state: %v", err)
	}
	label := flagValue(args, "--label")
	if err := atomicWrite(herdrLabelPath(spec.StateDir, "t", ref.Index), label+"\n"); err != nil {
		return fail("write tab label: %v", err)
	}
	cwd := flagValue(args, "--cwd")
	out := fmt.Sprintf("{\"id\":\"cli:tab:create\",\"result\":{\"type\":\"tab_created\",\"tab\":{\"tab_id\":%s,\"workspace_id\":%s,\"label\":%s,\"number\":%d,\"pane_count\":1,\"agent_status\":\"unknown\",\"focused\":false},\"root_pane\":{\"pane_id\":%s,\"tab_id\":%s,\"workspace_id\":%s,\"agent\":\"\",\"agent_status\":\"unknown\",\"cwd\":%s}}}\n",
		jsonQuote(tab.ID), jsonQuote(ws), jsonQuote(label), ref.Index+1, jsonQuote(tab.Pane), jsonQuote(tab.ID), jsonQuote(ws), jsonQuote(cwd))
	_, _ = io.WriteString(os.Stdout, out)
	return 0
}

func herdrTabRename(spec herdrSpec, tabs []herdrTabRef, args []string) int {
	if len(args) < 4 {
		return herdrError("tab_not_found", "tab not found", "cli:tab:rename")
	}
	ref := herdrTabIndex(tabs, args[2])
	if ref.Tab.ID == "" || !herdrLive(spec.StateDir, herdrTabPath(spec.StateDir, ref.Index)) {
		return herdrError("tab_not_found", "tab "+args[2]+" not found", "cli:tab:rename")
	}
	if err := atomicWrite(herdrLabelPath(spec.StateDir, "t", ref.Index), args[3]+"\n"); err != nil {
		return fail("write tab label: %v", err)
	}
	_, _ = fmt.Fprintf(os.Stdout, "{\"id\":\"cli:tab:rename\",\"result\":{\"type\":\"tab_info\",\"tab\":{\"tab_id\":%s,\"workspace_id\":\"\",\"label\":%s,\"number\":1,\"pane_count\":1,\"agent_status\":\"unknown\",\"focused\":false}}}\n",
		jsonQuote(args[2]), jsonQuote(args[3]))
	return 0
}

func herdrTabClose(spec herdrSpec, workspaces []HerdrWorkspace, tabs []herdrTabRef, args []string) int {
	if len(args) < 3 {
		return herdrError("tab_not_found", "tab not found", "cli:tab:close")
	}
	ref := herdrTabIndex(tabs, args[2])
	if ref.Tab.ID == "" || !herdrLive(spec.StateDir, herdrTabPath(spec.StateDir, ref.Index)) {
		return herdrError("tab_not_found", "tab "+args[2]+" not found", "cli:tab:close")
	}
	owner, _ := herdrTabWorkspace(spec, ref)
	if err := atomicWrite(herdrTabPath(spec.StateDir, ref.Index), ""); err != nil {
		return fail("close tab: %v", err)
	}
	if herdrWorkspaceTabCount(spec, owner, workspaces, tabs) == 0 {
		index := herdrWorkspaceIndex(workspaces, owner)
		if index >= 0 {
			if err := herdrCloseWorkspace(spec.StateDir, owner, index, tabs); err != nil {
				return fail("close workspace after tab close: %v", err)
			}
		}
	}
	_, _ = io.WriteString(os.Stdout, "{\"id\":\"cli:tab:close\",\"result\":{\"type\":\"ok\"}}\n")
	return 0
}

func herdrPaneGet(spec herdrSpec, tabs []herdrTabRef, args []string) int {
	if len(args) < 3 {
		return herdrError("pane_not_found", "pane not found", "cli:pane:get")
	}
	ref := herdrPaneRef(spec, tabs, args[2])
	if ref.Tab.ID == "" && !spec.AllowUnknownPane {
		return herdrError("pane_not_found", "pane "+args[2]+" not found", "cli:pane:get")
	}
	status := spec.PaneStatus
	if spec.PaneStatusFile != "" {
		if data, err := os.ReadFile(spec.PaneStatusFile); err == nil {
			status = strings.TrimSpace(string(data))
		}
	}
	if len(spec.PaneStatusSequence) > 0 {
		index := herdrStatusAdvance(spec.StateDir, len(spec.PaneStatusSequence))
		status = spec.PaneStatusSequence[index]
	}
	if status == "" {
		status = "working"
	}
	if status == "pane-gone" {
		_, _ = io.WriteString(os.Stdout, "{\"id\":\"cli:1\",\"error\":{\"code\":\"not_found\",\"message\":\"pane "+args[2]+" not found\"}}")
		return 0
	}
	agent := spec.PaneAgent
	if spec.PaneAgentEnv {
		agent = os.Getenv("PANE_AGENT")
	}
	if len(spec.Frames) > 0 {
		index := herdrFrameAdvance(spec.StateDir, len(spec.Frames))
		frame := spec.Frames[index]
		agent, status = frame.Agent, frame.Status
		if status == "" {
			status = "idle"
		}
	}
	workspace, _ := herdrTabWorkspace(spec, ref)
	_, _ = fmt.Fprintf(os.Stdout, "{\"id\":\"cli:pane:get\",\"result\":{\"type\":\"pane_info\",\"pane\":{\"pane_id\":%s,\"tab_id\":%s,\"workspace_id\":%s,\"agent\":%s,\"agent_status\":%s}}}\n",
		jsonQuote(args[2]), jsonQuote(ref.Tab.ID), jsonQuote(workspace), jsonQuote(agent), jsonQuote(status))
	return 0
}

func herdrPaneProcessInfo(spec herdrSpec, tabs []herdrTabRef, args []string) int {
	pane := flagValue(args, "--pane")
	if pane == "" || (herdrPaneRef(spec, tabs, pane).Tab.ID == "" && !spec.AllowUnknownPane) {
		return herdrError("pane_not_found", "pane "+pane+" not found", "cli:pane:process_info")
	}
	agents := append([]string(nil), spec.ProcessAgents...)
	if len(agents) == 0 {
		agent := spec.ProcessAgent
		if agent == "" {
			agent = spec.PaneAgent
		}
		if agent != "" {
			agents = []string{agent}
		}
	}
	if spec.PaneAgentEnv {
		agents = []string{os.Getenv("PANE_AGENT")}
	}
	if len(spec.Frames) > 0 {
		index := herdrProcessFrameAdvance(spec.StateDir, len(spec.Frames))
		agents = []string{spec.Frames[index].Agent}
	}
	foreground := `{"pid":1,"name":"bash","argv":["bash"]}`
	for i, agent := range agents {
		if agent == "" {
			continue
		}
		foreground += fmt.Sprintf(`,{"pid":%d,"name":%s,"argv":[%s]}`, i+2, jsonQuote(agent), jsonQuote(agent))
	}
	_, _ = fmt.Fprintf(os.Stdout, `{"id":"cli:pane:process_info","result":{"process_info":{"pane_id":%s,"shell_pid":1,"foreground_processes":[%s]}}}`+"\n", jsonQuote(pane), foreground)
	return 0
}

func herdrPaneRead(spec herdrSpec, tabs []herdrTabRef, args []string) int {
	if len(args) < 3 {
		return herdrError("pane_not_found", "pane not found", "cli:pane:read")
	}
	if herdrPaneRef(spec, tabs, args[2]).Tab.ID == "" && !spec.AllowUnknownPane {
		return herdrError("pane_not_found", "pane "+args[2]+" not found", "cli:pane:read")
	}
	read := spec.PaneReadOut
	if flagValue(args, "--source") == "recent-unwrapped" {
		switch {
		case len(spec.PaneReadUnwrappedSequence) > 0:
			index := herdrUnwrappedReadAdvance(spec.StateDir, len(spec.PaneReadUnwrappedSequence))
			read = spec.PaneReadUnwrappedSequence[index]
		case spec.PaneReadUnwrappedOut != "":
			read = spec.PaneReadUnwrappedOut
		}
	}
	if spec.ReadLogEnv {
		if path := os.Getenv("PANE_LOG"); path != "" {
			if err := appendLine(path, "read"); err != nil {
				return fail("log pane read: %v", err)
			}
		}
	}
	if spec.PaneReadFileEnv {
		path := os.Getenv("PANE_TEXT_FILE")
		if path == "" {
			read = ""
		} else if data, err := os.ReadFile(path); err == nil {
			read = string(data)
		}
	}
	if len(spec.Frames) > 0 {
		index := herdrFrameCurrent(spec.StateDir, len(spec.Frames))
		read = spec.Frames[index].Text
	}
	if read == "" {
		read = herdrDefaultPaneRead
	}
	_, _ = io.WriteString(os.Stdout, read)
	return 0
}

func herdrPaneVoid(spec herdrSpec, tabs []herdrTabRef, command string, args []string) int {
	if len(args) < 3 || herdrPaneRef(spec, tabs, args[2]).Tab.ID == "" {
		return herdrError("pane_not_found", "pane not found", "cli:request")
	}
	keyLog := spec.KeyLog
	if spec.KeyLogEnv {
		keyLog = os.Getenv("PANE_LOG")
	}
	textLog := spec.TextLog
	if spec.TextLogEnv {
		textLog = os.Getenv("PANE_LOG")
	}
	if command == "pane send-keys" && keyLog != "" && len(args) > 3 {
		entry := strings.Join(args[3:], " ")
		if spec.KeyLogEnv {
			entry = "send-keys " + entry
		}
		if err := appendLine(keyLog, entry); err != nil {
			return fail("log pane keys: %v", err)
		}
	}
	if command == "pane send-text" && textLog != "" && len(args) > 3 {
		entry := args[3]
		if spec.TextLogEnv {
			entry = "send-text " + entry
		}
		if err := appendLine(textLog, entry); err != nil {
			return fail("log pane text: %v", err)
		}
	}
	return 0
}

func herdrError(code, message, id string) int {
	_, _ = fmt.Fprintf(os.Stderr, "{\"error\":{\"code\":%s,\"message\":%s},\"id\":%s}\n", jsonQuote(code), jsonQuote(message), jsonQuote(id))
	return 1
}

func herdrTabs(workspaces []HerdrWorkspace, creates []HerdrTab) []herdrTabRef {
	var tabs []herdrTabRef
	for _, ws := range workspaces {
		for _, tab := range ws.Tabs {
			tabs = append(tabs, herdrTabRef{Tab: tab, Index: len(tabs)})
		}
	}
	for _, tab := range creates {
		tabs = append(tabs, herdrTabRef{Tab: tab, Index: len(tabs)})
	}
	return tabs
}

func herdrTabIndex(tabs []herdrTabRef, id string) herdrTabRef {
	for _, ref := range tabs {
		if ref.Tab.ID == id {
			return ref
		}
	}
	return herdrTabRef{}
}

func herdrPaneRef(spec herdrSpec, tabs []herdrTabRef, pane string) herdrTabRef {
	for _, ref := range tabs {
		if ref.Tab.Pane == pane && herdrLive(spec.StateDir, herdrTabPath(spec.StateDir, ref.Index)) {
			return ref
		}
	}
	return herdrTabRef{}
}

func herdrTabWorkspace(spec herdrSpec, ref herdrTabRef) (string, bool) {
	data, err := os.ReadFile(herdrTabPath(spec.StateDir, ref.Index))
	if err != nil {
		return "", false
	}
	owner := strings.TrimSpace(string(data))
	return owner, owner != ""
}

func herdrWorkspaceIndex(workspaces []HerdrWorkspace, id string) int {
	for i, ws := range workspaces {
		if ws.ID == id {
			return i
		}
	}
	return -1
}

func herdrWorkspaceIndexLive(spec herdrSpec, workspaces []HerdrWorkspace, id string) int {
	index := herdrWorkspaceIndex(workspaces, id)
	if index < 0 || !herdrLive(spec.StateDir, herdrWorkspacePath(spec.StateDir, index)) {
		return -1
	}
	return index
}

func herdrWorkspaceTabCount(spec herdrSpec, workspace string, workspaces []HerdrWorkspace, tabs []herdrTabRef) int {
	count := 0
	for _, ref := range tabs {
		owner, ok := herdrTabWorkspace(spec, ref)
		if ok && owner == workspace {
			count++
		}
	}
	return count
}

func herdrCloseWorkspace(state, workspace string, index int, tabs []herdrTabRef) error {
	if err := atomicWrite(herdrWorkspacePath(state, index), ""); err != nil {
		return err
	}
	for _, ref := range tabs {
		owner, err := os.ReadFile(herdrTabPath(state, ref.Index))
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(owner)) == workspace {
			if err := atomicWrite(herdrTabPath(state, ref.Index), ""); err != nil {
				return err
			}
		}
	}
	return nil
}

func herdrLive(state, path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.TrimSpace(string(data)) != ""
}

func herdrLabel(state, prefix string, index int) string {
	data, err := os.ReadFile(herdrLabelPath(state, prefix, index))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func herdrCounter(state, name string) int {
	data, err := os.ReadFile(herdrCounterPath(state, name))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return n
}

func herdrFrameAdvance(state string, count int) int {
	current := herdrCounter(state, "frames")
	if err := atomicWrite(herdrCounterPath(state, "frames"), strconv.Itoa(current+1)+"\n"); err != nil {
		return min(current, count-1)
	}
	return min(current, count-1)
}

func herdrFrameCurrent(state string, count int) int {
	return min(max(herdrCounter(state, "frames")-1, 0), count-1)
}

func herdrProcessFrameAdvance(state string, count int) int {
	current := herdrCounter(state, "process-frames")
	if err := atomicWrite(herdrCounterPath(state, "process-frames"), strconv.Itoa(current+1)+"\n"); err != nil {
		return min(current, count-1)
	}
	return min(current, count-1)
}

func herdrStatusAdvance(state string, count int) int {
	current := herdrCounter(state, "statuses")
	if err := atomicWrite(herdrCounterPath(state, "statuses"), strconv.Itoa(current+1)+"\n"); err != nil {
		return min(current, count-1)
	}
	return min(current, count-1)
}

func herdrUnwrappedReadAdvance(state string, count int) int {
	current := herdrCounter(state, "unwrapped-reads")
	if err := atomicWrite(herdrCounterPath(state, "unwrapped-reads"), strconv.Itoa(current+1)+"\n"); err != nil {
		return min(current, count-1)
	}
	return min(current, count-1)
}

func herdrWorkspacePath(state string, index int) string {
	return filepath.Join(state, fmt.Sprintf("w%d", index))
}

func herdrTabPath(state string, index int) string {
	return filepath.Join(state, fmt.Sprintf("t%d", index))
}

func herdrLabelPath(state, prefix string, index int) string {
	return filepath.Join(state, fmt.Sprintf("%s%d.label", prefix, index))
}

func herdrCounterPath(state, name string) string {
	return filepath.Join(state, name)
}

func ensureFile(t *testing.T, path, content string) {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		writeFile(t, path, content)
	} else if err != nil {
		t.Fatal(err)
	}
}

func appendLine(path, content string) error {
	return appendRawLine(path, content+"\n")
}

func appendRawLine(path, content string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := io.WriteString(file, content)
	closeErr := file.Close()
	return firstError(writeErr, closeErr)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
