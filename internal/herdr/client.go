package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ErrComposerBusyTimeout marks WaitComposerEmpty's own deadline expiry, which a caller has to tell
// apart from the PaneGet failures the same function returns: a composer that stayed busy is
// transient and worth retrying, a pane that stopped answering will never answer a retry.
var ErrComposerBusyTimeout = errors.New("composer still busy")

type Client struct{}

func NewClient() *Client {
	return &Client{}
}

// The harness-identity variables a pane must never inherit from the herdr server it is a child of
// (atqamz/hand#109). A server inside a Claude Code session passes its own session identity
// down, and CLAUDE_CODE_CHILD_SESSION disables the pane's transcript - a worker's only record.
var sanitizedEnvKeys = []string{"CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID", "CLAUDECODE"}

// Blanking them at creation removes the failure for every pane hand creates, rather than depending
// on the herdr server's own environment being clean.
func sanitizedEnvArgs() []string {
	args := make([]string, 0, len(sanitizedEnvKeys)*2)
	for _, key := range sanitizedEnvKeys {
		args = append(args, "--env", key+"=")
	}
	return args
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
	if code == "not_found" || strings.HasSuffix(code, "_not_found") {
		return ErrNotFound
	}
	return nil
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
	cmd := exec.CommandContext(ctx, "herdr", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr != nil {
		runErr = &ExecError{Started: cmd.ProcessState != nil, Err: runErr}
	}
	return bytes.TrimSpace(stdout.Bytes()), strings.TrimSpace(stderr.String()), runErr
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
		return nil, newAPIError(args, env.Error)
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
				return newAPIError(args, env.Error)
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
		return newAPIError(args, env.Error)
	}
	if runErr != nil {
		return fmt.Errorf("herdr %s: %w: %s", strings.Join(args, " "), runErr, stderr)
	}
	return nil
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
func (c *Client) WorkspaceCreate(cwd, label string) (Workspace, Tab, Pane, error) {
	args := []string{"workspace", "create", "--no-focus"}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	if label != "" {
		args = append(args, "--label", label)
	}
	args = append(args, sanitizedEnvArgs()...)
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

func (c *Client) TabCreate(workspaceID, cwd, label string) (Tab, Pane, error) {
	args := []string{"tab", "create", "--workspace", workspaceID, "--no-focus"}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	if label != "" {
		args = append(args, "--label", label)
	}
	args = append(args, sanitizedEnvArgs()...)
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

// PaneRun types command into the pane followed by Enter.
func (c *Client) PaneRun(paneID, command string) error {
	return c.callVoid("pane", "run", paneID, command)
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
		return "", fmt.Errorf("herdr %s: %s: %s", strings.Join(args, " "), eb.Code, eb.Message)
	}
	if runErr != nil {
		return "", fmt.Errorf("herdr %s: %w: %s", strings.Join(args, " "), runErr, stderr)
	}
	return string(stdout), nil
}

// WaitComposerEmpty polls the pane until the agent is no longer "working" or timeout elapses.
// There is no herdr primitive for "not working", so this polls pane state directly
// rather than depending on a single-target wait command.
func (c *Client) WaitComposerEmpty(paneID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		pane, err := c.PaneGet(paneID)
		if err != nil {
			return err
		}
		if pane.AgentStatus != StatusWorking {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w after %s", ErrComposerBusyTimeout, timeout)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
