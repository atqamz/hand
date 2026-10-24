package mockllm

import (
	"encoding/json"
	"net/http"
)

type anthropicRequest struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
	Tools  []struct {
		Name string `json:"name"`
	} `json:"tools"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

func (a anthropicRequest) turn() int {
	n := 0
	for _, m := range a.Messages {
		if m.Role == "assistant" {
			n++
		}
	}
	return n
}

func (a anthropicRequest) hasTool(name string) bool {
	for _, t := range a.Tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

func (s *Server) anthropic(w http.ResponseWriter, r *http.Request, body []byte) {
	var req anthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.record(r, body, -1, Step{})
		writeJSON(w, 400, map[string]any{"type": "error", "error": map[string]any{"type": "invalid_request_error", "message": err.Error()}})
		return
	}
	turn, step := -1, Step{Text: sideTitle}
	if len(req.Tools) > 0 {
		turn = req.turn()
		step = s.step(turn)
		if step.Shell != "" && !req.hasTool("Bash") {
			step = Step{Text: "mockllm: the request offers no Bash tool"}
		}
	}
	s.record(r, body, turn, step)
	id := "msg_mock" + string(rune('a'+(turn+1)%26))
	var blocks []map[string]any
	stop := "end_turn"
	if step.Text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": step.Text})
	}
	if step.Shell != "" {
		stop = "tool_use"
		blocks = append(blocks, map[string]any{"type": "tool_use", "id": "toolu_mock" + id, "name": "Bash", "input": map[string]any{"command": step.Shell, "description": "Run the scripted command"}})
	}
	usage := map[string]any{"input_tokens": 10, "output_tokens": 10}
	if !req.Stream {
		writeJSON(w, 200, map[string]any{"id": id, "type": "message", "role": "assistant", "model": req.Model, "content": blocks, "stop_reason": stop, "stop_sequence": nil, "usage": usage})
		return
	}
	out := newSSE(w)
	out.event("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": id, "type": "message", "role": "assistant", "model": req.Model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": usage}})
	for i, b := range blocks {
		var start, delta map[string]any
		switch b["type"] {
		case "text":
			start = map[string]any{"type": "text", "text": ""}
			delta = map[string]any{"type": "text_delta", "text": b["text"]}
		default:
			start = map[string]any{"type": "tool_use", "id": b["id"], "name": b["name"], "input": map[string]any{}}
			args, _ := json.Marshal(b["input"])
			delta = map[string]any{"type": "input_json_delta", "partial_json": string(args)}
		}
		out.event("content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": start})
		out.event("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": delta})
		out.event("content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
	}
	out.event("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 10}})
	out.event("message_stop", map[string]any{"type": "message_stop"})
}
