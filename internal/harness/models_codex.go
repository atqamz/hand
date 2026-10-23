package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

var codexTimeout = 5 * time.Second

var codexLister = lister{
	ttl:      6 * time.Hour,
	prefixes: []string{"CODEX_"},
	files:    func(env Env) []string { return []string{filepath.Join(env.CodexHome, "config.toml"), codexCache(env)} },
	list: func(ctx context.Context, x runner, env Env) ([]Model, bool, error) {
		out, _, err := x.run(ctx, codexTimeout, "", "debug", "models", "--bundled")
		if err == nil {
			var models []Model
			if models, err = decodeCodex(out, SourceBuiltin); err == nil {
				return models, true, nil
			}
		}
		if models, cerr := readCodexCache(codexCache(env)); cerr == nil {
			return models, false, nil
		}
		return nil, false, fmt.Errorf("codex debug models --bundled %w, and models_cache.json is unusable", err)
	},
}

func codexCache(env Env) string { return filepath.Join(env.CodexHome, "models_cache.json") }

func decodeCodex(b []byte, src Source) ([]Model, error) {
	var c struct {
		Models []struct {
			Slug       string `json:"slug"`
			Display    string `json:"display_name"`
			Visibility string `json:"visibility"`
			Levels     []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if json.Unmarshal(b, &c) != nil {
		return nil, errors.New("printed output it does not recognise")
	}
	var out []Model
	for _, m := range c.Models {
		model := Model{Name: m.Slug, Label: m.Display, Source: src, Hidden: m.Visibility != "" && m.Visibility != "list"}
		for _, l := range m.Levels {
			model.Efforts = append(model.Efforts, l.Effort)
		}
		out = append(out, model)
	}
	if out = tidy(out); len(out) == 0 {
		return nil, errors.New("listed no models")
	}
	return out, nil
}

func readCodexCache(path string) ([]Model, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxCache))
	if err != nil {
		return nil, err
	}
	return decodeCodex(b, SourceDiscovered)
}
