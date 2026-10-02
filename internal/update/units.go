package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/proc"
)

var ErrNoSystemctl = errors.New("systemctl was not found")

type Unit struct {
	Name, Command, Home string
	Active, Transient   bool
}

func Units(ctx context.Context, env []string, target string) ([]Unit, error) {
	if _, err := systemctl(env); err != nil {
		return nil, ErrNoSystemctl
	}
	out, err := systemctlOutput(ctx, env, "list-units", "--all", "--plain", "--no-legend", "secondhand-*")
	if err != nil {
		return nil, err
	}
	var names []string
	for line := range strings.Lines(out) {
		if fields := strings.Fields(line); len(fields) > 0 {
			names = append(names, fields[0])
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	out, err = systemctlOutput(ctx, env, append(append([]string{"show"}, names...), "-p", "Id,ActiveState,ExecStart,Environment,Transient")...)
	if err != nil {
		return nil, err
	}
	var units []Unit
	for block := range strings.SplitSeq(out, "\n\n") {
		props := map[string]string{}
		for line := range strings.Lines(block) {
			if k, v, ok := strings.Cut(strings.TrimRight(line, "\n"), "="); ok {
				props[k] = v
			}
		}
		path, argv := execStartOf(props["ExecStart"])
		resolved := path
		if p, err := filepath.EvalSymlinks(path); err == nil {
			resolved = p
		}
		if resolved != target || props["Id"] == "" {
			continue
		}
		command := ""
		if rest, ok := strings.CutPrefix(argv, path+" "); ok {
			command, _, _ = strings.Cut(rest, " ")
		} else if fields := strings.Fields(argv); len(fields) > 1 {
			command = fields[1]
		}
		units = append(units, Unit{Name: props["Id"], Command: command, Home: envValue(props["Environment"], "HAND_HOME"), Active: props["ActiveState"] == "active" || props["ActiveState"] == "activating" || props["ActiveState"] == "reloading", Transient: props["Transient"] == "yes"})
	}
	return units, nil
}

func Systemctl(ctx context.Context, env []string, args ...string) error {
	_, err := systemctlOutput(ctx, env, args...)
	return err
}

func systemctl(env []string) (string, error) {
	path := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	return harness.LookPath("systemctl", path)
}

func systemctlOutput(ctx context.Context, env []string, args ...string) (string, error) {
	bin, err := systemctl(env)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{"--user"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Env, cmd.Stdout, cmd.Stderr, cmd.WaitDelay = env, &stdout, &stderr, time.Second
	proc.NewGroup(cmd)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("systemctl --user %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()+stdout.String()))
	}
	return stdout.String(), nil
}

func execStartOf(v string) (path, argv string) {
	v = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(v), "{ "), " }")
	for part := range strings.SplitSeq(v, " ; ") {
		if p, ok := strings.CutPrefix(part, "path="); ok {
			path = p
		} else if a, ok := strings.CutPrefix(part, "argv[]="); ok {
			argv = a
		}
	}
	return path, argv
}

func envValue(env, name string) string {
	var assignments []string
	var cur strings.Builder
	quoted := false
	for _, r := range env {
		switch {
		case r == '"':
			quoted = !quoted
		case r == ' ' && !quoted:
			assignments = append(assignments, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	assignments = append(assignments, cur.String())
	for _, a := range assignments {
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v
		}
	}
	return ""
}
