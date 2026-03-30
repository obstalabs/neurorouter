package neurorouter

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleResponses_NonStreaming(t *testing.T) {
	// mock upstream Chat Completions API
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("auth header: %s", r.Header.Get("Authorization"))
		}

		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "deepseek-chat" {
			t.Fatalf("model: %s", req.Model)
		}
		if len(req.Messages) != 2 {
			t.Fatalf("messages: %d", len(req.Messages))
		}

		resp := ChatCompletionResponse{
			ID: "chatcmpl-123", Model: "deepseek-chat",
			Choices: []Choice{{Message: ChatMessage{Role: "assistant", Content: "hi there"}}},
			Usage:   &Usage{PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"deepseek-chat": {BaseURL: upstream.URL, APIKey: "test-key"},
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"deepseek-chat","input":[{"type":"message","role":"user","content":"hello"}],"instructions":"be helpful","stream":false}`
	resp, err := http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(b))
	}

	var result ResponsesAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Object != "response" {
		t.Fatalf("object: %s", result.Object)
	}
	if result.Status != "completed" {
		t.Fatalf("status: %s", result.Status)
	}
	if len(result.Output) != 1 {
		t.Fatalf("output: %d", len(result.Output))
	}
	if result.Output[0].Content[0].Text != "hi there" {
		t.Fatalf("text: %q", result.Output[0].Content[0].Text)
	}
	if result.Usage.InputTokens != 10 {
		t.Fatalf("usage: %+v", result.Usage)
	}
}

func TestHandleResponses_CodexAliasPath(t *testing.T) {
	var hits int

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-alias","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}],"status":"completed"}]}`))
	}))
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"gpt-5.4": {BaseURL: upstream.URL},
		},
		Capabilities: map[string]TargetCapabilities{
			"gpt-5.4": {
				Model:          "gpt-5.4",
				Provider:       "openai",
				WireAPI:        WireAPIResponses,
				Streaming:      true,
				ResponsesItems: true,
				ReasoningItems: true,
			},
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":"hi"}]}`
	resp, err := http.Post("http://"+addr+"/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(b))
	}
	if hits != 1 {
		t.Fatalf("upstream hits: got %d, want 1", hits)
	}
}

func TestHandleResponses_ChatCompletionsPathNotExposed(t *testing.T) {
	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"default": {BaseURL: "https://example.invalid"},
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	resp, err := http.Post("http://"+addr+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestHandleResponses_AnthropicMessagesPathNotExposed(t *testing.T) {
	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"default": {BaseURL: "https://example.invalid"},
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	resp, err := http.Post("http://"+addr+"/v1/messages", "application/json", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestHandleResponses_Streaming(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		stop := "stop"
		chunks := []ChatChunk{
			{ID: "c1", Model: "test", Choices: []ChunkChoice{{Delta: ChunkDelta{Role: "assistant", Content: "hello"}}}},
			{ID: "c2", Model: "test", Choices: []ChunkChoice{{Delta: ChunkDelta{Content: " world"}}}},
			{ID: "c3", Model: "test", Choices: []ChunkChoice{{Delta: ChunkDelta{}, FinishReason: &stop}}},
		}
		for _, ch := range chunks {
			b, _ := json.Marshal(ch)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen:  ":0",
		Targets: map[string]Target{"test": {BaseURL: upstream.URL}},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"test","input":[{"type":"message","role":"user","content":"hi"}],"stream":true}`
	resp, err := http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content-type: %s", resp.Header.Get("Content-Type"))
	}

	// read all SSE events
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	events := string(data)

	if !strings.Contains(events, "event: response.created") {
		t.Fatal("missing response.created event")
	}
	if !strings.Contains(events, "event: response.output_text.delta") {
		t.Fatal("missing text delta event")
	}
	if !strings.Contains(events, "event: response.completed") {
		t.Fatal("missing response.completed event")
	}
	if !strings.Contains(events, "hello world") {
		t.Fatal("missing full text in completed event")
	}
}

func TestHandleResponses_NativeResponsesNonStreamingPassthrough(t *testing.T) {
	var captured map[string]any

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-native","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"native ok"}],"status":"completed"}]}`))
	}))
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"gpt-5.4": {BaseURL: upstream.URL},
		},
		Capabilities: map[string]TargetCapabilities{
			"gpt-5.4": {
				Model:          "gpt-5.4",
				Provider:       "openai",
				WireAPI:        WireAPIResponses,
				Streaming:      true,
				Tools:          true,
				ToolResults:    true,
				ResponsesItems: true,
				ReasoningItems: true,
			},
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":"hello"},{"type":"shell_call_output","call_id":"call_123","output":"stdout","status":"completed"}]}`
	resp, err := http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(b))
	}
	if captured["input"].([]any)[1].(map[string]any)["type"] != "shell_call_output" {
		t.Fatalf("structured input item lost: %+v", captured["input"])
	}

	data, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(data), `"id":"resp-native"`) {
		t.Fatalf("response body was not passed through: %s", string(data))
	}
}

func TestHandleResponses_NativeResponsesStreamingPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "event: response.created\ndata: {\"id\":\"resp-native\"}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "event: response.reasoning.delta\ndata: {\"delta\":\"thinking\"}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "event: response.output_item.done\ndata: {\"type\":\"message\"}\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"gpt-5.4": {BaseURL: upstream.URL},
		},
		Capabilities: map[string]TargetCapabilities{
			"gpt-5.4": {
				Model:          "gpt-5.4",
				Provider:       "openai",
				WireAPI:        WireAPIResponses,
				Streaming:      true,
				Tools:          true,
				ToolResults:    true,
				ResponsesItems: true,
				ReasoningItems: true,
			},
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"gpt-5.4","stream":true,"input":[{"type":"message","role":"user","content":"hi"},{"type":"reasoning","content":"keep"}]}`
	resp, err := http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, _ := io.ReadAll(resp.Body)
	events := string(data)
	if !strings.Contains(events, "event: response.reasoning.delta") {
		t.Fatalf("native event stream was translated instead of passed through: %s", events)
	}
	if !strings.Contains(events, "event: response.output_item.done") {
		t.Fatalf("missing passthrough output item event: %s", events)
	}
}

func TestHandleResponses_NativeResponsesFiltersOnlyMessageText(t *testing.T) {
	var captured map[string]any

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-filtered","object":"response","status":"completed","output":[]}`))
	}))
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"gpt-5.4": {BaseURL: upstream.URL},
		},
		Capabilities: map[string]TargetCapabilities{
			"gpt-5.4": {
				Model:          "gpt-5.4",
				Provider:       "openai",
				WireAPI:        WireAPIResponses,
				Streaming:      true,
				Tools:          true,
				ToolResults:    true,
				ResponsesItems: true,
				ReasoningItems: true,
			},
		},
		Filters: FilterConfig{
			SystemReminders: true,
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"gpt-5.4","instructions":"shared system","input":[{"type":"message","role":"developer","content":"shared system"},{"type":"shell_call_output","call_id":"call_123","output":"stdout","status":"completed"},{"type":"message","role":"user","content":"hello"}]}`
	resp, err := http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(b))
	}
	if _, ok := captured["instructions"]; ok {
		t.Fatalf("instructions should have been deduplicated: %+v", captured)
	}
	input := captured["input"].([]any)
	if input[1].(map[string]any)["type"] != "shell_call_output" {
		t.Fatalf("shell_call_output changed: %+v", input[1])
	}
	if input[0].(map[string]any)["role"] != "developer" {
		t.Fatalf("developer role should be preserved upstream: %+v", input[0])
	}
}

func TestHandleResponses_UnknownModel(t *testing.T) {
	p := NewProxy(ProxyConfig{
		Listen:  ":0",
		Targets: map[string]Target{},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"unknown","input":[{"type":"message","role":"user","content":"hi"}]}`
	resp, err := http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusTooManyRequests)
	}
}

func TestHandleResponses_UpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen:  ":0",
		Targets: map[string]Target{"test": {BaseURL: upstream.URL}},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"test","input":[{"type":"message","role":"user","content":"hi"}]}`
	resp, err := http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status: got %d, want 429", resp.StatusCode)
	}
}

func TestHandleResponses_IncompatibleResponsesItemsReturnClearError(t *testing.T) {
	var hits int

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"test-model": {BaseURL: upstream.URL},
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"test-model","input":[{"type":"message","role":"user","content":"hi"},{"type":"shell_call_output","content":"stdout"}]}`
	resp, err := http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(data), "responses_items") {
		t.Fatalf("error body: %s", string(data))
	}
	if hits != 0 {
		t.Fatalf("upstream should not be hit, got %d requests", hits)
	}
}

func TestHandleResponses_OpenAIAdapterDeduplicatesSystemMessages(t *testing.T) {
	var captured ChatRequest

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		resp := ChatCompletionResponse{
			ID: "chatcmpl-openai", Choices: []Choice{{Message: ChatMessage{Content: "ok"}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"gpt-4o": {BaseURL: upstream.URL},
		},
		Capabilities: map[string]TargetCapabilities{
			"gpt-4o": {
				Model:     "gpt-4o",
				Provider:  "openai",
				WireAPI:   WireAPIChatCompletions,
				Streaming: true,
			},
		},
		Filters: FilterConfig{
			SystemReminders: true,
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"gpt-4o","instructions":"shared system","stream":false,"input":[{"type":"message","role":"developer","content":"shared system"},{"type":"message","role":"user","content":"hello"}]}`
	resp, err := http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(b))
	}

	if len(captured.Messages) != 2 {
		t.Fatalf("messages: got %d, want 2", len(captured.Messages))
	}
	if captured.Messages[0].Role != "system" || captured.Messages[0].Content != "shared system" {
		t.Fatalf("first message: %+v", captured.Messages[0])
	}
	if captured.Messages[1].Role != "user" {
		t.Fatalf("second message role: %s", captured.Messages[1].Role)
	}
}

func TestHandleResponses_OpenAIAdapterRemovesOrphanedToolResults(t *testing.T) {
	var captured ChatRequest

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		resp := ChatCompletionResponse{
			ID: "chatcmpl-tools", Choices: []Choice{{Message: ChatMessage{Content: "ok"}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"gpt-4o": {BaseURL: upstream.URL},
		},
		Capabilities: map[string]TargetCapabilities{
			"gpt-4o": {
				Model:     "gpt-4o",
				Provider:  "openai",
				WireAPI:   WireAPIChatCompletions,
				Streaming: true,
			},
		},
		Filters: FilterConfig{
			OrphanedResults: true,
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"gpt-4o","stream":false,"input":[{"type":"message","role":"assistant","content":"{\"tool_calls\":[{\"id\":\"call_abc\",\"function\":{\"name\":\"read_file\"}}]}"},{"type":"message","role":"tool","content":"{\"tool_call_id\":\"call_abc\",\"content\":\"ok\"}"},{"type":"message","role":"tool","content":"{\"tool_call_id\":\"call_missing\",\"content\":\"orphaned\"}"}]}`
	resp, err := http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(b))
	}

	if len(captured.Messages) != 2 {
		t.Fatalf("messages: got %d, want 2", len(captured.Messages))
	}
	for _, msg := range captured.Messages {
		if strings.Contains(msg.Content, "call_missing") {
			t.Fatalf("orphaned tool result was forwarded: %+v", msg)
		}
	}
}

func TestHandleResponses_ClaudeAdapterKeepsThinkingFilterLive(t *testing.T) {
	var captured ChatRequest

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		resp := ChatCompletionResponse{
			ID: "chatcmpl-claude", Choices: []Choice{{Message: ChatMessage{Content: "ok"}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"claude-sonnet": {BaseURL: upstream.URL},
		},
		Capabilities: map[string]TargetCapabilities{
			"claude-sonnet": {
				Model:     "claude-sonnet",
				Provider:  "anthropic",
				WireAPI:   WireAPIChatCompletions,
				Streaming: true,
			},
		},
		Filters: FilterConfig{
			Thinking: true,
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"claude-sonnet","stream":false,"input":[{"type":"message","role":"assistant","content":"<thinking>internal</thinking>Visible answer"},{"type":"message","role":"user","content":"hello"}]}`
	resp, err := http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(b))
	}

	if len(captured.Messages) != 2 {
		t.Fatalf("messages: got %d, want 2", len(captured.Messages))
	}
	if captured.Messages[0].Content != "Visible answer" {
		t.Fatalf("assistant content: got %q", captured.Messages[0].Content)
	}
}

func TestHandleResponses_CompatibleDefaultFallback(t *testing.T) {
	var specificHits, defaultHits int

	specificUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		specificHits++
		resp := ChatCompletionResponse{
			ID: "specific", Choices: []Choice{{Message: ChatMessage{Content: "wrong"}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer specificUpstream.Close()

	defaultUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		defaultHits++
		resp := ChatCompletionResponse{
			ID: "default", Choices: []Choice{{Message: ChatMessage{Content: "ok"}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer defaultUpstream.Close()

	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"test-model": {BaseURL: specificUpstream.URL},
			"default":    {BaseURL: defaultUpstream.URL},
		},
		Capabilities: map[string]TargetCapabilities{
			"test-model": {
				Model:     "test-model",
				Provider:  "openai",
				WireAPI:   WireAPIChatCompletions,
				Streaming: false,
			},
			"default": {
				Model:     "default",
				Provider:  "openai",
				WireAPI:   WireAPIChatCompletions,
				Streaming: true,
			},
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"test-model","stream":true,"input":[{"type":"message","role":"user","content":"hi"}]}`
	resp, err := http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(b))
	}
	if specificHits != 0 {
		t.Fatalf("specific upstream should be skipped, got %d hits", specificHits)
	}
	if defaultHits != 1 {
		t.Fatalf("default upstream hits: got %d, want 1", defaultHits)
	}
}

func TestHandleResponses_NoCompatibleStreamingTarget(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"test-model": {BaseURL: upstream.URL},
		},
		Capabilities: map[string]TargetCapabilities{
			"test-model": {
				Model:     "test-model",
				Provider:  "openai",
				WireAPI:   WireAPIChatCompletions,
				Streaming: false,
			},
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"test-model","stream":true,"input":[{"type":"message","role":"user","content":"hi"}]}`
	resp, err := http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(data), "streaming") {
		t.Fatalf("error body: %s", string(data))
	}
}

func TestHealth(t *testing.T) {
	p := NewProxy(ProxyConfig{Listen: ":0", Targets: map[string]Target{}})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	resp, err := http.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestStart_PortOnlyListenBindsLoopback(t *testing.T) {
	p := NewProxy(ProxyConfig{Listen: ":0", Targets: map[string]Target{}})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("host: got %q, want 127.0.0.1", host)
	}
}

func TestStart_PublicBindRequiresExplicitOptIn(t *testing.T) {
	p := NewProxy(ProxyConfig{Listen: "0.0.0.0:0", Targets: map[string]Target{}})
	_, err := p.Start()
	if err == nil {
		t.Fatal("expected public bind to require explicit opt-in")
	}
	if !strings.Contains(err.Error(), "requires explicit opt-in") {
		t.Fatalf("error: %v", err)
	}
}

func TestPublicBind_HidesManagementByDefault(t *testing.T) {
	p := NewProxy(ProxyConfig{
		Listen:            "0.0.0.0:0",
		AllowPublicListen: true,
		Targets:           map[string]Target{},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	for _, path := range []string{"/v1/audit", "/v1/suggestions", "/v1/dnd"} {
		resp, err := http.Get(localProxyURL(addr, path))
		if err != nil {
			t.Fatalf("request %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status: got %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestPublicBind_CanExposeManagement(t *testing.T) {
	p := NewProxy(ProxyConfig{
		Listen:            "0.0.0.0:0",
		AllowPublicListen: true,
		ExposeManagement:  true,
		Targets:           map[string]Target{},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	for _, path := range []string{"/v1/audit", "/v1/suggestions", "/v1/dnd"} {
		resp, err := http.Get(localProxyURL(addr, path))
		if err != nil {
			t.Fatalf("request %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status: got %d, want 200", path, resp.StatusCode)
		}
	}
}

func TestStartStop(t *testing.T) {
	p := NewProxy(ProxyConfig{Listen: ":0", Targets: map[string]Target{}})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if addr == "" {
		t.Fatal("addr is empty")
	}

	// verify it's listening
	resp, err := http.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatalf("health check: %v", err)
	}
	_ = resp.Body.Close()

	// stop and verify it's no longer listening
	if err := p.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	_, err = http.Get("http://" + addr + "/health")
	if err == nil {
		t.Fatal("expected connection refused after stop")
	}
}

func TestDNDEndpoint_ManualToggle(t *testing.T) {
	p := NewProxy(ProxyConfig{Listen: ":0", Targets: map[string]Target{}})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	snapshot := fetchDNDSnapshot(t, addr)
	if snapshot.Active {
		t.Fatalf("active: got true, want false")
	}
	if snapshot.Source != DNDSourceOff {
		t.Fatalf("source: got %q, want %q", snapshot.Source, DNDSourceOff)
	}
	if snapshot.Status != "off" {
		t.Fatalf("status: got %q, want off", snapshot.Status)
	}

	snapshot = postDNDToggle(t, addr, true)
	if !snapshot.Active || !snapshot.Manual {
		t.Fatalf("manual snapshot: %+v", snapshot)
	}
	if snapshot.Source != DNDSourceManual {
		t.Fatalf("source: got %q, want %q", snapshot.Source, DNDSourceManual)
	}

	snapshot = postDNDToggle(t, addr, false)
	if snapshot.Active {
		t.Fatalf("active after disable: got true, want false")
	}
	if snapshot.Source != DNDSourceOff {
		t.Fatalf("source after disable: got %q, want %q", snapshot.Source, DNDSourceOff)
	}
}

func TestSuggestions_DNDFiltersNonCritical(t *testing.T) {
	p := NewProxy(ProxyConfig{
		Listen:     ":0",
		Targets:    map[string]Target{},
		Neurocache: NeurocacheConfig{Enabled: true},
	})
	seedDNDSuggestions(t, p)

	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	postDNDToggle(t, addr, true)

	resp, err := http.Get(localProxyURL(addr, "/v1/suggestions"))
	if err != nil {
		t.Fatalf("suggestions: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var payload struct {
		Suggestions []Suggestion `json:"suggestions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode suggestions: %v", err)
	}

	if len(payload.Suggestions) != 1 {
		t.Fatalf("suggestions: got %d, want 1", len(payload.Suggestions))
	}
	if payload.Suggestions[0].Type != "reminder_spam" {
		t.Fatalf("suggestion type: got %q, want reminder_spam", payload.Suggestions[0].Type)
	}
	if payload.Suggestions[0].Severity != SeverityHigh {
		t.Fatalf("severity: got %q, want %q", payload.Suggestions[0].Severity, SeverityHigh)
	}
	if payload.Suggestions[0].Category != CategoryFilter {
		t.Fatalf("category: got %q, want %q", payload.Suggestions[0].Category, CategoryFilter)
	}
}

func TestHandleResponses_DNDSuppressesSuggestionHeader(t *testing.T) {
	p := NewProxy(ProxyConfig{
		Listen:  ":0",
		DryRun:  true,
		Targets: map[string]Target{"test": {BaseURL: "https://example.invalid"}},
		Neurocache: NeurocacheConfig{
			Enabled: true,
		},
	})
	seedDNDSuggestions(t, p)

	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"test","input":[{"type":"message","role":"user","content":"hi"}]}`
	resp, err := http.Post(localProxyURL(addr, "/v1/responses"), "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if got := resp.Header.Get("X-Neurorouter-Suggestions"); got != "2" {
		t.Fatalf("header before dnd: got %q, want 2", got)
	}

	postDNDToggle(t, addr, true)

	resp, err = http.Post(localProxyURL(addr, "/v1/responses"), "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request with dnd: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("X-Neurorouter-Suggestions"); got != "1" {
		t.Fatalf("header during dnd: got %q, want 1", got)
	}
}

func TestHandleResponses_RequestThrashActivatesAutoDND(t *testing.T) {
	p := NewProxy(ProxyConfig{
		Listen:  ":0",
		DryRun:  true,
		Targets: map[string]Target{"test": {BaseURL: "https://example.invalid"}},
	})
	p.dnd.thrashThreshold = 1

	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"test","input":[{"type":"message","role":"user","content":"hi"}]}`
	resp, err := http.Post(localProxyURL(addr, "/v1/responses"), "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()

	snapshot := fetchDNDSnapshot(t, addr)
	if !snapshot.Active {
		t.Fatal("expected auto DND to be active after thrash trigger")
	}
	if snapshot.Source != DNDSourceAuto {
		t.Fatalf("source: got %q, want %q", snapshot.Source, DNDSourceAuto)
	}
	if snapshot.Manual {
		t.Fatalf("manual: got true, want false")
	}
}

func TestHandleResponses_UpstreamErrorActivatesAutoDND(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"cooldown"}`))
	}))
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen:  ":0",
		Targets: map[string]Target{"test": {BaseURL: upstream.URL}},
	})
	p.dnd.errorThreshold = 1

	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"test","input":[{"type":"message","role":"user","content":"hi"}]}`
	resp, err := http.Post(localProxyURL(addr, "/v1/responses"), "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status: got %d, want 429", resp.StatusCode)
	}

	snapshot := fetchDNDSnapshot(t, addr)
	if !snapshot.Active {
		t.Fatal("expected auto DND to be active after upstream error")
	}
	if snapshot.Source != DNDSourceAuto {
		t.Fatalf("source: got %q, want %q", snapshot.Source, DNDSourceAuto)
	}
}

func localProxyURL(addr, path string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr + path
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + path
}

func fetchDNDSnapshot(t *testing.T, addr string) DNDSnapshot {
	t.Helper()

	resp, err := http.Get(localProxyURL(addr, "/v1/dnd"))
	if err != nil {
		t.Fatalf("fetch dnd: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dnd status: got %d, want 200", resp.StatusCode)
	}

	var snapshot DNDSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode dnd: %v", err)
	}
	return snapshot
}

func postDNDToggle(t *testing.T, addr string, enabled bool) DNDSnapshot {
	t.Helper()

	body := fmt.Sprintf(`{"enabled":%t}`, enabled)
	resp, err := http.Post(localProxyURL(addr, "/v1/dnd"), "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("toggle dnd: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("toggle status: got %d, want 200", resp.StatusCode)
	}

	var snapshot DNDSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode toggle dnd: %v", err)
	}
	return snapshot
}

func seedDNDSuggestions(t *testing.T, p *Proxy) {
	t.Helper()

	if p.pipeline == nil || p.pipeline.neurocache == nil {
		t.Fatal("proxy neurocache is not initialized")
	}

	p.pipeline.neurocache.fileReads["/tmp/repeat.txt"] = 3
	p.pipeline.neurocache.uniqueRemBytes = 1
	p.pipeline.neurocache.reminderBytes = 7_000_000
}

func TestPool_RoundRobin(t *testing.T) {
	var hits [2]int
	upstream0 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits[0]++
		resp := ChatCompletionResponse{
			ID: "c0", Choices: []Choice{{Message: ChatMessage{Content: "from-0"}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream0.Close()

	upstream1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits[1]++
		resp := ChatCompletionResponse{
			ID: "c1", Choices: []Choice{{Message: ChatMessage{Content: "from-1"}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream1.Close()

	p := NewProxy(ProxyConfig{
		Listen: ":0",
		TargetPool: map[string][]PoolTarget{
			"test-model": {
				{Target: Target{BaseURL: upstream0.URL}},
				{Target: Target{BaseURL: upstream1.URL}},
			},
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"test-model","input":[{"type":"message","role":"user","content":"hi"}]}`
	for i := 0; i < 4; i++ {
		resp, err := http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: status %d", i, resp.StatusCode)
		}
	}

	// Both should have received requests.
	if hits[0] == 0 || hits[1] == 0 {
		t.Fatalf("requests not distributed: hits=%v", hits)
	}
	if hits[0]+hits[1] != 4 {
		t.Fatalf("total hits: %d", hits[0]+hits[1])
	}
}

func TestPool_Failover(t *testing.T) {
	// upstream0 always fails with 500.
	upstream0 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream0.Close()

	// upstream1 always succeeds.
	upstream1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := ChatCompletionResponse{
			ID: "c1", Choices: []Choice{{Message: ChatMessage{Content: "ok"}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream1.Close()

	p := NewProxy(ProxyConfig{
		Listen: ":0",
		TargetPool: map[string][]PoolTarget{
			"test": {
				{Target: Target{BaseURL: upstream0.URL}},
				{Target: Target{BaseURL: upstream1.URL}},
			},
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"test","input":[{"type":"message","role":"user","content":"hi"}]}`

	// Send enough requests to trigger health threshold on upstream0.
	// Some may hit upstream0 (500), some upstream1 (200).
	successCount := 0
	for i := 0; i < 10; i++ {
		resp, err := http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if resp.StatusCode == http.StatusOK {
			successCount++
		}
		_ = resp.Body.Close()
	}

	// After upstream0 is marked unhealthy, all remaining requests
	// should go to upstream1. We should see more successes than failures.
	if successCount < 5 {
		t.Fatalf("expected majority successes with failover, got %d/10", successCount)
	}
}

func TestPool_RateLimitFallback(t *testing.T) {
	var hits [2]int

	upstream0 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits[0]++
		resp := ChatCompletionResponse{
			ID: "c0", Choices: []Choice{{Message: ChatMessage{Content: "from-0"}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream0.Close()

	upstream1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits[1]++
		resp := ChatCompletionResponse{
			ID: "c1", Choices: []Choice{{Message: ChatMessage{Content: "from-1"}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream1.Close()

	// upstream0 has very low rate limit, upstream1 has no limit.
	p := NewProxy(ProxyConfig{
		Listen: ":0",
		TargetPool: map[string][]PoolTarget{
			"test": {
				{Target: Target{BaseURL: upstream0.URL}, RateLimit: &RateLimit{RequestsPerMinute: 60, BurstSize: 1}},
				{Target: Target{BaseURL: upstream1.URL}},
			},
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"test","input":[{"type":"message","role":"user","content":"hi"}]}`
	for i := 0; i < 5; i++ {
		resp, err := http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: status %d", i, resp.StatusCode)
		}
	}

	// upstream0 should have at most 1 hit (burst), rest go to upstream1.
	if hits[0] > 2 {
		t.Fatalf("upstream0 should be rate limited, got %d hits", hits[0])
	}
	if hits[1] < 3 {
		t.Fatalf("upstream1 should get most requests, got %d hits", hits[1])
	}
}

func TestPool_AllExhausted(t *testing.T) {
	p := NewProxy(ProxyConfig{
		Listen: ":0",
		TargetPool: map[string][]PoolTarget{
			"test": {
				{Target: Target{BaseURL: "http://127.0.0.1:1"}, RateLimit: &RateLimit{RequestsPerMinute: 60, BurstSize: 1}},
			},
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"test","input":[{"type":"message","role":"user","content":"hi"}]}`

	// First request consumes the burst token (will fail on connect but
	// the limiter token is already consumed in resolveFromPool).
	resp, err := http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request 1: %v", err)
	}
	_ = resp.Body.Close()

	// Second request — rate limiter exhausted, should get 429.
	resp, err = http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request 2: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status: got %d, want 429", resp.StatusCode)
	}
}

func TestPool_BackwardCompat(t *testing.T) {
	// When only Targets is configured (no TargetPool), everything works as before.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := ChatCompletionResponse{
			ID: "c1", Choices: []Choice{{Message: ChatMessage{Content: "ok"}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen:  ":0",
		Targets: map[string]Target{"test": {BaseURL: upstream.URL}},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"test","input":[{"type":"message","role":"user","content":"hi"}]}`
	resp, err := http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}
