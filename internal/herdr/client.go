package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/launch"
	"github.com/atqamz/hand/internal/toolchain"
)

// ErrComposerBusyTimeout marks WaitComposerEmpty's own deadline expiry, which a caller has to tell
// apart from the PaneGet failures the same function returns: a composer that stayed busy is
// transient and worth retrying, a pane that stopped answering will never answer a retry.
var ErrComposerBusyTimeout = errors.New("composer still busy")

// ComposerBusyError preserves the observed pane state when a composer never becomes ready.
type ComposerBusyError struct {
	Timeout          time.Duration
	AgentStatus      Status
	BackgroundShells int
	ShellsKnown      bool
}

func (e *ComposerBusyError) Error() string {
	return fmt.Sprintf("%s after %s (%s)", ErrComposerBusyTimeout, e.Timeout, e.Detail())
}

func (e *ComposerBusyError) Unwrap() error { return ErrComposerBusyTimeout }

func (e *ComposerBusyError) Detail() string {
	detail := fmt.Sprintf("agent_status=%q", e.AgentStatus)
	if e.ShellsKnown {
		detail += fmt.Sprintf(", background_shells=%d", e.BackgroundShells)
	}
	return detail
}

type Client struct {
	session    string
	executable string
	env        []string
	childEnv   []string
	initErr    error
}

func NewClient() *Client {
	return &Client{executable: "herdr"}
}

func NewClientAt(path string, env []string) (*Client, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("managed Herdr executable %q must be an absolute path", path)
	}
	return &Client{executable: path, env: append([]string(nil), env...)}, nil
}

func NewManagedClient() *Client {
	if legacyHerdrFallback {
		if path := os.Getenv("HAND_TEST_HERDR_PATH"); path != "" {
			client, err := NewClientAt(path, os.Environ())
			if err == nil {
				return client
			}
		}
	}
	store, err := toolchain.DefaultStore()
	if err != nil {
		if legacyHerdrFallback {
			return NewClient()
		}
		return &Client{initErr: err}
	}
	runtime, err := store.Selected("", "")
	if err != nil {
		if legacyHerdrFallback {
			return NewClient()
		}
		return &Client{initErr: err}
	}
	env, err := toolchain.ManagedEnvironment(os.Environ(), runtime.GitBin)
	if err != nil {
		if legacyHerdrFallback {
			return NewClient()
		}
		return &Client{initErr: err}
	}
	client, err := NewClientAt(runtime.HerdrPath, env)
	if err != nil {
		if legacyHerdrFallback {
			return NewClient()
		}
		return &Client{initErr: err}
	}
	client.childEnv = []string{"PATH=" + environmentValue(env, "PATH")}
	return client
}

func NewSessionClient(session string) *Client {
	return &Client{session: session}
}

func NewManagedSessionClient(session string) *Client {
	client := NewManagedClient()
	client.session = session
	return client
}

func (c *Client) WorkerEnvironment() map[string]string {
	result := make(map[string]string)
	for _, item := range c.childEnv {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			result[key] = value
		}
	}
	return result
}

// The harness-identity variables a pane must never inherit from the herdr server it is a child of
// (atqamz/hand#109). A server inside a Claude Code session passes its own session identity
// down, and CLAUDE_CODE_CHILD_SESSION disables the pane's transcript - a worker's only record.
var sanitizedEnvKeys = []string{"CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID", "CLAUDECODE"}

func (c *Client) childEnvArgs(extra map[string]string) []string {
	values := make(map[string]string, len(sanitizedEnvKeys)+len(c.childEnv)+len(extra))
	for _, key := range sanitizedEnvKeys {
		values[key] = ""
	}
	for _, item := range c.childEnv {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range extra {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	args := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		args = append(args, "--env", key+"="+values[key])
	}
	return args
}

func environmentValue(env []string, key string) string {
	for _, item := range env {
		name, value, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(name, key) {
			return value
		}
	}
	return ""
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type envelope struct {
	Result json.RawMessage `json:"result"`
	Error  *errorBody      `json:"error"`
}

var ErrNotFound = errors.New("herdr resource not found")

var ErrServerNotRunning = errors.New("herdr server is not running")

type ExecError struct {
	Started bool
	Err     error
}

func (e *ExecError) Error() string {
	return e.Err.Error()
}

func (e *ExecError) Unwrap() error {
	return e.Err
}

func IsProcessNotStarted(err error) bool {
	var execErr *ExecError
	return errors.As(err, &execErr) && !execErr.Started
}

type APIError struct {
	Operation              string
	Code                   string
	Message                string
	PreSideEffectRejection bool
}

func (e *APIError) Error() string {
	return fmt.Sprintf("herdr %s: %s: %s", e.Operation, e.Code, e.Message)
}

func (e *APIError) Unwrap() error {
	code := strings.ToLower(e.Code)
	if code == "server_not_running" {
		return ErrServerNotRunning
	}
	if code == "not_found" || strings.HasSuffix(code, "_not_found") {
		return ErrNotFound
	}
	return nil
}

type ServerNotRunningError struct {
	Session string
	Cause   error
}

func (e *ServerNotRunningError) Error() string {
	if e.Session == "" || e.Session == "default" {
		return "Herdr server is not running"
	}
	return fmt.Sprintf("Fleet Herdr session %q is not running", e.Session)
}

func (e *ServerNotRunningError) Unwrap() error { return e.Cause }

func IsServerNotRunning(err error) bool {
	return errors.Is(err, ErrServerNotRunning)
}

func IsPreSideEffectRejection(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.PreSideEffectRejection
}

// Execs herdr and returns its trimmed stdout and trimmed stderr alongside the process error,
// letting call and callVoid share one invocation path.
func (c *Client) run(args ...string) ([]byte, string, error) {
	return c.runContext(context.Background(), args...)
}

func (c *Client) runContext(ctx context.Context, args ...string) ([]byte, string, error) {
	executable := "herdr"
	var env []string
	wireArgs := args
	if c != nil {
		if c.initErr != nil {
			return nil, "", c.initErr
		}
		if c.executable != "" {
			executable = c.executable
		}
		env = c.env
		wireArgs = c.wireArgs(args...)
	}
	cmd := exec.CommandContext(ctx, executable, wireArgs...)
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = daemonEnvironment(env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr != nil {
		runErr = &ExecError{Started: cmd.ProcessState != nil, Err: runErr}
	}
	return bytes.TrimSpace(stdout.Bytes()), strings.TrimSpace(stderr.String()), runErr
}

func (c *Client) startServer(ctx context.Context) error {
	if c == nil {
		return errors.New("managed Herdr client is unavailable")
	}
	if c.initErr != nil {
		return c.initErr
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	executable := c.executable
	if executable == "" {
		executable = "herdr"
	}
	if !filepath.IsAbs(executable) {
		return fmt.Errorf("managed Herdr executable %q must be an absolute path", executable)
	}
	cmd := exec.Command(executable, c.wireArgs("server")...)
	parentEnv := c.env
	if parentEnv == nil {
		parentEnv = os.Environ()
	}
	cmd.Env = daemonEnvironment(parentEnv)
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open detached Herdr stdio: %w", err)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devNull, devNull, devNull
	detachCommand(cmd)
	if err := cmd.Start(); err != nil {
		_ = devNull.Close()
		return &ExecError{Started: false, Err: err}
	}
	_ = devNull.Close()
	_ = cmd.Process.Release()
	return nil
}

func (c *Client) attach(ctx context.Context) error {
	if c == nil {
		return errors.New("managed Herdr client is unavailable")
	}
	if c.initErr != nil {
		return c.initErr
	}
	if c.session == "" {
		return errors.New("herdr session identity is unavailable")
	}
	executable := c.executable
	if executable == "" {
		executable = "herdr"
	}
	if !filepath.IsAbs(executable) {
		return fmt.Errorf("managed Herdr executable %q must be an absolute path", executable)
	}
	cmd := exec.CommandContext(ctx, executable, "session", "attach", c.session)
	if c.env != nil {
		cmd.Env = daemonEnvironment(c.env)
	} else {
		cmd.Env = daemonEnvironment(os.Environ())
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("attach Herdr session %q: %w", c.session, err)
	}
	return nil
}

func (c *Client) wireArgs(args ...string) []string {
	if c.session == "" {
		return args
	}
	wire := make([]string, 0, len(args)+2)
	wire = append(wire, "--session", c.session)
	return append(wire, args...)
}

func parseEnvelope(args []string, trimmed []byte) (envelope, error) {
	var env envelope
	if err := json.Unmarshal(trimmed, &env); err != nil {
		return envelope{}, fmt.Errorf("herdr %s: parse response: %w", strings.Join(args, " "), err)
	}
	return env, nil
}

// For query commands, whose real herdr response is always a JSON envelope carrying a non-null
// result object.
func (c *Client) call(args ...string) (json.RawMessage, error) {
	return c.callContext(context.Background(), args...)
}

func (c *Client) callContext(ctx context.Context, args ...string) (json.RawMessage, error) {
	trimmed, stderr, runErr := c.runContext(ctx, args...)

	if len(trimmed) == 0 {
		if runErr != nil {
			return nil, fmt.Errorf("herdr %s: %w: %s", strings.Join(args, " "), runErr, stderr)
		}
		return nil, fmt.Errorf("herdr %s: empty response", strings.Join(args, " "))
	}

	env, err := parseEnvelope(args, trimmed)
	if err != nil {
		return nil, err
	}
	if env.Error != nil {
		return nil, c.normalizeAPIError(newAPIError(args, env.Error))
	}
	if runErr != nil {
		return nil, fmt.Errorf("herdr %s: %w: %s", strings.Join(args, " "), runErr, stderr)
	}
	if len(env.Result) == 0 {
		return nil, fmt.Errorf("herdr %s: response missing result", strings.Join(args, " "))
	}
	result := bytes.TrimSpace(env.Result)
	if string(result) == "null" {
		return nil, fmt.Errorf("herdr %s: response has null result", strings.Join(args, " "))
	}
	if result[0] != '{' {
		return nil, fmt.Errorf("herdr %s: response result is not an object", strings.Join(args, " "))
	}
	return result, nil
}

// For void commands (pane run/send-text/send-keys), success has empty stdout. Failure may be a
// structured envelope on stderr, so it must be checked ahead of the process exit status.
func (c *Client) callVoid(args ...string) error {
	trimmed, stderr, runErr := c.run(args...)

	if len(trimmed) == 0 {
		if runErr != nil {
			if env, err := parseEnvelope(args, []byte(stderr)); err == nil && env.Error != nil {
				return c.normalizeAPIError(newAPIError(args, env.Error))
			}
			return fmt.Errorf("herdr %s: %w: %s", strings.Join(args, " "), runErr, stderr)
		}
		return nil
	}

	env, err := parseEnvelope(args, trimmed)
	if err != nil {
		return err
	}
	if env.Error != nil {
		return c.normalizeAPIError(newAPIError(args, env.Error))
	}
	if runErr != nil {
		return fmt.Errorf("herdr %s: %w: %s", strings.Join(args, " "), runErr, stderr)
	}
	return nil
}

func (c *Client) normalizeAPIError(err error) error {
	if !IsServerNotRunning(err) {
		return err
	}
	session := ""
	if c != nil {
		session = c.session
	}
	return &ServerNotRunningError{Session: session, Cause: err}
}

func newAPIError(args []string, body *errorBody) *APIError {
	operation := strings.Join(args, " ")
	command := strings.Join(args[:minInt(len(args), 2)], " ")
	preSideEffect := command == "pane send-text" &&
		(strings.EqualFold(body.Code, "pane_not_found") || strings.EqualFold(body.Code, "pane_send_failed"))
	if command == "pane send-keys" && len(args) == 4 && strings.EqualFold(args[3], "Enter") {
		preSideEffect = strings.EqualFold(body.Code, "pane_not_found") || strings.EqualFold(body.Code, "pane_send_failed")
	}
	return &APIError{Operation: operation, Code: body.Code, Message: body.Message, PreSideEffectRejection: preSideEffect}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (c *Client) WorkspaceList() ([]Workspace, error) {
	return c.workspaceList(context.Background())
}

func (c *Client) WorkspaceListContext(ctx context.Context) ([]Workspace, error) {
	return c.workspaceList(ctx)
}

func (c *Client) workspaceList(ctx context.Context) ([]Workspace, error) {
	res, err := c.callContext(ctx, "workspace", "list")
	if err != nil {
		return nil, err
	}
	var body struct {
		Workspaces []Workspace `json:"workspaces"`
	}
	if err := json.Unmarshal(res, &body); err != nil {
		return nil, fmt.Errorf("parse workspace list: %w", err)
	}
	if body.Workspaces == nil {
		return nil, fmt.Errorf("parse workspace list: missing workspaces")
	}
	return body.Workspaces, nil
}

// FindWorkspaceByLabel returns the workspace whose label matches, if any.
func (c *Client) FindWorkspaceByLabel(label string) (Workspace, bool, error) {
	workspaces, err := c.WorkspaceList()
	if err != nil {
		return Workspace{}, false, err
	}
	for _, w := range workspaces {
		if w.Label == label {
			return w, true, nil
		}
	}
	return Workspace{}, false, nil
}

// WorkspaceCreate also returns the root tab and pane herdr creates at cwd as a side effect of
// creating the workspace - herdr has no way to create an empty workspace - so a caller that
// discards them leaves a live, unowned shell behind in the workspace.
func (c *Client) WorkspaceCreate(cwd string, env map[string]string, label string) (Workspace, Tab, Pane, error) {
	args := []string{"workspace", "create", "--no-focus"}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	if label != "" {
		args = append(args, "--label", label)
	}
	args = append(args, c.childEnvArgs(env)...)
	res, err := c.call(args...)
	if err != nil {
		return Workspace{}, Tab{}, Pane{}, err
	}
	var body struct {
		Workspace Workspace `json:"workspace"`
		Tab       Tab       `json:"tab"`
		RootPane  Pane      `json:"root_pane"`
	}
	// A workspace ID on a failure path below means herdr created the workspace before the response
	// came back unusable (only reachable against a protocol predating tab/root_pane), so it closes
	// here or nothing ever learns it exists. Unmarshal decodes past its first type error, ID too.
	failed := func(parseErr error) (Workspace, Tab, Pane, error) {
		if body.Workspace.WorkspaceID != "" {
			if closeErr := c.WorkspaceClose(body.Workspace.WorkspaceID); closeErr != nil {
				return Workspace{}, Tab{}, Pane{}, fmt.Errorf("%w; cleanup failed: %w", parseErr, closeErr)
			}
		}
		return Workspace{}, Tab{}, Pane{}, parseErr
	}
	if err := json.Unmarshal(res, &body); err != nil {
		return failed(fmt.Errorf("parse workspace create: %w", err))
	}
	if body.Workspace.WorkspaceID == "" || body.Tab.TabID == "" || body.RootPane.PaneID == "" {
		return failed(fmt.Errorf("parse workspace create: missing workspace, tab, or root pane"))
	}
	return body.Workspace, body.Tab, body.RootPane, nil
}

func (c *Client) WorkspaceClose(workspaceID string) error {
	_, err := c.call("workspace", "close", workspaceID)
	return err
}

func (c *Client) TabList(workspaceID string) ([]Tab, error) {
	res, err := c.call("tab", "list", "--workspace", workspaceID)
	if err != nil {
		return nil, err
	}
	var body struct {
		Tabs []Tab `json:"tabs"`
	}
	if err := json.Unmarshal(res, &body); err != nil {
		return nil, fmt.Errorf("parse tab list: %w", err)
	}
	if body.Tabs == nil {
		return nil, fmt.Errorf("parse tab list: missing tabs")
	}
	return body.Tabs, nil
}

func (c *Client) TabCreate(workspaceID, cwd string, env map[string]string, label string) (Tab, Pane, error) {
	args := []string{"tab", "create", "--workspace", workspaceID, "--no-focus"}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	if label != "" {
		args = append(args, "--label", label)
	}
	args = append(args, c.childEnvArgs(env)...)
	res, err := c.call(args...)
	if err != nil {
		return Tab{}, Pane{}, err
	}
	var body struct {
		Tab      Tab  `json:"tab"`
		RootPane Pane `json:"root_pane"`
	}
	// Same shape as WorkspaceCreate's failed() above: a tab ID on any failure path below means
	// herdr already created the tab before the response came back unusable, so it must be closed
	// here or nothing else ever learns the tab exists to clean it up.
	failed := func(parseErr error) (Tab, Pane, error) {
		if body.Tab.TabID != "" {
			if closeErr := c.TabClose(body.Tab.TabID); closeErr != nil {
				return Tab{}, Pane{}, fmt.Errorf("%w; cleanup failed: %w", parseErr, closeErr)
			}
		}
		return Tab{}, Pane{}, parseErr
	}
	if err := json.Unmarshal(res, &body); err != nil {
		return failed(fmt.Errorf("parse tab create: %w", err))
	}
	if body.Tab.TabID == "" || body.RootPane.PaneID == "" {
		return failed(fmt.Errorf("parse tab create: missing tab or root pane"))
	}
	return body.Tab, body.RootPane, nil
}

func (c *Client) TabRename(tabID, label string) error {
	_, err := c.call("tab", "rename", tabID, label)
	return err
}

func (c *Client) TabClose(tabID string) error {
	_, err := c.call("tab", "close", tabID)
	return err
}

func (c *Client) PaneGet(paneID string) (Pane, error) {
	return c.paneGet(context.Background(), paneID)
}

func (c *Client) PaneGetContext(ctx context.Context, paneID string) (Pane, error) {
	return c.paneGet(ctx, paneID)
}

func (c *Client) paneGet(ctx context.Context, paneID string) (Pane, error) {
	res, err := c.callContext(ctx, "pane", "get", paneID)
	if err != nil {
		return Pane{}, err
	}
	var body struct {
		Pane Pane `json:"pane"`
	}
	if err := json.Unmarshal(res, &body); err != nil {
		return Pane{}, fmt.Errorf("parse pane get: %w", err)
	}
	if body.Pane.PaneID == "" {
		return Pane{}, fmt.Errorf("parse pane get: missing pane")
	}
	return body.Pane, nil
}

func (c *Client) PaneProcessInfo(paneID string) (ProcessInfo, error) {
	res, err := c.call("pane", "process-info", "--pane", paneID)
	if err != nil {
		return ProcessInfo{}, err
	}
	var body struct {
		ProcessInfo ProcessInfo `json:"process_info"`
	}
	if err := json.Unmarshal(res, &body); err != nil {
		return ProcessInfo{}, fmt.Errorf("parse pane process info: %w", err)
	}
	if body.ProcessInfo.PaneID == "" || body.ProcessInfo.PaneID != paneID || body.ProcessInfo.ShellPID <= 0 {
		return ProcessInfo{}, fmt.Errorf("parse pane process info: missing or mismatched pane or shell pid")
	}
	return body.ProcessInfo, nil
}

func (c *Client) paneRun(paneID, command string) error {
	return c.callVoid("pane", "run", paneID, command)
}

func (c *Client) PaneRunSpec(paneID string, spec launch.LaunchSpec) error {
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("validate launch spec: %w", err)
	}
	info, err := c.PaneProcessInfo(paneID)
	if err != nil {
		return fmt.Errorf("observe pane shell: %w", err)
	}
	shell, err := shellForProcess(info)
	if err != nil {
		return err
	}
	var command string
	switch shell {
	case shellPOSIX:
		command, err = renderPOSIX(spec.Executable, spec.Args)
	case shellPowerShell:
		command, err = renderPowerShell(spec.Executable, spec.Args)
	}
	if err != nil {
		return fmt.Errorf("render launch for %s: %w", shell, err)
	}
	return c.paneRun(paneID, command)
}

func (c *Client) PaneSendText(paneID, text string) error {
	return c.callVoid("pane", "send-text", paneID, text)
}

func (c *Client) PaneSendKeys(paneID string, keys ...string) error {
	args := append([]string{"pane", "send-keys", paneID}, keys...)
	return c.callVoid(args...)
}

// PaneRead returns the pane's recent scrollback as plain text, and a failure as an error rather than as
// text, since a failure read as pane text would confirm a worker nobody observed. Re-answering a dialog
// that lingers in scrollback is prevented in internal/runtime/launch.go, by answering each one once per launch.
func (c *Client) PaneRead(paneID string, lines int) (string, error) {
	// The one caller matches first-run dialogs on their lower half, so a viewport too short for the whole
	// dialog clips exactly the text that has to match, and an unmatched dialog under a live agent reads as
	// started. Hence recent, not visible's 23-row unattached viewport (internal/faketool/FIDELITY.md).
	args := []string{"pane", "read", paneID, "--source", "recent", "--lines", strconv.Itoa(lines)}
	stdout, stderr, runErr := c.run(args...)
	// Unlike every command above, herdr's contract for pane read is a third shape: raw text on
	// success, on failure a bare {"code","message"} object, not the {"error":{...}} envelope. Read
	// ahead of the exit status for callVoid's reason - that code cannot be trusted on its own.
	var eb errorBody
	if json.Unmarshal(stdout, &eb) == nil && eb.Code != "" && eb.Message != "" {
		return "", c.normalizeAPIError(&APIError{Operation: strings.Join(args, " "), Code: eb.Code, Message: eb.Message})
	}
	if runErr != nil {
		return "", fmt.Errorf("herdr %s: %w: %s", strings.Join(args, " "), runErr, stderr)
	}
	return string(stdout), nil
}

// WaitComposerEmpty polls pane state and its recent rendering until the agent is no longer "working",
// the rendered composer is visibly empty, or timeout elapses. There is no herdr primitive for "not
// working", so this combines the pane signals rather than depending on a single-target wait command.
func (c *Client) WaitComposerEmpty(paneID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var shellsKnown bool
	var backgroundShells int
	var lastStatus Status
	for {
		pane, err := c.PaneGet(paneID)
		if err != nil {
			return err
		}
		lastStatus = pane.AgentStatus
		if pane.AgentStatus != StatusWorking {
			return nil
		}
		if text, err := c.PaneRead(paneID, 20); err == nil {
			if count, ok := renderedBackgroundShells(text); ok {
				backgroundShells, shellsKnown = count, true
			}
			if renderedEmptyComposer(text) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return &ComposerBusyError{Timeout: timeout, AgentStatus: lastStatus, BackgroundShells: backgroundShells, ShellsKnown: shellsKnown}
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func renderedEmptyComposer(text string) bool {
	lines := strings.Split(strings.ReplaceAll(text, "\r", ""), "\n")
	start := len(lines) - 6
	if start < 0 {
		start = 0
	}
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(strings.ToLower(trimmed), "esc to interrupt") {
			return false
		}
		if trimmed == "❯" || trimmed == ">" || trimmed == "›" || trimmed == "»" {
			return true
		}
	}
	return false
}

func renderedBackgroundShells(text string) (int, bool) {
	fields := strings.Fields(text)
	for i := 1; i < len(fields); i++ {
		if fields[i] != "shells" && fields[i] != "shell" {
			continue
		}
		count, err := strconv.Atoi(fields[i-1])
		if err == nil && count >= 0 {
			return count, true
		}
	}
	return 0, false
}
