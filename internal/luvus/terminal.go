package luvus

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Root struct {
	PID         int    `json:"pid"`
	StartMarker string `json:"start_marker"`
}

type Terminal struct {
	ServerGeneration string `json:"server_generation"`
	TerminalID       string `json:"terminal_id"`
	PaneID           string `json:"pane_id"`
	Root             Root   `json:"root_process"`
	CWD              string `json:"cwd"`
}

type locator struct {
	ServerGeneration string `json:"server_generation"`
	TerminalID       string `json:"terminal_id"`
	PaneID           string `json:"pane_id"`
	ExpectedRoot     Root   `json:"expected_root"`
}

func (t Terminal) locator() locator {
	return locator{t.ServerGeneration, t.TerminalID, t.PaneID, t.Root}
}

func (c Client) Create(ctx context.Context, cwd, label string, argv []string) (Terminal, error) {
	var t Terminal
	err := c.Call(ctx, "terminal.backend.create", map[string]any{
		"cwd":       cwd,
		"placement": map[string]string{"kind": "workspace"},
		"focus":     false,
		"label":     label,
		"command":   argv,
	}, &t)
	return t, err
}

func (c Client) Validate(ctx context.Context, t Terminal) (string, error) {
	var r struct {
		State string `json:"state"`
	}
	err := c.Call(ctx, "terminal.backend.validate", t.locator(), &r)
	return r.State, err
}

func (c Client) Close(ctx context.Context, t Terminal) error {
	return c.Call(ctx, "terminal.backend.close", t.locator(), nil)
}

func (c Client) Inventory(ctx context.Context) ([]Terminal, error) {
	var r struct {
		ServerGeneration string     `json:"server_generation"`
		Terminals        []Terminal `json:"terminals"`
	}
	if err := c.Call(ctx, "terminal.backend.inventory", nil, &r); err != nil {
		return nil, err
	}
	for i := range r.Terminals {
		r.Terminals[i].ServerGeneration = r.ServerGeneration
	}
	return r.Terminals, nil
}

type Agent struct {
	Pane   string
	Agent  string
	Status string
	Hint   string
}

func (c Client) Explain(ctx context.Context, pane string) (Agent, error) {
	var r struct {
		Pane          string `json:"pane"`
		Agent         string `json:"agent"`
		Status        string `json:"status"`
		StateEvidence struct {
			BlockedHint string `json:"blocked_hint"`
		} `json:"state_evidence"`
	}
	err := c.Call(ctx, "agent.explain", map[string]string{"pane": pane}, &r)
	return Agent{Pane: r.Pane, Agent: r.Agent, Status: r.Status, Hint: r.StateEvidence.BlockedHint}, err
}

func (c Client) Prompt(ctx context.Context, pane, text string) error {
	return c.Call(ctx, "agent.prompt", map[string]any{"target": pane, "text": text, "wait": false}, nil)
}

type Screen struct {
	Text            string `json:"text"`
	ContentRevision int64  `json:"content_revision"`
	TerminalID      string `json:"terminal_id"`
}

func (c Client) Read(ctx context.Context, pane string, lines int) (Screen, error) {
	var s Screen
	err := c.Call(ctx, "agent.read", map[string]any{"target": pane, "source": "visible", "lines": lines}, &s)
	return s, err
}

func (c Client) Keys(ctx context.Context, pane string, keys []string, revision int64, terminalID string) error {
	return c.Call(ctx, "agent.keys", map[string]any{"target": pane, "keys": keys, "if_content_revision": revision, "terminal_id": terminalID}, nil)
}

func ProcStartMarker(pid int) (string, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	s := string(b)
	i := strings.LastIndex(s, ") ")
	if i < 0 {
		return "", fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	f := strings.Fields(s[i+2:])
	if len(f) < 20 {
		return "", fmt.Errorf("short /proc/%d/stat", pid)
	}
	return f[19], nil
}
