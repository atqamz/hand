package luvus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

func Address(ctx context.Context, bin, session string, environ []string) (addr, dir string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := command(ctx, Scrub(environ), bin, "session", "list", "--json").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			err = fmt.Errorf("%w: %s", err, bytes.TrimSpace(exit.Stderr))
		}
		return "", "", fmt.Errorf("luvus session list: %w", err)
	}
	var list struct {
		Sessions []struct {
			Name     string `json:"name"`
			Dir      string `json:"session_dir"`
			Endpoint struct {
				Address string `json:"address"`
			} `json:"endpoint"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return "", "", fmt.Errorf("luvus session list: bad output: %w", err)
	}
	for _, s := range list.Sessions {
		if s.Name == session && s.Endpoint.Address != "" {
			return s.Endpoint.Address, s.Dir, nil
		}
	}
	return "", "", fmt.Errorf("luvus session list: no address for session %s", session)
}
