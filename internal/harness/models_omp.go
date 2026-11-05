package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

var ompTimeout = 15 * time.Second

var ompLister = lister{
	ttl:      6 * time.Hour,
	prefixes: []string{"PI_"},
	files: func(env Env) []string {
		return []string{filepath.Join(OmpAgent(env.get), "config.yml")}
	},
	list: func(ctx context.Context, x runner, _ Env) ([]Model, bool, error) {
		out, _, err := x.run(ctx, ompTimeout, "", "models", "--json")
		if err != nil {
			return nil, false, fmt.Errorf("omp models --json %w", err)
		}
		var reply struct {
			Models []struct {
				Selector string   `json:"selector"`
				Name     string   `json:"name"`
				Thinking []string `json:"thinking"`
			} `json:"models"`
		}
		if json.Unmarshal(out, &reply) != nil {
			return nil, false, errors.New("omp models --json printed no model list")
		}
		models := make([]Model, 0, len(reply.Models))
		for _, m := range reply.Models {
			if m.Selector != "" {
				models = append(models, Model{Name: m.Selector, Label: m.Name, Efforts: m.Thinking, Source: SourceDiscovered})
			}
		}
		if len(models) == 0 {
			return nil, false, errors.New("omp models --json listed no models")
		}
		return models, true, nil
	},
}

func OmpAgent(getenv func(string) string) string {
	if dir := getenv("PI_CODING_AGENT_DIR"); dir != "" {
		return dir
	}
	if dir := getenv("PI_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "agent")
	}
	return filepath.Join(getenv("HOME"), ".omp", "agent")
}
