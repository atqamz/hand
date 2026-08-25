package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type SessionState string

const (
	SessionRunningCompatible SessionState = "running-compatible"
	SessionStopped           SessionState = "stopped"
	SessionUnknown           SessionState = "unknown"
	SessionIncompatible      SessionState = "incompatible"
)

type SessionObservation struct {
	Name   string       `json:"name"`
	State  SessionState `json:"state"`
	Reason string       `json:"reason"`
}

type sessionRecord struct {
	Name    string `json:"name"`
	Running *bool  `json:"running"`
}

type serverStatus struct {
	Status     string `json:"status"`
	Running    *bool  `json:"running"`
	Compatible *bool  `json:"compatible"`
	Session    string `json:"session"`
}

func ObserveFleetSession(ctx context.Context, fleetID string) SessionObservation {
	if strings.TrimSpace(fleetID) == "" {
		return SessionObservation{State: SessionUnknown, Reason: "Fleet identity is unavailable"}
	}
	name := SessionName(fleetID)
	return NewManagedSessionClient(name).ObserveSession(ctx)
}

func (c *Client) ObserveSession(ctx context.Context) SessionObservation {
	name := ""
	if c != nil {
		name = c.session
	}
	if name == "" {
		return SessionObservation{State: SessionUnknown, Reason: "Herdr session identity is unavailable"}
	}

	sessions, err := c.sessionListContext(ctx)
	if err != nil {
		return unknownSessionObservation(name, err)
	}
	var target *sessionRecord
	for i := range sessions {
		if sessions[i].Name == name {
			target = &sessions[i]
			break
		}
	}
	if target == nil {
		return SessionObservation{Name: name, State: SessionStopped, Reason: "exact Fleet Herdr session is not present"}
	}
	if target.Running == nil {
		return unknownSessionObservation(name, errors.New("session inventory omitted running state"))
	}
	if !*target.Running {
		return SessionObservation{Name: name, State: SessionStopped, Reason: "exact Fleet Herdr session is stopped"}
	}

	status, err := c.serverStatusContext(ctx)
	if err != nil {
		if IsServerNotRunning(err) {
			return SessionObservation{Name: name, State: SessionStopped, Reason: "exact Fleet Herdr session is stopped"}
		}
		return unknownSessionObservation(name, err)
	}
	if status.Running == nil {
		return unknownSessionObservation(name, errors.New("server status omitted running state"))
	}
	if !*status.Running {
		return SessionObservation{Name: name, State: SessionStopped, Reason: "exact Fleet Herdr session is stopped"}
	}
	if status.Session != "" && status.Session != name {
		return unknownSessionObservation(name, fmt.Errorf("server status named session %q", status.Session))
	}
	if strings.EqualFold(status.Status, "incompatible") || (status.Compatible != nil && !*status.Compatible) {
		return SessionObservation{Name: name, State: SessionIncompatible, Reason: "exact Fleet Herdr session is protocol-incompatible"}
	}
	if status.Compatible == nil {
		return unknownSessionObservation(name, errors.New("server status omitted compatibility"))
	}
	return SessionObservation{Name: name, State: SessionRunningCompatible, Reason: "exact Fleet Herdr session is running with a compatible protocol"}
}

func (c *Client) sessionListContext(ctx context.Context) ([]sessionRecord, error) {
	data, err := c.rawJSONContext(ctx, "session", "list", "--json")
	if err != nil {
		return nil, err
	}
	var body struct {
		Sessions []sessionRecord `json:"sessions"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("parse Herdr session list: %w", err)
	}
	if body.Sessions == nil {
		return nil, errors.New("parse Herdr session list: missing sessions")
	}
	return body.Sessions, nil
}

func (c *Client) serverStatusContext(ctx context.Context) (serverStatus, error) {
	data, err := c.rawJSONContext(ctx, "status", "server", "--json")
	if err != nil {
		return serverStatus{}, err
	}
	var status serverStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return serverStatus{}, fmt.Errorf("parse Herdr server status: %w", err)
	}
	return status, nil
}

func (c *Client) rawJSONContext(ctx context.Context, args ...string) ([]byte, error) {
	stdout, stderr, runErr := c.runContext(ctx, args...)
	for _, output := range [][]byte{stdout, []byte(stderr)} {
		if apiErr := parseAPIError(args, output); apiErr != nil {
			return nil, c.normalizeAPIError(apiErr)
		}
	}
	if runErr != nil {
		return nil, fmt.Errorf("herdr %s: %w: %s", strings.Join(args, " "), runErr, stderr)
	}
	if len(stdout) == 0 {
		return nil, fmt.Errorf("herdr %s: empty response", strings.Join(args, " "))
	}
	return stdout, nil
}

func parseAPIError(args []string, data []byte) *APIError {
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err == nil && env.Error != nil {
		return newAPIError(args, env.Error)
	}
	var body errorBody
	if err := json.Unmarshal(data, &body); err == nil && body.Code != "" && body.Message != "" {
		return &APIError{Operation: strings.Join(args, " "), Code: body.Code, Message: body.Message}
	}
	return nil
}

func unknownSessionObservation(name string, err error) SessionObservation {
	reason := strings.Join(strings.Fields(err.Error()), " ")
	reason = strings.NewReplacer("{", "", "}", "", "\"", "").Replace(reason)
	if len(reason) > 160 {
		reason = reason[:160]
	}
	return SessionObservation{Name: name, State: SessionUnknown, Reason: "could not observe exact Fleet Herdr session: " + reason}
}
