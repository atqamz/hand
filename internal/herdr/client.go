package herdr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type Client struct{}

func NewClient() *Client {
	return &Client{}
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type envelope struct {
	Result json.RawMessage `json:"result"`
	Error  *errorBody      `json:"error"`
}

// run execs herdr and returns its trimmed stdout and trimmed stderr alongside
// the process error, letting call and callVoid share one invocation path.
func (c *Client) run(args ...string) ([]byte, string, error) {
	cmd := exec.Command("herdr", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	return bytes.TrimSpace(stdout.Bytes()), strings.TrimSpace(stderr.String()), runErr
}

func parseEnvelope(args []string, trimmed []byte) (envelope, error) {
	var env envelope
	if err := json.Unmarshal(trimmed, &env); err != nil {
		return envelope{}, fmt.Errorf("herdr %s: parse response: %w", strings.Join(args, " "), err)
	}
	return env, nil
}

// call is for query commands, whose real herdr response is always a JSON
// envelope carrying a non-null result object.
func (c *Client) call(args ...string) (json.RawMessage, error) {
	trimmed, stderr, runErr := c.run(args...)

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
		return nil, fmt.Errorf("herdr %s: %s: %s", strings.Join(args, " "), env.Error.Code, env.Error.Message)
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

// callVoid is for void commands (pane run/send-text/send-keys), whose real
// herdr response is empty stdout on success and a JSON error envelope - with
// exit code 0 - on failure, so the error envelope must be checked ahead of
// (and independent from) the process exit status.
func (c *Client) callVoid(args ...string) error {
	trimmed, stderr, runErr := c.run(args...)

	if len(trimmed) == 0 {
		if runErr != nil {
			return fmt.Errorf("herdr %s: %w: %s", strings.Join(args, " "), runErr, stderr)
		}
		return nil
	}

	env, err := parseEnvelope(args, trimmed)
	if err != nil {
		return err
	}
	if env.Error != nil {
		return fmt.Errorf("herdr %s: %s: %s", strings.Join(args, " "), env.Error.Code, env.Error.Message)
	}
	if runErr != nil {
		return fmt.Errorf("herdr %s: %w: %s", strings.Join(args, " "), runErr, stderr)
	}
	return nil
}

func (c *Client) WorkspaceList() ([]Workspace, error) {
	res, err := c.call("workspace", "list")
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

func (c *Client) WorkspaceCreate(cwd, label string) (Workspace, error) {
	args := []string{"workspace", "create", "--no-focus"}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	if label != "" {
		args = append(args, "--label", label)
	}
	res, err := c.call(args...)
	if err != nil {
		return Workspace{}, err
	}
	var body struct {
		Workspace Workspace `json:"workspace"`
	}
	if err := json.Unmarshal(res, &body); err != nil {
		return Workspace{}, fmt.Errorf("parse workspace create: %w", err)
	}
	if body.Workspace.WorkspaceID == "" {
		return Workspace{}, fmt.Errorf("parse workspace create: missing workspace")
	}
	return body.Workspace, nil
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
	res, err := c.call(args...)
	if err != nil {
		return Tab{}, Pane{}, err
	}
	var body struct {
		Tab      Tab  `json:"tab"`
		RootPane Pane `json:"root_pane"`
	}
	if err := json.Unmarshal(res, &body); err != nil {
		return Tab{}, Pane{}, fmt.Errorf("parse tab create: %w", err)
	}
	if body.Tab.TabID == "" || body.RootPane.PaneID == "" {
		return Tab{}, Pane{}, fmt.Errorf("parse tab create: missing tab or root pane")
	}
	return body.Tab, body.RootPane, nil
}

func (c *Client) TabClose(tabID string) error {
	_, err := c.call("tab", "close", tabID)
	return err
}

func (c *Client) PaneGet(paneID string) (Pane, error) {
	res, err := c.call("pane", "get", paneID)
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
			return fmt.Errorf("composer not empty after %s", timeout)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
