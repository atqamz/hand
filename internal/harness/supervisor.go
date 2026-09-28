package harness

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/state"
)

var (
	uuidPattern    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	sessionPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*$`)
)

func SupervisorArgv(bin string, s Spec, session, prompt string, resume bool) ([]string, error) {
	if resume {
		if prompt != "" {
			return nil, fmt.Errorf("%w: a resumed supervisor takes no launch prompt", state.ErrInvalid)
		}
		if !sessionPattern.MatchString(session) {
			return nil, fmt.Errorf("%w: resume needs a session id of letters, digits, '_' and '-', not %q", state.ErrInvalid, session)
		}
	} else {
		if err := checkPrompt(prompt); err != nil {
			return nil, err
		}
		if s.Harness == "claude" && !uuidPattern.MatchString(session) {
			return nil, fmt.Errorf("%w: a new claude supervisor needs a UUID session id, not %q", state.ErrInvalid, session)
		}
		if s.Harness != "claude" && session != "" {
			return nil, fmt.Errorf("%w: %s picks its own session id; pass none", state.ErrInvalid, s.Harness)
		}
	}
	switch s.Harness {
	case "claude":
		argv := []string{bin, "--dangerously-skip-permissions"}
		if resume {
			argv = append(argv, "--resume", session)
		} else {
			argv = append(argv, "--session-id", session)
		}
		if s.Model != "" {
			argv = append(argv, "--model", s.Model)
		}
		if s.Effort != "" {
			argv = append(argv, "--effort", s.Effort)
		}
		if !resume {
			argv = append(argv, prompt)
		}
		return argv, nil
	case "codex":
		argv := []string{bin}
		if resume {
			argv = append(argv, "resume")
		}
		argv = append(argv, "--dangerously-bypass-approvals-and-sandbox")
		if s.Model != "" {
			argv = append(argv, "-m", s.Model)
		}
		if s.Effort != "" {
			argv = append(argv, "-c", "model_reasoning_effort="+s.Effort)
		}
		if resume {
			return append(argv, session), nil
		}
		return append(argv, prompt), nil
	case "opencode":
		if resume {
			return []string{bin, "--standalone", "--auto", "--session", session}, nil
		}
		return []string{bin, "--standalone", "--auto", "--prompt", prompt}, nil
	}
	return nil, fmt.Errorf("%w: harness %q has no launch command", state.ErrInvalid, s.Harness)
}

func NewSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

const promptScan = 1 << 20

func CodexSession(codexHome, cwd string, since time.Time, marker string) (string, error) {
	var first string
	var firstAt time.Time
	today := time.Now()
	for day := since.Local(); ; day = day.AddDate(0, 0, 1) {
		dir := filepath.Join(codexHome, "sessions", day.Format("2006"), day.Format("01"), day.Format("02"))
		files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		if err != nil {
			return "", err
		}
		for _, file := range files {
			id, at, from, err := codexMeta(file)
			if err != nil {
				return "", err
			}
			if from != cwd || at.Before(since) || (first != "" && !at.Before(firstAt)) {
				continue
			}
			ok, err := codexLaunched(file, marker)
			if err != nil {
				return "", err
			}
			if ok {
				first, firstAt = id, at
			}
		}
		if sameDay(day, today) || day.After(today) {
			return first, nil
		}
	}
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func launches(text, marker string) bool {
	return strings.HasPrefix(strings.TrimLeft(text, "\" \t\r\n"), marker)
}

func codexLaunched(file, marker string) (bool, error) {
	f, err := os.Open(file)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, promptScan))
	for {
		var rec struct {
			Type    string `json:"type"`
			Payload struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"payload"`
		}
		if err := dec.Decode(&rec); err != nil {
			return false, nil
		}
		if rec.Type != "response_item" || rec.Payload.Type != "message" || rec.Payload.Role != "user" {
			continue
		}
		for _, c := range rec.Payload.Content {
			if launches(c.Text, marker) {
				return true, nil
			}
		}
	}
}

func codexMeta(file string) (string, time.Time, string, error) {
	f, err := os.Open(file)
	if errors.Is(err, fs.ErrNotExist) {
		return "", time.Time{}, "", nil
	}
	if err != nil {
		return "", time.Time{}, "", err
	}
	defer f.Close()
	var line struct {
		Type    string `json:"type"`
		Payload struct {
			ID        string `json:"id"`
			Cwd       string `json:"cwd"`
			Timestamp string `json:"timestamp"`
		} `json:"payload"`
	}
	if err := json.NewDecoder(f).Decode(&line); err != nil || line.Type != "session_meta" {
		return "", time.Time{}, "", nil
	}
	at, err := time.Parse(time.RFC3339Nano, line.Payload.Timestamp)
	if err != nil {
		return "", time.Time{}, "", nil
	}
	return line.Payload.ID, at, line.Payload.Cwd, nil
}

type opencodeSession struct {
	ID        string `json:"id"`
	Created   int64  `json:"created"`
	Directory string `json:"directory"`
}

type opencodeMessage struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Info struct {
		Role string `json:"role"`
	} `json:"info"`
	Parts []struct {
		Text string `json:"text"`
	} `json:"parts"`
}

func (m opencodeMessage) launches(marker string) bool {
	if m.Type != "user" && m.Info.Role != "user" {
		return false
	}
	if launches(m.Text, marker) {
		return true
	}
	for _, p := range m.Parts {
		if launches(p.Text, marker) {
			return true
		}
	}
	return false
}

func OpencodeSession(bin, dir string, since time.Time, marker string) (string, error) {
	out, err := opencode(bin, dir, "session", "list", "--standalone", "--format", "json")
	if err != nil {
		return "", err
	}
	var sessions []opencodeSession
	if err := json.Unmarshal(out, &sessions); err != nil {
		return "", fmt.Errorf("opencode session list: %w", err)
	}
	sessions = slices.DeleteFunc(sessions, func(s opencodeSession) bool {
		return filepath.Clean(s.Directory) != filepath.Clean(dir) || s.Created < since.UnixMilli()
	})
	slices.SortFunc(sessions, func(a, b opencodeSession) int {
		return int(a.Created - b.Created)
	})
	for _, s := range sessions {
		out, err := opencode(bin, dir, "session", "export", "--standalone", s.ID)
		if err != nil {
			return "", err
		}
		var export struct {
			Messages []opencodeMessage `json:"messages"`
		}
		if err := json.Unmarshal(out, &export); err != nil {
			return "", fmt.Errorf("opencode session export: %w", err)
		}
		if slices.ContainsFunc(export.Messages, func(m opencodeMessage) bool { return m.launches(marker) }) {
			return s.ID, nil
		}
	}
	return "", nil
}

func opencode(bin, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return nil, fmt.Errorf("opencode %s %s: %v: %s", args[0], args[1], err, exit.Stderr)
		}
		return nil, fmt.Errorf("opencode %s %s: %w", args[0], args[1], err)
	}
	return out, nil
}
