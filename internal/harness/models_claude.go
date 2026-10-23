package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"time"
)

const initializeID = "hand-models"

var (
	initializeRequest = `{"type":"control_request","request_id":"` + initializeID + `","request":{"subtype":"initialize"}}` + "\n"
	claudeBuiltin     = regexp.MustCompile(`^((default|opus|sonnet|haiku|fable)|([a-z]+\.)*claude-[a-z0-9.@:-]+)(\[1m\])?$`)
)

var claudeTimeout = 15 * time.Second

var claudeLister = lister{
	ttl:      time.Hour,
	prefixes: []string{"ANTHROPIC_", "CLAUDE_", "CLOUD_ML_", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_PROFILE"},
	files: func(env Env) []string {
		dir := env.get("CLAUDE_CONFIG_DIR")
		if dir == "" {
			dir = filepath.Join(env.get("HOME"), ".claude")
		}
		return []string{filepath.Join(dir, "settings.json"), filepath.Join(dir, "settings.local.json")}
	},
	list: func(ctx context.Context, x runner, _ Env) ([]Model, bool, error) {
		out, _, err := x.run(ctx, claudeTimeout, initializeRequest, "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--no-session-persistence")
		models, derr := decodeClaude(out)
		switch {
		case derr == nil:
			return models, true, nil
		case err != nil:
			return nil, false, fmt.Errorf("claude -p %w", err)
		}
		return nil, false, derr
	},
}

func decodeClaude(out []byte) ([]Model, error) {
	for _, line := range bytes.Split(out, []byte("\n")) {
		if !bytes.Contains(line, []byte(`"control_response"`)) {
			continue
		}
		var r struct {
			Type     string `json:"type"`
			Response struct {
				Subtype   string `json:"subtype"`
				RequestID string `json:"request_id"`
				Response  struct {
					Models []struct {
						Value    string   `json:"value"`
						Display  string   `json:"displayName"`
						Supports bool     `json:"supportsEffort"`
						Levels   []string `json:"supportedEffortLevels"`
					} `json:"models"`
				} `json:"response"`
			} `json:"response"`
		}
		if json.Unmarshal(line, &r) != nil || r.Type != "control_response" || r.Response.RequestID != initializeID {
			continue
		}
		if r.Response.Subtype != "success" {
			return nil, errors.New("claude refused the initialize request")
		}
		var models []Model
		for _, m := range r.Response.Response.Models {
			model := Model{Name: m.Value, Label: m.Display, Source: SourceBuiltin}
			if m.Supports {
				model.Efforts = m.Levels
			}
			if !claudeBuiltin.MatchString(m.Value) {
				model.Source = SourceCustom
				model.Unverified = len(model.Efforts) == len(claudeEfforts) && !slices.ContainsFunc(claudeEfforts, func(e string) bool { return !slices.Contains(model.Efforts, e) })
			}
			models = append(models, model)
		}
		return models, nil
	}
	return nil, errors.New("claude gave no initialize response; it may be too old")
}
