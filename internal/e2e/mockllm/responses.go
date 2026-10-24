package mockllm

import (
	"encoding/json"
	"net/http"
)

type responsesRequest struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
	Tools  []struct {
		Name string `json:"name"`
	} `json:"tools"`
	Input []struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"input"`
}

func (a responsesRequest) turn() int {
	n := 0
	for _, it := range a.Input {
		if it.Type == "function_call" || (it.Type == "message" && it.Role == "assistant") {
			n++
		}
	}
	return n
}

func (a responsesRequest) shellTool() (name, arg string) {
	for _, t := range a.Tools {
		switch t.Name {
		case "exec_command":
			return t.Name, "cmd"
		case "shell_command":
			return t.Name, "command"
		}
	}
	return "", ""
}

func (s *Server) responses(w http.ResponseWriter, r *http.Request, body []byte) {
	var req responsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.record(r, body, -1, Step{})
		writeJSON(w, 400, map[string]any{"error": map[string]any{"type": "invalid_request_error", "message": err.Error()}})
		return
	}
	turn, step := -1, Step{Text: sideTitle}
	tool, arg := req.shellTool()
	if len(req.Tools) > 0 {
		turn = req.turn()
		step = s.step(turn)
		if step.Shell != "" && tool == "" {
			step = Step{Text: "mockllm: the request offers no shell tool"}
		}
	}
	s.record(r, body, turn, step)
	id := "resp_mock" + string(rune('a'+(turn+1)%26))
	var items []map[string]any
	if step.Text != "" {
		items = append(items, map[string]any{"type": "message", "id": "msg_" + id, "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": step.Text, "annotations": []any{}}}})
	}
	if step.Shell != "" {
		args, _ := json.Marshal(map[string]any{arg: step.Shell, "login": false})
		items = append(items, map[string]any{"type": "function_call", "id": "fc_" + id, "call_id": "call_" + id, "name": tool, "arguments": string(args), "status": "completed"})
	}
	usage := map[string]any{"input_tokens": 10, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens": 10, "output_tokens_details": map[string]any{"reasoning_tokens": 0}, "total_tokens": 20}
	done := map[string]any{"id": id, "object": "response", "status": "completed", "model": req.Model, "output": items, "usage": usage}
	if !req.Stream {
		writeJSON(w, 200, done)
		return
	}
	out := newSSE(w)
	out.event("response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": id, "object": "response", "status": "in_progress", "model": req.Model, "output": []any{}}})
	for i, it := range items {
		pending := map[string]any{}
		for k, v := range it {
			pending[k] = v
		}
		pending["status"] = "in_progress"
		if it["type"] == "function_call" {
			pending["arguments"] = ""
		} else {
			pending["content"] = []any{}
		}
		out.event("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": i, "item": pending})
		if it["type"] == "message" {
			out.event("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": it["id"], "output_index": i, "content_index": 0, "delta": step.Text})
		}
		out.event("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": i, "item": it})
	}
	out.event("response.completed", map[string]any{"type": "response.completed", "response": done})
}
