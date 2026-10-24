package mockllm

import (
	"encoding/json"
	"strings"
)

type block struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Output  json.RawMessage `json:"output"`
	Content json.RawMessage `json:"content"`
}

type turnMessage struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Output  json.RawMessage `json:"output"`
}

func messages(body []byte) []turnMessage {
	var req struct {
		Messages []turnMessage   `json:"messages"`
		Input    json.RawMessage `json:"input"`
	}
	if json.Unmarshal(body, &req) != nil {
		return nil
	}
	if len(req.Messages) > 0 {
		return req.Messages
	}
	var input []turnMessage
	_ = json.Unmarshal(req.Input, &input)
	return input
}

func blocks(raw json.RawMessage) []block {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []block{{Type: "text", Text: s}}
	}
	var bs []block
	_ = json.Unmarshal(raw, &bs)
	return bs
}

func flatten(raw json.RawMessage) string {
	var parts []string
	for _, b := range blocks(raw) {
		parts = append(parts, b.Text)
	}
	return strings.Join(parts, "\n")
}

func (r Request) UserTexts() []string {
	var out []string
	for _, m := range messages(r.Body) {
		if m.Role != "user" {
			continue
		}
		for _, b := range blocks(m.Content) {
			if (b.Type == "text" || b.Type == "input_text") && b.Text != "" {
				out = append(out, b.Text)
			}
		}
	}
	return out
}

func (r Request) ToolOutputs() []string {
	var out []string
	for _, m := range messages(r.Body) {
		if m.Type == "function_call_output" {
			out = append(out, flatten(m.Output))
			continue
		}
		for _, b := range blocks(m.Content) {
			if b.Type == "tool_result" {
				out = append(out, flatten(b.Content))
			}
		}
	}
	return out
}

func (r Request) Strings() []string {
	var v any
	if json.Unmarshal(r.Body, &v) != nil {
		return nil
	}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			out = append(out, v)
		case []any:
			for _, e := range v {
				walk(e)
			}
		case map[string]any:
			for _, e := range v {
				walk(e)
			}
		}
	}
	walk(v)
	return out
}
