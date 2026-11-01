package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"llm-gateway/internal/config"
)

func TestAnthropicToolRoundTrip(t *testing.T) {
	raw := []byte(`{"model":"alias","messages":[{"role":"system","content":"Be helpful"},{"role":"user","content":"Weather?"},{"role":"assistant","content":null,"tool_calls":[{"id":"tool_1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Bangkok\"}"}}]},{"role":"tool","tool_call_id":"tool_1","content":"Sunny"}],"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object"}}}],"max_tokens":100}`)
	r, e := Decode(raw)
	if e != nil {
		t.Fatal(e)
	}
	body, e := Build(r, config.Model{Model: "upstream"}, config.Provider{Type: "anthropic"})
	if e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{`"tool_result"`, `"tool_use"`, `"system":"Be helpful"`, `"model":"upstream"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
	response := []byte(`{"id":"msg_1","content":[{"type":"tool_use","id":"tool_2","name":"weather","input":{"city":"Bangkok"}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}`)
	b, e := ConvertResponse(response, "alias")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), `"finish_reason":"tool_calls"`) {
		t.Fatal(string(b))
	}
}
func TestRejectUnsupportedAndTrailingJSON(t *testing.T) {
	for _, raw := range []string{`{"model":"a","messages":[{"role":"user","content":"Hi"}],"n":2}`, `{"model":"a","messages":[{"role":"user","content":"Hi"}]} {}`, `{"model":"a","messages":[{"role":"user","content":[{"type":"image_url"}]}]}`} {
		if _, e := Decode([]byte(raw)); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestStreamConvertsToolIndexesAndUsage(t *testing.T) {
	s := AnthropicStream{Model: "alias", IncludeUsage: true}
	frames := []string{
		`{"type":"message_start","message":{"id":"msg","usage":{"input_tokens":12,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"lookup"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"id\":1}"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}`,
		`{"type":"message_stop"}`,
	}
	out := ""
	for _, v := range frames {
		b, e := s.Convert([]byte("data: " + v + "\n\n"))
		if e != nil {
			t.Fatal(e)
		}
		out += string(b)
	}
	for _, want := range []string{`"index":0`, `"arguments":"{\"id\":1}"`, `"finish_reason":"tool_calls"`, `"total_tokens":16`, `[DONE]`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in %s", want, out)
		}
	}
	if !s.Done || !s.Known {
		t.Fatal("terminal state missing")
	}
}
func TestSSEMultilineAndLimit(t *testing.T) {
	var frames [][]byte
	e := ReadSSE(strings.NewReader(": ping\r\n\r\ndata: first\ndata: second\n\n"), func(b []byte) error { frames = append(frames, b); return nil })
	if e != nil || len(frames) != 2 {
		t.Fatal(e, len(frames))
	}
	if string(SSEData(frames[1])) != "first\nsecond" {
		t.Fatal(string(frames[1]))
	}
	if e := ReadSSE(strings.NewReader(strings.Repeat("x", 5<<20)), func([]byte) error { return nil }); e == nil {
		t.Fatal("unbounded event accepted")
	}
}
func TestUsageNullNotKnown(t *testing.T) {
	if ParseUsage([]byte(`{"usage":null}`), "openai").Known {
		t.Fatal("null usage treated as known")
	}
	var r Request
	_ = json.Unmarshal([]byte(`{}`), &r)
}

func TestToolWithNoArgumentsEmitsJSONObject(t *testing.T) {
	s := AnthropicStream{Model: "alias"}
	for _, v := range []string{`{"type":"message_start","message":{"id":"msg","usage":{"input_tokens":1}}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t","name":"now","input":{}}}`} {
		if _, e := s.Convert([]byte("data: " + v + "\n\n")); e != nil {
			t.Fatal(e)
		}
	}
	b, e := s.Convert([]byte("data: {\"type\":\"content_block_stop\",\"index\":0}\n\n"))
	if e != nil || !strings.Contains(string(b), `"arguments":"{}"`) {
		t.Fatalf("empty tool arguments %s %v", b, e)
	}
}
func TestCacheUsageAndNegativeTokens(t *testing.T) {
	u := ParseUsage([]byte(`{"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":20,"cache_creation_input_tokens":30}}`), "anthropic")
	if !u.Known || u.Input != 60 {
		t.Fatal(u)
	}
	if ParseUsage([]byte(`{"usage":{"prompt_tokens":-10,"completion_tokens":5}}`), "openai").Known {
		t.Fatal("negative usage accepted")
	}
}

func TestRejectToolMessagesWithInvalidContent(t *testing.T) {
	for _, raw := range []string{`{"model":"a","messages":[{"role":"tool","content":null,"tool_call_id":"t","tool_calls":[{"id":"t","type":"function","function":{"name":"f","arguments":"{}"}}]}]}`, `{"model":"a","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"t","type":"function","function":{"name":"f","arguments":"null"}}]}]}`} {
		if _, e := Decode([]byte(raw)); e == nil {
			t.Fatalf("accepted invalid tool input %s", raw)
		}
	}
}

func TestFirstOutputIgnoresRoleAndUsageChunks(t *testing.T) {
	for _, b := range []string{`{"choices":[{"delta":{"role":"assistant","content":""}}]}`, `{"choices":[],"usage":{"prompt_tokens":1}}`, `[DONE]`} {
		if HasOutput([]byte(b)) {
			t.Fatal("non-output chunk counted", b)
		}
	}
	for _, b := range []string{`{"choices":[{"delta":{"content":"hi"}}]}`, `{"choices":[{"delta":{"tool_calls":[{"index":0}]}}]}`} {
		if !HasOutput([]byte(b)) {
			t.Fatal("output ignored", b)
		}
	}
}
