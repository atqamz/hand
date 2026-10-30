package harness

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

var opencodeTimeout = 10 * time.Second

var opencodeLister = lister{
	ttl:      6 * time.Hour,
	prefixes: []string{"OPENCODE_"},
	files: func(env Env) []string {
		dir := env.get("XDG_CONFIG_HOME")
		if dir == "" {
			dir = filepath.Join(env.get("HOME"), ".config")
		}
		return []string{filepath.Join(dir, "opencode", "opencode.json"), filepath.Join(dir, "opencode", "opencode.jsonc")}
	},
	list: func(ctx context.Context, x runner, _ Env) ([]Model, bool, error) {
		out, _, err := x.run(ctx, opencodeTimeout, "", "models", "--standalone")
		if err != nil {
			return nil, false, fmt.Errorf("opencode models --standalone %w", err)
		}
		var models []Model
		for _, line := range strings.Split(string(out), "\n") {
			if name := strings.TrimSpace(line); validModel("opencode", name) {
				models = append(models, Model{Name: name, Source: SourceDiscovered})
			}
		}
		if len(models) == 0 {
			return nil, false, errors.New("opencode models --standalone printed no model names")
		}
		return models, true, nil
	},
}
