package provider

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"llm-gateway/internal/config"
)

// The first release deliberately accepts a documented text/tool subset.
type Request struct {
	Model         string          `json:"model"`
	Messages      []Message       `json:"messages"`
	Stream        bool            `json:"stream,omitempty"`
	MaxTokens     int64           `json:"max_tokens,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	Stop          json.RawMessage `json:"stop,omitempty"`
	Tools         []Tool          `json:"tools,omitempty"`
	ToolChoice    json.RawMessage `json:"tool_choice,omitempty"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
}
type Message struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}
type ToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function Function `json:"function"`
}
type Function struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Arguments   string          `json:"arguments,omitempty"`
}
type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}
type Usage struct {
	Input  int64
	Output int64
	Known  bool
}

func Decode(b []byte) (Request, error) {
	var r Request
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(&r); e != nil {
		return r, e
	}
	if d.Decode(new(any)) != io.EOF {
		return r, errors.New("request must contain exactly one JSON object")
	}
	if r.Model == "" || len(r.Messages) == 0 {
		return r, errors.New("model and messages are required")
	}
	if r.MaxTokens < 0 {
		return r, errors.New("max_tokens must be positive")
	}
	for _, m := range r.Messages {
		switch m.Role {
		case "system", "user", "assistant", "tool":
		default:
			return r, errors.New("unsupported message role")
		}
		if m.Content == nil && len(m.ToolCalls) == 0 {
			return r, errors.New("message requires content or tool_calls")
		}
		if m.Role != "assistant" && len(m.ToolCalls) > 0 {
			return r, errors.New("tool_calls are only valid on assistant messages")
		}
		if m.Role == "tool" && m.Content == nil {
			return r, errors.New("tool result requires text content")
		}
		if m.Role != "tool" && m.ToolCallID != "" {
			return r, errors.New("tool_call_id is only valid on tool messages")
		}
		if m.Role == "tool" && m.ToolCallID == "" {
			return r, errors.New("tool message requires tool_call_id")
		}
		for _, t := range m.ToolCalls {
			if t.Type != "function" || t.ID == "" || t.Function.Name == "" || !jsonObject([]byte(t.Function.Arguments)) {
				return r, errors.New("invalid tool call")
			}
		}
	}
	for _, t := range r.Tools {
		if t.Type != "function" || t.Function.Name == "" || !jsonObject(t.Function.Parameters) {
			return r, errors.New("invalid tool definition")
		}
	}
	if r.Temperature != nil && (*r.Temperature < 0 || *r.Temperature > 2) {
		return r, errors.New("temperature out of range")
	}
	if r.TopP != nil && (*r.TopP < 0 || *r.TopP > 1) {
		return r, errors.New("top_p out of range")
	}
	return r, nil
}
func jsonObject(b []byte) bool {
	b = bytes.TrimSpace(b)
	return len(b) > 1 && b[0] == '{' && json.Valid(b)
}

func Build(r Request, m config.Model, p config.Provider) ([]byte, error) {
	r.Model = m.Model
	if p.Type == "openai" {
		if r.Stream {
			r.StreamOptions = &struct {
				IncludeUsage bool `json:"include_usage"`
			}{true}
		}
		if !r.Stream {
			r.StreamOptions = nil
		}
		return json.Marshal(r)
	}
	body := map[string]any{"model": m.Model, "max_tokens": r.MaxTokens, "stream": r.Stream}
	system := []string{}
	messages := []any{}
	for _, v := range r.Messages {
		if v.Role == "system" {
			if v.Content != nil {
				system = append(system, *v.Content)
			}
			continue
		}
		role := v.Role
		blocks := []any{}
		if role == "tool" {
			role = "user"
			blocks = append(blocks, map[string]any{"type": "tool_result", "tool_use_id": v.ToolCallID, "content": *v.Content})
		} else {
			if v.Content != nil && *v.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": *v.Content})
			}
			for _, t := range v.ToolCalls {
				var input any
				if e := json.Unmarshal([]byte(t.Function.Arguments), &input); e != nil {
					return nil, e
				}
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": t.ID, "name": t.Function.Name, "input": input})
			}
		}
		if len(blocks) == 0 {
			return nil, errors.New("Anthropic requires nonempty message content")
		}
		messages = append(messages, map[string]any{"role": role, "content": blocks})
	}
	if len(messages) == 0 {
		return nil, errors.New("at least one non-system message required")
	}
	body["messages"] = messages
	if len(system) > 0 {
		body["system"] = strings.Join(system, "\n\n")
	}
	if r.Temperature != nil {
		if *r.Temperature > 1 {
			return nil, errors.New("Anthropic temperature must be <= 1")
		}
		body["temperature"] = r.Temperature
	}
	if r.TopP != nil {
		body["top_p"] = r.TopP
	}
	if len(r.Stop) > 0 && string(r.Stop) != "null" {
		var stops []string
		var one string
		if json.Unmarshal(r.Stop, &one) == nil {
			stops = []string{one}
		} else if e := json.Unmarshal(r.Stop, &stops); e != nil {
			return nil, errors.New("stop must be a string or string array")
		}
		body["stop_sequences"] = stops
	}
	if len(r.Tools) > 0 {
		tools := []any{}
		for _, t := range r.Tools {
			tools = append(tools, map[string]any{"name": t.Function.Name, "description": t.Function.Description, "input_schema": t.Function.Parameters})
		}
		body["tools"] = tools
	}
	if len(r.ToolChoice) > 0 && string(r.ToolChoice) != "null" {
		var choice string
		if json.Unmarshal(r.ToolChoice, &choice) == nil {
			switch choice {
			case "auto":
				body["tool_choice"] = map[string]string{"type": "auto"}
			case "required":
				body["tool_choice"] = map[string]string{"type": "any"}
			case "none":
				delete(body, "tools")
			default:
				return nil, errors.New("unsupported tool_choice")
			}
		} else {
			var t Tool
			if e := json.Unmarshal(r.ToolChoice, &t); e != nil || t.Type != "function" || t.Function.Name == "" {
				return nil, errors.New("invalid tool_choice")
			}
			body["tool_choice"] = map[string]string{"type": "tool", "name": t.Function.Name}
		}
	}
	return json.Marshal(body)
}
func ParseUsage(b []byte, kind string) Usage {
	var v struct {
		Usage struct {
			Prompt      int64 `json:"prompt_tokens"`
			Completion  int64 `json:"completion_tokens"`
			Input       int64 `json:"input_tokens"`
			CacheRead   int64 `json:"cache_read_input_tokens"`
			CacheCreate int64 `json:"cache_creation_input_tokens"`
			Output      int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil || len(raw["usage"]) == 0 || string(raw["usage"]) == "null" {
		return Usage{}
	}
	if json.Unmarshal(b, &v) != nil {
		return Usage{}
	}
	if kind == "anthropic" {
		input := v.Usage.Input + v.Usage.CacheRead + v.Usage.CacheCreate
		if input < 0 || input > 10000000 || v.Usage.Output < 0 || v.Usage.Output > 1000000 {
			return Usage{}
		}
		return Usage{input, v.Usage.Output, true}
	}
	if v.Usage.Prompt < 0 || v.Usage.Prompt > 10000000 || v.Usage.Completion < 0 || v.Usage.Completion > 1000000 {
		return Usage{}
	}
	return Usage{v.Usage.Prompt, v.Usage.Completion, true}
}
func finishReason(reason string) string {
	switch reason {
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	default:
		return "stop"
	}
}
func ConvertResponse(b []byte, alias string) ([]byte, error) {
	var v struct {
		ID      string `json:"id"`
		Stop    string `json:"stop_reason"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if e := json.Unmarshal(b, &v); e != nil {
		return nil, e
	}
	text := ""
	calls := []any{}
	for _, c := range v.Content {
		switch c.Type {
		case "text":
			text += c.Text
		case "tool_use":
			calls = append(calls, map[string]any{"id": c.ID, "type": "function", "function": map[string]any{"name": c.Name, "arguments": string(c.Input)}})
		default:
			return nil, fmt.Errorf("unsupported Anthropic content block %s", c.Type)
		}
	}
	msg := map[string]any{"role": "assistant", "content": text}
	if len(calls) > 0 {
		msg["tool_calls"] = calls
		if text == "" {
			msg["content"] = nil
		}
	}
	u := ParseUsage(b, "anthropic")
	return json.Marshal(map[string]any{"id": v.ID, "object": "chat.completion", "created": time.Now().Unix(), "model": alias, "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finishReason(v.Stop)}}, "usage": map[string]int64{"prompt_tokens": u.Input, "completion_tokens": u.Output, "total_tokens": u.Input + u.Output}})
}

// Each callback receives a complete raw SSE frame. Scanner limits fail explicitly.
func ReadSSE(r io.Reader, fn func([]byte) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	var frame bytes.Buffer
	for scanner.Scan() {
		line := scanner.Bytes()
		frame.Write(line)
		frame.WriteByte('\n')
		if frame.Len() > 4<<20 {
			return errors.New("SSE frame exceeds 4 MiB")
		}
		if len(line) == 0 {
			if e := fn(bytes.Clone(frame.Bytes())); e != nil {
				return e
			}
			frame.Reset()
		}
	}
	if e := scanner.Err(); e != nil {
		return e
	}
	if frame.Len() > 0 {
		return fn(frame.Bytes())
	}
	return nil
}
func SSEData(frame []byte) []byte {
	parts := [][]byte{}
	for _, line := range bytes.Split(frame, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("data:")) {
			parts = append(parts, bytes.TrimPrefix(bytes.TrimPrefix(line, []byte("data:")), []byte(" ")))
		}
	}
	return bytes.Join(parts, []byte("\n"))
}

type AnthropicStream struct {
	ID, Model     string
	Input, Output int64
	Known, Done   bool
	tools         map[int]int
	toolHasArgs   map[int]bool
	nextTool      int
	IncludeUsage  bool
}

func (s *AnthropicStream) chunk(delta any, finish any) []byte {
	b, _ := json.Marshal(map[string]any{"id": s.ID, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": s.Model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
	return append(append([]byte("data: "), b...), []byte("\n\n")...)
}
func (s *AnthropicStream) Convert(raw []byte) ([]byte, error) {
	b := SSEData(raw)
	if len(b) == 0 {
		return nil, nil
	}
	var v struct {
		Type    string `json:"type"`
		Index   int    `json:"index"`
		Message struct {
			ID    string `json:"id"`
			Usage struct {
				Input       int64 `json:"input_tokens"`
				CacheRead   int64 `json:"cache_read_input_tokens"`
				CacheCreate int64 `json:"cache_creation_input_tokens"`
				Output      int64 `json:"output_tokens"`
			} `json:"usage"`
		} `json:"message"`
		Block struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Text  string          `json:"text"`
			Input json.RawMessage `json:"input"`
		} `json:"content_block"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
			JSON string `json:"partial_json"`
			Stop string `json:"stop_reason"`
		} `json:"delta"`
		Usage struct {
			Output int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if e := json.Unmarshal(b, &v); e != nil {
		return nil, e
	}
	switch v.Type {
	case "message_start":
		s.ID = v.Message.ID
		s.Input = v.Message.Usage.Input + v.Message.Usage.CacheRead + v.Message.Usage.CacheCreate
		s.Output = v.Message.Usage.Output
		s.tools = map[int]int{}
		s.toolHasArgs = map[int]bool{}
		return s.chunk(map[string]string{"role": "assistant", "content": ""}, nil), nil
	case "content_block_start":
		switch v.Block.Type {
		case "tool_use":
			idx := s.nextTool
			s.nextTool++
			if s.tools == nil {
				return nil, errors.New("tool block before message start")
			}
			s.tools[v.Index] = idx
			args := ""
			if len(v.Block.Input) > 0 && string(v.Block.Input) != "{}" && string(v.Block.Input) != "null" {
				args = string(v.Block.Input)
				s.toolHasArgs[v.Index] = true
			}
			return s.chunk(map[string]any{"tool_calls": []any{map[string]any{"index": idx, "id": v.Block.ID, "type": "function", "function": map[string]string{"name": v.Block.Name, "arguments": args}}}}, nil), nil
		case "text":
			if v.Block.Text != "" {
				return s.chunk(map[string]string{"content": v.Block.Text}, nil), nil
			}
		default:
			return nil, fmt.Errorf("unsupported streaming block %s", v.Block.Type)
		}
	case "content_block_delta":
		switch v.Delta.Type {
		case "text_delta":
			return s.chunk(map[string]string{"content": v.Delta.Text}, nil), nil
		case "input_json_delta":
			idx, ok := s.tools[v.Index]
			if !ok {
				return nil, errors.New("tool delta without tool start")
			}
			if v.Delta.JSON != "" {
				s.toolHasArgs[v.Index] = true
			}
			return s.chunk(map[string]any{"tool_calls": []any{map[string]any{"index": idx, "function": map[string]string{"arguments": v.Delta.JSON}}}}, nil), nil
		default:
			return nil, errors.New("unsupported streaming delta")
		}
	case "message_delta":
		s.Output = v.Usage.Output
		s.Known = s.Input >= 0 && s.Input <= 10000000 && s.Output >= 0 && s.Output <= 1000000
		return s.chunk(map[string]any{}, finishReason(v.Delta.Stop)), nil
	case "message_stop":
		s.Done = true
		out := []byte{}
		if s.IncludeUsage {
			u, _ := json.Marshal(map[string]any{"id": s.ID, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": s.Model, "choices": []any{}, "usage": map[string]int64{"prompt_tokens": s.Input, "completion_tokens": s.Output, "total_tokens": s.Input + s.Output}})
			out = append(out, []byte("data: ")...)
			out = append(out, u...)
			out = append(out, []byte("\n\n")...)
		}
		return append(out, []byte("data: [DONE]\n\n")...), nil
	case "error":
		return nil, errors.New("upstream stream error")
	case "content_block_stop":
		if idx, ok := s.tools[v.Index]; ok && !s.toolHasArgs[v.Index] {
			s.toolHasArgs[v.Index] = true
			return s.chunk(map[string]any{"tool_calls": []any{map[string]any{"index": idx, "function": map[string]string{"arguments": "{}"}}}}, nil), nil
		}
		return nil, nil
	case "ping":
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported streaming event %s", v.Type)
	}
	return nil, nil
}

// HasOutput excludes role-only/usage chunks from time-to-first-output measurement.
func HasOutput(b []byte) bool {
	var v struct {
		Choices []struct {
			Delta struct {
				Content   string            `json:"content"`
				ToolCalls []json.RawMessage `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if json.Unmarshal(b, &v) != nil {
		return false
	}
	for _, c := range v.Choices {
		if c.Delta.Content != "" || len(c.Delta.ToolCalls) > 0 {
			return true
		}
	}
	return false
}
