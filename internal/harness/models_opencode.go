package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
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
		variants := opencodeVariants(ctx, x)
		var models []Model
		for _, line := range strings.Split(string(out), "\n") {
			if name := strings.TrimSpace(line); validModel("opencode", name) {
				models = append(models, Model{Name: name, Efforts: variants[name], Source: SourceDiscovered})
			}
		}
		if len(models) == 0 {
			return nil, false, errors.New("opencode models --standalone printed no model names")
		}
		return models, true, nil
	},
}

func opencodeVariants(ctx context.Context, x runner) map[string][]string {
	out, _, err := x.run(ctx, opencodeTimeout, "", "api", "--standalone", "GET", "/api/model")
	if err != nil {
		return nil
	}
	var r struct {
		Data []struct {
			ProviderID string `json:"providerID"`
			ModelID    string `json:"modelID"`
			Variants   []struct {
				ID string `json:"id"`
			} `json:"variants"`
		} `json:"data"`
	}
	if json.Unmarshal(out, &r) != nil {
		return nil
	}
	variants := map[string][]string{}
	for _, m := range r.Data {
		name := m.ProviderID + "/" + m.ModelID
		for _, v := range m.Variants {
			if effortToken.MatchString(v.ID) && !slices.Contains(variants[name], v.ID) {
				variants[name] = append(variants[name], v.ID)
			}
		}
	}
	return variants
}
