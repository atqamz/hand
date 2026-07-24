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

func (c *Client) call(args ...string) (json.RawMessage, error) {
	cmd := exec.Command("herdr", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	trimmed := bytes.TrimSpace(stdout.Bytes())
	if len(trimmed) == 0 {
		if runErr != nil {
			return nil, fmt.Errorf("herdr %s: %w: %s", strings.Join(args, " "), runErr, strings.TrimSpace(stderr.String()))
		}
		return nil, nil
	}

	var env envelope
	if err := json.Unmarshal(trimmed, &env); err != nil {
		return nil, fmt.Errorf("herdr %s: parse response: %w", strings.Join(args, " "), err)
	}
	if env.Error != nil {
		return nil, fmt.Errorf("herdr %s: %s: %s", strings.Join(args, " "), env.Error.Code, env.Error.Message)
	}
	return env.Result, nil
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
	return body.Pane, nil
}

// PaneRun types command into the pane followed by Enter.
func (c *Client) PaneRun(paneID, command string) error {
	_, err := c.call("pane", "run", paneID, command)
	return err
}

func (c *Client) PaneSendText(paneID, text string) error {
	_, err := c.call("pane", "send-text", paneID, text)
	return err
}

func (c *Client) PaneSendKeys(paneID string, keys ...string) error {
	args := append([]string{"pane", "send-keys", paneID}, keys...)
	_, err := c.call(args...)
	return err
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
