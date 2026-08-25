package herdr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/filelock"
	"github.com/atqamz/hand/internal/secondhand"
)

const (
	defaultHerdrStartTimeout = 15 * time.Second
	defaultHerdrPollInterval = 100 * time.Millisecond
)

var (
	ErrSessionUnknown      = errors.New("fleet Herdr session observation is unknown")
	ErrSessionIncompatible = errors.New("fleet Herdr session is incompatible")
	ErrEnsureTimeout       = errors.New("timed out waiting for Fleet Herdr session")
)

type SessionEnsureError struct {
	Observation SessionObservation
	Cause       error
}

func (e *SessionEnsureError) Error() string {
	if e.Observation.Name == "" {
		return fmt.Sprintf("ensure Fleet Herdr session: %v", e.Cause)
	}
	if e.Observation.Reason == "" {
		return fmt.Sprintf("ensure Fleet Herdr session %q: %v", e.Observation.Name, e.Cause)
	}
	return fmt.Sprintf("ensure Fleet Herdr session %q: %v (%s)", e.Observation.Name, e.Cause, e.Observation.Reason)
}

func (e *SessionEnsureError) Unwrap() error { return e.Cause }

// FleetHerdr owns the exact Herdr namespace for one canonical Fleet identity.
type FleetHerdr struct {
	fleetID string
	session string
	client  *Client

	observeFn func(context.Context) SessionObservation
	startFn   func(context.Context) error
	attachFn  func(context.Context) error
	lockFn    func(context.Context) (func(), error)

	startTimeout time.Duration
	pollInterval time.Duration
}

func NewFleetHerdr(fleetID string) *FleetHerdr {
	return &FleetHerdr{
		fleetID:      fleetID,
		session:      SessionName(fleetID),
		client:       NewManagedSessionClient(SessionName(fleetID)),
		startTimeout: defaultHerdrStartTimeout,
		pollInterval: defaultHerdrPollInterval,
	}
}

func (f *FleetHerdr) Session() string {
	if f == nil {
		return ""
	}
	return f.session
}

func (f *FleetHerdr) Observe(ctx context.Context) SessionObservation {
	if f == nil {
		return SessionObservation{State: SessionUnknown, Reason: "Fleet Herdr runtime is unavailable"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(f.fleetID) == "" {
		return SessionObservation{Name: f.session, State: SessionUnknown, Reason: "Fleet identity is unavailable"}
	}
	if f.observeFn != nil {
		return f.observeFn(ctx)
	}
	if f.client == nil {
		return SessionObservation{Name: f.session, State: SessionUnknown, Reason: "managed Herdr client is unavailable"}
	}
	return f.client.ObserveSession(ctx)
}

func (f *FleetHerdr) Ensure(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	observation := f.Observe(ctx)
	switch observation.State {
	case SessionRunningCompatible:
		return nil
	case SessionUnknown:
		return sessionEnsureError(observation, ErrSessionUnknown)
	case SessionIncompatible:
		return sessionEnsureError(observation, ErrSessionIncompatible)
	case SessionStopped:
		return f.ensureStopped(ctx)
	default:
		return sessionEnsureError(observation, ErrSessionUnknown)
	}
}

func (f *FleetHerdr) ensureStopped(ctx context.Context) error {
	release, err := f.acquireStartLock(ctx)
	if err != nil {
		return fmt.Errorf("coordinate Fleet Herdr start: %w", err)
	}
	defer release()

	observation := f.Observe(ctx)
	switch observation.State {
	case SessionRunningCompatible:
		return nil
	case SessionUnknown:
		return sessionEnsureError(observation, ErrSessionUnknown)
	case SessionIncompatible:
		return sessionEnsureError(observation, ErrSessionIncompatible)
	case SessionStopped:
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := f.start(ctx); err != nil {
			return fmt.Errorf("start Fleet Herdr session %q: %w", f.session, err)
		}
		return f.waitReady(ctx)
	default:
		return sessionEnsureError(observation, ErrSessionUnknown)
	}
}

func (f *FleetHerdr) waitReady(ctx context.Context) error {
	timeout := f.startTimeout
	if timeout <= 0 {
		timeout = defaultHerdrStartTimeout
	}
	interval := f.pollInterval
	if interval <= 0 {
		interval = defaultHerdrPollInterval
	}
	deadline := time.Now().Add(timeout)
	var last SessionObservation
	for {
		last = f.Observe(ctx)
		switch last.State {
		case SessionRunningCompatible:
			return nil
		case SessionUnknown:
			return sessionEnsureError(last, ErrSessionUnknown)
		case SessionIncompatible:
			return sessionEnsureError(last, ErrSessionIncompatible)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return sessionEnsureError(last, ErrEnsureTimeout)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (f *FleetHerdr) start(ctx context.Context) error {
	if f.startFn != nil {
		return f.startFn(ctx)
	}
	if f.client == nil {
		return errors.New("managed Herdr client is unavailable")
	}
	return f.client.startServer(ctx)
}

func (f *FleetHerdr) Open(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := f.Ensure(ctx); err != nil {
		return err
	}
	if f.attachFn != nil {
		return f.attachFn(ctx)
	}
	if f.client == nil {
		return errors.New("managed Herdr client is unavailable")
	}
	return f.client.attach(ctx)
}

func sessionEnsureError(observation SessionObservation, cause error) error {
	return &SessionEnsureError{Observation: observation, Cause: cause}
}

func (f *FleetHerdr) acquireStartLock(ctx context.Context) (func(), error) {
	if f.lockFn != nil {
		return f.lockFn(ctx)
	}
	root, err := secondhand.Home()
	if err != nil {
		return nil, err
	}
	lockRoot := filepath.Join(root, "herdr")
	if err := os.MkdirAll(lockRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create Herdr start lock directory: %w", err)
	}
	key := sha256.Sum256([]byte(f.session))
	lockPath := filepath.Join(lockRoot, hex.EncodeToString(key[:])+".start.lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Herdr start lock: %w", err)
	}
	for {
		err = filelock.Lock(file, false)
		if err == nil {
			return func() {
				_ = filelock.Unlock(file)
				_ = file.Close()
			}, nil
		}
		if !errors.Is(err, filelock.ErrBusy) {
			_ = file.Close()
			return nil, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func daemonEnvironment(parent []string) []string {
	result := make([]string, 0, len(parent))
	for _, item := range parent {
		key, _, ok := strings.Cut(item, "=")
		if ok && daemonEnvironmentKey(key) {
			continue
		}
		result = append(result, item)
	}
	return result
}

func daemonEnvironmentKey(key string) bool {
	key = strings.ToUpper(key)
	if strings.HasPrefix(key, "HAND_TASK_") || strings.HasPrefix(key, "HAND_ATTEMPT_") || strings.HasPrefix(key, "HAND_REPORT_") || strings.HasPrefix(key, "HAND_WORKSPACE_") || strings.HasPrefix(key, "HAND_TAB_") || strings.HasPrefix(key, "HAND_PANE_") || strings.HasPrefix(key, "HAND_BRIDGE_") || strings.HasPrefix(key, "HAND_ROUTING_") || strings.HasPrefix(key, "HAND_ROUTE_") || strings.HasPrefix(key, "HAND_RUNTIME_") || strings.HasPrefix(key, "HAND_SUPERVISOR_") || strings.HasPrefix(key, "HAND_WORKER_") || strings.HasPrefix(key, "NO_MISTAKES_") {
		return true
	}
	switch key {
	case "HAND_HOME", "HAND_ROLE", "HAND_HARNESS", "HAND_PROJECT_ID", "HAND_ROUTING_SOURCE", "HAND_PROFILE", "HAND_MODEL", "HAND_EFFORT",
		"HERDR_ENV", "HERDR_SESSION", "HERDR_SOCKET", "HERDR_SOCKET_PATH", "HERDR_CLIENT_SOCKET", "HERDR_SOCKET_OVERRIDE", "HERDR_WORKSPACE", "HERDR_TAB", "HERDR_PANE", "HERDR_WORKSPACE_ID", "HERDR_TAB_ID", "HERDR_PANE_ID",
		"CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID", "CLAUDECODE", "CODEX_THREAD_ID", "PI_CODING_AGENT", "PI_SESSION_ID", "GROK_AGENT", "GROK_SESSION_ID", "OPENCODE_SESSION_ID", "OPENAI_SESSION_ID":
		return true
	default:
		return false
	}
}
