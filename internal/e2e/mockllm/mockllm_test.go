package mockllm_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/e2e/mockllm"
)

const key = "sk-ant-never-logged-0123456789"

func post(t *testing.T, s *mockllm.Server, path, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Api-Key", key)
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, b
}

func start(t *testing.T, steps ...mockllm.Step) *mockllm.Server {
	t.Helper()
	s, err := mockllm.Start(steps...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func events(t *testing.T, body []byte) map[string][]map[string]any {
	t.Helper()
	out := map[string][]map[string]any{}
	sc := bufio.NewScanner(bytes.NewReader(body))
	name := ""
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			name = v
		}
		if v, ok := strings.CutPrefix(line, "data: "); ok {
			var data map[string]any
			if err := json.Unmarshal([]byte(v), &data); err != nil {
				t.Fatalf("event %s: %v", name, err)
			}
			if data["type"] != name {
				t.Fatalf("event %s carries type %v", name, data["type"])
			}
			out[name] = append(out[name], data)
		}
	}
	return out
}

const claudeFirst = `{"model":"claude-sonnet-5-5","stream":true,"tools":[{"name":"Bash"},{"name":"Read"}],"messages":[{"role":"user","content":[{"type":"text","text":"<system-reminder>ctx</system-reminder>"},{"type":"text","text":"run it"}]},{"role":"system","content":[{"type":"text","text":"Primary working directory: /w"}]}]}`

const claudeSecond = `{"model":"claude-sonnet-5-5","stream":true,"tools":[{"name":"Bash"}],"messages":[{"role":"user","content":[{"type":"text","text":"run it"}]},{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"x"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"report: r1"}]}]}`

func TestAnthropicRunsTheShellThenAnswers(t *testing.T) {
	s := start(t, mockllm.Step{Shell: "echo 'hi' > out.txt"}, mockllm.Step{Text: "Done."})
	res, body := post(t, s, "/v1/messages?beta=true", claudeFirst)
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status %d, type %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	ev := events(t, body)
	for _, name := range []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"} {
		if len(ev[name]) == 0 {
			t.Fatalf("no %s event in\n%s", name, body)
		}
	}
	start := ev["content_block_start"][0]["content_block"].(map[string]any)
	if start["type"] != "tool_use" || start["name"] != "Bash" {
		t.Fatalf("block = %v", start)
	}
	var input map[string]string
	partial := ev["content_block_delta"][0]["delta"].(map[string]any)["partial_json"].(string)
	if err := json.Unmarshal([]byte(partial), &input); err != nil || input["command"] != "echo 'hi' > out.txt" {
		t.Fatalf("input = %q, %v", partial, err)
	}
	if stop := ev["message_delta"][0]["delta"].(map[string]any)["stop_reason"]; stop != "tool_use" {
		t.Fatalf("stop_reason = %v", stop)
	}

	_, body = post(t, s, "/v1/messages", claudeSecond)
	ev = events(t, body)
	if text := ev["content_block_delta"][0]["delta"].(map[string]any)["text"]; text != "Done." {
		t.Fatalf("second reply = %v", text)
	}
	if stop := ev["message_delta"][0]["delta"].(map[string]any)["stop_reason"]; stop != "end_turn" {
		t.Fatalf("stop_reason = %v", stop)
	}

	reqs := s.Requests()
	if len(reqs) != 2 || reqs[0].Turn != 0 || reqs[1].Turn != 1 {
		t.Fatalf("requests = %+v", reqs)
	}
	if got := reqs[0].UserTexts(); len(got) != 2 || got[1] != "run it" {
		t.Fatalf("user texts = %q", got)
	}
	if got := reqs[1].ToolOutputs(); len(got) != 1 || got[0] != "report: r1" {
		t.Fatalf("tool outputs = %q", got)
	}
	if !strings.Contains(strings.Join(reqs[0].Strings(), "\n"), "Primary working directory: /w") {
		t.Fatalf("strings = %q", reqs[0].Strings())
	}
}

func TestAnthropicRequestWithoutToolsGetsATitle(t *testing.T) {
	s := start(t, mockllm.Step{Shell: "never"})
	_, body := post(t, s, "/v1/messages?beta=true", `{"model":"m","stream":false,"tools":[],"messages":[{"role":"user","content":[{"type":"text","text":"<session>x</session>"}]}]}`)
	var msg struct {
		Content    []struct{ Text string }
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(body, &msg); err != nil || len(msg.Content) != 1 || msg.Content[0].Text != `{"title":"Mock session"}` || msg.StopReason != "end_turn" {
		t.Fatalf("reply = %s, %v", body, err)
	}
	if reqs := s.Requests(); len(reqs) != 1 || reqs[0].Turn != -1 {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestAnthropicWithoutABashToolSaysSo(t *testing.T) {
	s := start(t, mockllm.Step{Shell: "echo"})
	_, body := post(t, s, "/v1/messages", `{"model":"m","stream":false,"tools":[{"name":"Read"}],"messages":[{"role":"user","content":"x"}]}`)
	if !strings.Contains(string(body), "no Bash tool") || strings.Contains(string(body), "tool_use") {
		t.Fatalf("reply = %s", body)
	}
}

const codexFirst = `{"model":"mock-model","stream":true,"tools":[{"type":"function","name":"exec_command"},{"type":"web_search"}],"input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"rules"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/w</cwd>\n</environment_context>"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"run it"}]}]}`

const codexSecond = `{"model":"mock-model","stream":true,"tools":[{"type":"function","name":"exec_command"}],"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"run it"}]},{"type":"function_call","call_id":"call_1","name":"exec_command","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"report: r1"}]}`

func TestResponsesRunsTheShellThenAnswers(t *testing.T) {
	s := start(t, mockllm.Step{Shell: "echo 'hi' > out.txt"}, mockllm.Step{Text: "Done."})
	res, body := post(t, s, "/v1/responses", codexFirst)
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status %d, type %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	ev := events(t, body)
	call := ev["response.output_item.done"][0]["item"].(map[string]any)
	var args map[string]any
	if call["type"] != "function_call" || call["name"] != "exec_command" || json.Unmarshal([]byte(call["arguments"].(string)), &args) != nil || args["cmd"] != "echo 'hi' > out.txt" || args["login"] != false {
		t.Fatalf("call = %v", call)
	}
	done := ev["response.completed"][0]["response"].(map[string]any)
	if done["status"] != "completed" || done["usage"] == nil {
		t.Fatalf("completed = %v", done)
	}

	_, body = post(t, s, "/v1/responses", codexSecond)
	ev = events(t, body)
	msg := ev["response.output_item.done"][0]["item"].(map[string]any)
	if msg["type"] != "message" || msg["content"].([]any)[0].(map[string]any)["text"] != "Done." {
		t.Fatalf("message = %v", msg)
	}

	reqs := s.Requests()
	if len(reqs) != 2 || reqs[0].Turn != 0 || reqs[1].Turn != 1 {
		t.Fatalf("requests = %+v", reqs)
	}
	if got := reqs[0].UserTexts(); len(got) != 2 || got[1] != "run it" {
		t.Fatalf("user texts = %q", got)
	}
	if got := reqs[1].ToolOutputs(); len(got) != 1 || got[0] != "report: r1" {
		t.Fatalf("tool outputs = %q", got)
	}
}

func TestResponsesUsesTheShellCommandToolWhenThatIsAllItOffers(t *testing.T) {
	s := start(t, mockllm.Step{Shell: "echo"})
	_, body := post(t, s, "/v1/responses", `{"model":"m","stream":false,"tools":[{"type":"function","name":"shell_command"}],"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"x"}]}]}`)
	var res struct {
		Output []struct{ Name, Arguments string }
	}
	if err := json.Unmarshal(body, &res); err != nil || len(res.Output) != 1 || res.Output[0].Name != "shell_command" || !strings.Contains(res.Output[0].Arguments, `"command":"echo"`) {
		t.Fatalf("reply = %s, %v", body, err)
	}
}

func TestOtherRoutesAreLoggedAndRefused(t *testing.T) {
	s := start(t)
	res, _ := post(t, s, "/v1/unknown", `{}`)
	if res.StatusCode != 404 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	res, body := post(t, s, "/v1/messages/count_tokens", `{}`)
	if res.StatusCode != 200 || !strings.Contains(string(body), "input_tokens") {
		t.Fatalf("count_tokens = %d %s", res.StatusCode, body)
	}
	if reqs := s.Requests(); len(reqs) != 2 || reqs[0].Path != "/v1/unknown" || reqs[1].Turn != -1 {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestTheLogNeverHoldsACredential(t *testing.T) {
	s := start(t)
	post(t, s, "/v1/messages", claudeFirst)
	if strings.Contains(string(s.Dump()), key) || !strings.Contains(string(s.Dump()), "<redacted>") {
		t.Fatalf("dump:\n%s", s.Dump())
	}
}

func TestAScriptThatRunsOutAnswersPolitely(t *testing.T) {
	s := start(t, mockllm.Step{Text: "once"})
	_, body := post(t, s, "/v1/messages", claudeSecond)
	if !strings.Contains(string(body), "Nothing more to do.") {
		t.Fatalf("reply = %s", body)
	}
}
