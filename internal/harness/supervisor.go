package harness

import (
	"bytes"
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
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/agydb"
	"github.com/atqamz/hand/internal/proc"
	"github.com/atqamz/hand/internal/state"
)

var (
	uuidPattern    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	sessionPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*$`)
	opencodeID     = regexp.MustCompile(`^ses_hand[0-9a-f]{26}$`)
)

func SupervisorArgv(bin string, s Spec, session, prompt, dir string, resume bool) ([]string, error) {
	argv, err := supervisorArgv(bin, s, session, prompt, dir, resume)
	if err != nil {
		return nil, err
	}
	if err := checkCommandLine(runtime.GOOS, argv); err != nil {
		return nil, err
	}
	return argv, nil
}

func supervisorArgv(bin string, s Spec, session, prompt, dir string, resume bool) ([]string, error) {
	if err := CheckSupervisor(s); err != nil {
		return nil, err
	}
	if resume {
		if prompt != "" {
			return nil, fmt.Errorf("%w: a resumed supervisor takes no launch prompt", state.ErrInvalid)
		}
		if !sessionPattern.MatchString(session) {
			return nil, fmt.Errorf("%w: resume needs a session id of letters, digits, '_' and '-', not %q", state.ErrInvalid, session)
		}
	} else {
		if err := CheckPrompt(prompt); err != nil {
			return nil, err
		}
		if s.Harness == "claude" && !uuidPattern.MatchString(session) {
			return nil, fmt.Errorf("%w: a new claude supervisor needs a UUID session id, not %q", state.ErrInvalid, session)
		}
		if s.Harness == "opencode" && !opencodeID.MatchString(session) {
			return nil, fmt.Errorf("%w: a new opencode supervisor needs a ses_hand session id, not %q", state.ErrInvalid, session)
		}
		if s.Harness != "claude" && s.Harness != "opencode" && session != "" {
			return nil, fmt.Errorf("%w: %s picks its own session id; pass none", state.ErrInvalid, s.Harness)
		}
	}
	if err := checkDir(s, dir); err != nil {
		return nil, err
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
		argv = append(argv, "--dangerously-bypass-approvals-and-sandbox", "--disable", "hooks", "--cd", dir)
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
		return []string{bin, "--standalone", "--auto", "--session", session, "--prompt", prompt}, nil
	case "agy":
		argv := []string{bin, "--model", s.Model, "--dangerously-skip-permissions"}
		if resume {
			return append(argv, "--conversation", session), nil
		}
		return append(argv, "-i", prompt), nil
	}
	return nil, fmt.Errorf("%w: harness %q has no launch command", state.ErrInvalid, s.Harness)
}

func AgySession(dir, cwd string, since time.Time, marker string) (string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	want := realPath(cwd)
	var id string
	var newest time.Time
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".db")
		if !ok || e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil || fi.ModTime().Before(since) || !fi.ModTime().After(newest) {
			continue
		}
		if agyLaunched(filepath.Join(dir, e.Name()), want, marker) {
			id, newest = name, fi.ModTime()
		}
	}
	return id, nil
}

func agyLaunched(path, cwd, marker string) bool {
	db, err := agydb.Open(path)
	if err != nil {
		return false
	}
	defer db.Close()
	if ws, err := agydb.Workspace(db); err != nil || (ws != "" && realPath(ws) != cwd) {
		return false
	}
	steps, err := agydb.Steps(db)
	if err != nil {
		return false
	}
	for _, s := range steps {
		if s.Type == 14 {
			return launches(s.Text, marker)
		}
	}
	return false
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func AgyConversations(getenv func(string) string) string {
	return filepath.Join(getenv("HOME"), ".gemini", "antigravity-cli", "conversations")
}

func NewSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func NewOpencodeSessionID() string {
	var b [13]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("ses_hand%x", b)
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

func Opencode(ctx context.Context, bin, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	proc.NoWindow(cmd)
	out, err := cmd.Output()
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return out, fmt.Errorf("opencode %s %s: %v: %s", args[0], args[1], err, bytes.TrimSpace(exit.Stderr))
		}
		return out, fmt.Errorf("opencode %s %s: %w", args[0], args[1], err)
	}
	return out, nil
}

type opencodeModel struct {
	Provider string `json:"providerID"`
	ID       string `json:"id"`
	Variant  string `json:"variant,omitempty"`
}

func OpencodeCreate(ctx context.Context, bin, dir, id string, s Spec) error {
	provider, model, _ := strings.Cut(s.Model, "/")
	var body struct {
		ID       string        `json:"id"`
		Model    opencodeModel `json:"model"`
		Location struct {
			Directory string `json:"directory"`
		} `json:"location"`
	}
	body.ID, body.Model, body.Location.Directory = id, opencodeModel{provider, model, s.Effort}, dir
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	out, err := Opencode(ctx, bin, dir, "api", "--standalone", "session.create", "-d", string(b))
	if err != nil {
		var reply struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(out, &reply) == nil && reply.Message != "" {
			err = fmt.Errorf("%w: %s", err, reply.Message)
		}
		return fmt.Errorf("creating opencode session %s on %s: %s", id, s.Model, strings.Join(strings.Fields(err.Error()), " "))
	}
	var reply struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &reply); err != nil {
		return fmt.Errorf("creating opencode session %s on %s: opencode's answer is not a session: %v", id, s.Model, err)
	}
	if reply.Data.ID != id {
		return fmt.Errorf("creating opencode session %s on %s: opencode answered with session %q", id, s.Model, reply.Data.ID)
	}
	return nil
}

func OpencodeCreateFor(ctx context.Context, argv []string, dir string, s Spec) error {
	if s.Harness != "opencode" || s.Model == "" {
		return nil
	}
	i := slices.Index(argv, "--session")
	if i < 0 || i+1 >= len(argv) {
		return fmt.Errorf("%w: the opencode command has no --session id", state.ErrInvalid)
	}
	return OpencodeCreate(ctx, argv[0], dir, argv[i+1], s)
}
