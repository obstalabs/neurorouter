package neurorouter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/klauspost/compress/zstd"
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

func TestHandleModels_ReturnsCodexDiscoveryShape(t *testing.T) {
	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"gpt-5.4-mini": {BaseURL: "https://api.openai.com"},
		},
		Capabilities: map[string]TargetCapabilities{
			"gpt-5.4-mini": {
				Model:          "gpt-5.4-mini",
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

	resp, err := http.Get("http://" + addr + "/models?client_version=0.98.0")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(b))
	}

	var result struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			ID        string  `json:"id"`
			Slug      string  `json:"slug"`
			Display   string  `json:"display_name"`
			Provider  string  `json:"provider"`
			WireAPI   WireAPI `json:"wire_api"`
			Streaming bool    `json:"streaming"`
			Upgrade   *struct {
				Model string `json:"model"`
			} `json:"upgrade"`
			Reasoning []struct {
				Effort      string `json:"effort"`
				Description string `json:"description"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if result.Object != "list" {
		t.Fatalf("object: got %q, want list", result.Object)
	}
	if len(result.Data) != 1 || result.Data[0].ID != "gpt-5.4-mini" {
		t.Fatalf("data: %+v", result.Data)
	}
	if len(result.Models) != 1 {
		t.Fatalf("models len: got %d, want 1", len(result.Models))
	}
	if result.Models[0].ID != "gpt-5.4-mini" {
		t.Fatalf("model id: got %q, want gpt-5.4-mini", result.Models[0].ID)
	}
	if result.Models[0].Slug != "gpt-5.4-mini" {
		t.Fatalf("model slug: got %q, want gpt-5.4-mini", result.Models[0].Slug)
	}
	if result.Models[0].Display != "gpt-5.4-mini" {
		t.Fatalf("model display name: got %q, want gpt-5.4-mini", result.Models[0].Display)
	}
	if result.Models[0].WireAPI != WireAPIResponses {
		t.Fatalf("wire api: got %q, want %q", result.Models[0].WireAPI, WireAPIResponses)
	}
	if !result.Models[0].Streaming {
		t.Fatal("expected model discovery to report streaming support")
	}
	if len(result.Models[0].Reasoning) == 0 {
		t.Fatal("expected model discovery to expose supported reasoning levels")
	}
	if result.Models[0].Upgrade != nil {
		t.Fatalf("upgrade: got %+v, want nil", result.Models[0].Upgrade)
	}
}

func TestHandleResponses_DecodesZstdRequestBody(t *testing.T) {
	var captured map[string]any

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.Error(w, "upgrade required", http.StatusUpgradeRequired)
			return
		}
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-zstd","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}],"status":"completed"}]}`))
	}))
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"gpt-5.4-mini": {BaseURL: upstream.URL},
		},
		Capabilities: map[string]TargetCapabilities{
			"gpt-5.4-mini": {
				Model:          "gpt-5.4-mini",
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

	body := `{"model":"gpt-5.4-mini","stream":true,"input":[{"type":"message","role":"user","content":"hello"}],"metadata":{"session_id":"codex-zstd"}}`
	req, err := http.NewRequest(http.MethodPost, localProxyURL(addr, "/responses"), bytes.NewReader(compressZstd(t, []byte(body))))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "zstd")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(b))
	}
	if captured["model"] != "gpt-5.4-mini" {
		t.Fatalf("captured model: %+v", captured["model"])
	}
	if captured["metadata"].(map[string]any)["session_id"] != "codex-zstd" {
		t.Fatalf("metadata lost: %+v", captured["metadata"])
	}
}

func TestHandleResponses_ForwardsClientAuthContextHeaders(t *testing.T) {
	var authHeader string
	var accountHeader string
	var originator string
	var version string
	var searchEligible string
	var sessionID string
	var requestID string
	var subagent string
	var beta string
	var turnMetadata string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		accountHeader = r.Header.Get("Chatgpt-Account-Id")
		originator = r.Header.Get("Originator")
		version = r.Header.Get("Version")
		searchEligible = r.Header.Get("X-Oai-Web-Search-Eligible")
		sessionID = r.Header.Get("session_id")
		requestID = r.Header.Get("X-Client-Request-Id")
		subagent = r.Header.Get("X-OpenAI-Subagent")
		beta = r.Header.Get("X-Codex-Beta-Features")
		turnMetadata = r.Header.Get(codexTurnMetadataHeader)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-client-auth","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}],"status":"completed"}]}`))
	}))
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"gpt-5.4-mini": {BaseURL: upstream.URL},
		},
		Capabilities: map[string]TargetCapabilities{
			"gpt-5.4-mini": {
				Model:          "gpt-5.4-mini",
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

	req, err := http.NewRequest(http.MethodPost, localProxyURL(addr, "/responses"), strings.NewReader(`{"model":"gpt-5.4-mini","input":[{"type":"message","role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer oauth-token")
	req.Header.Set("Chatgpt-Account-Id", "acct_123")
	req.Header.Set("Originator", "Codex Desktop")
	req.Header.Set("Version", "0.98.0")
	req.Header.Set("X-Oai-Web-Search-Eligible", "true")
	req.Header.Set("session_id", "sess_123")
	req.Header.Set("X-Client-Request-Id", "req_123")
	req.Header.Set("X-OpenAI-Subagent", "review")
	req.Header.Set("X-Codex-Beta-Features", "ws_v2")
	req.Header.Set(codexTurnMetadataHeader, `{"turn_id":"turn-123"}`)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(b))
	}
	if authHeader != "Bearer oauth-token" {
		t.Fatalf("authorization header: got %q, want bearer token", authHeader)
	}
	if accountHeader != "acct_123" {
		t.Fatalf("account header: got %q, want acct_123", accountHeader)
	}
	if originator != "Codex Desktop" {
		t.Fatalf("originator: got %q, want Codex Desktop", originator)
	}
	if version != "0.98.0" {
		t.Fatalf("version: got %q, want 0.98.0", version)
	}
	if searchEligible != "true" {
		t.Fatalf("search eligibility header: got %q, want true", searchEligible)
	}
	if sessionID != "sess_123" {
		t.Fatalf("session_id: got %q, want sess_123", sessionID)
	}
	if requestID != "req_123" {
		t.Fatalf("x-client-request-id: got %q, want req_123", requestID)
	}
	if subagent != "review" {
		t.Fatalf("x-openai-subagent: got %q, want review", subagent)
	}
	if beta != "ws_v2" {
		t.Fatalf("x-codex-beta-features: got %q, want ws_v2", beta)
	}
	if turnMetadata != `{"turn_id":"turn-123"}` {
		t.Fatalf("turn metadata: got %q", turnMetadata)
	}
}

func TestHandleResponses_RewritesOAuthScopeFailureClearly(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Missing scopes: api.responses.write","type":"invalid_request_error"}}`))
	}))
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"gpt-5.4-mini": {BaseURL: upstream.URL},
		},
		Capabilities: map[string]TargetCapabilities{
			"gpt-5.4-mini": {
				Model:          "gpt-5.4-mini",
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

	req, err := http.NewRequest(http.MethodPost, localProxyURL(addr, "/responses"), strings.NewReader(`{"model":"gpt-5.4-mini","input":[{"type":"message","role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer oauth-token")
	req.Header.Set("Chatgpt-Account-Id", "acct_123")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(b))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "supports Codex through an OpenAI API key today") {
		t.Fatalf("expected clearer OAuth compatibility error, got %s", string(body))
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
		if r.Method == http.MethodGet {
			http.Error(w, "upgrade required", http.StatusUpgradeRequired)
			return
		}
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

func TestHandleResponsesWebsocket_NativeResponsesStreamingPassthrough(t *testing.T) {
	var captured map[string]any

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.Error(w, "upgrade required", http.StatusUpgradeRequired)
			return
		}
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Fatalf("accept header: got %q, want text/event-stream", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-native\"}}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-native\"}}\n\n")
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

	wsURL := "ws://" + addr + "/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	request := map[string]any{
		"type":                "response.create",
		"model":               "gpt-5.4",
		"instructions":        "",
		"input":               []map[string]any{{"type": "message", "role": "user", "content": "hi"}},
		"tools":               []any{},
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
		"stream":              true,
		"store":               true,
		"include":             []any{},
		"client_metadata":     map[string]string{"traceparent": "00-test"},
	}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, requestBytes); err != nil {
		t.Fatalf("write websocket request: %v", err)
	}

	var events []string
	for len(events) < 3 {
		_, message, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read websocket message: %v", err)
		}
		events = append(events, string(message))
	}

	if _, ok := captured["type"]; ok {
		t.Fatalf("websocket request type should not be forwarded upstream: %+v", captured)
	}
	if _, ok := captured["client_metadata"]; ok {
		t.Fatalf("websocket client metadata should not be forwarded upstream: %+v", captured)
	}
	if !strings.Contains(events[0], `"type":"response.created"`) {
		t.Fatalf("missing created event: %s", events[0])
	}
	if !strings.Contains(events[1], `"type":"response.output_text.delta"`) {
		t.Fatalf("missing delta event: %s", events[1])
	}
	if !strings.Contains(events[2], `"type":"response.completed"`) {
		t.Fatalf("missing completed event: %s", events[2])
	}
}

func TestHandleResponsesWebsocket_UsesUpstreamWebsocketAcrossRequests(t *testing.T) {
	type capturedRequest struct {
		Body    map[string]any
		Session string
		Request string
		Beta    string
	}

	var (
		mu             sync.Mutex
		handshakeCount int
		captured       []capturedRequest
	)

	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "websocket required", http.StatusMethodNotAllowed)
			return
		}

		mu.Lock()
		handshakeCount++
		mu.Unlock()
		headers := http.Header{}
		headers.Set(codexTurnStateHeader, "turn-ws-123")
		conn, err := upgrader.Upgrade(w, r, headers)
		if err != nil {
			t.Fatalf("upgrade upstream websocket: %v", err)
		}
		defer func() { _ = conn.Close() }()

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var body map[string]any
			if err := json.Unmarshal(message, &body); err != nil {
				t.Fatalf("decode websocket request: %v", err)
			}

			mu.Lock()
			idx := len(captured)
			captured = append(captured, capturedRequest{
				Body:    body,
				Session: r.Header.Get("session_id"),
				Request: r.Header.Get("X-Client-Request-Id"),
				Beta:    r.Header.Get("OpenAI-Beta"),
			})
			mu.Unlock()

			var completed string
			if idx == 0 {
				completed = "resp-native-1"
			} else {
				completed = "resp-native-2"
			}

			if err := conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"response.created","response":{"id":"%s"}}`, completed))); err != nil {
				t.Fatalf("write created event: %v", err)
			}
			if err := conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":"%s"}}`, completed))); err != nil {
				t.Fatalf("write completed event: %v", err)
			}
		}
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"gpt-5.4": {BaseURL: upstream.URL, APIKey: "proxy-key"},
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

	headers := http.Header{}
	headers.Set("session_id", "sess-ws")
	headers.Set("X-Client-Request-Id", "req-ws")
	headers.Set("OpenAI-Beta", openAIResponsesWebsocketBetaHeaderValue)
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/responses", headers)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	first := map[string]any{
		"type":         "response.create",
		"model":        "gpt-5.4",
		"instructions": "",
		"input":        []map[string]any{{"type": "message", "role": "user", "content": "hi"}},
		"stream":       true,
		"store":        true,
		"client_metadata": map[string]string{
			codexTurnMetadataHeader: `{"turn_id":"turn-1"}`,
		},
	}
	firstBytes, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first request: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, firstBytes); err != nil {
		t.Fatalf("write first websocket request: %v", err)
	}
	readResponsesWebsocketUntilCompleted(t, conn)

	second := map[string]any{
		"type":                 "response.create",
		"model":                "gpt-5.4",
		"instructions":         "",
		"input":                []map[string]any{{"type": "message", "role": "user", "content": "next"}},
		"stream":               true,
		"store":                true,
		"previous_response_id": "resp-native-1",
		"client_metadata": map[string]string{
			codexTurnMetadataHeader: `{"turn_id":"turn-2"}`,
		},
	}
	secondBytes, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second request: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, secondBytes); err != nil {
		t.Fatalf("write second websocket request: %v", err)
	}
	readResponsesWebsocketUntilCompleted(t, conn)

	mu.Lock()
	defer mu.Unlock()

	if handshakeCount != 1 {
		t.Fatalf("upstream websocket handshakes: got %d, want 1", handshakeCount)
	}
	if len(captured) != 2 {
		t.Fatalf("captured requests: got %d, want 2", len(captured))
	}
	if captured[0].Body["type"] != "response.create" {
		t.Fatalf("first request type: got %#v, want response.create", captured[0].Body["type"])
	}
	if _, ok := captured[0].Body["client_metadata"]; !ok {
		t.Fatalf("first request missing client_metadata: %+v", captured[0].Body)
	}
	if got := captured[1].Body["previous_response_id"]; got != "resp-native-1" {
		t.Fatalf("second previous_response_id: got %#v, want %q", got, "resp-native-1")
	}
	if captured[0].Session != "sess-ws" || captured[0].Request != "req-ws" {
		t.Fatalf("upstream handshake headers: got session=%q request=%q", captured[0].Session, captured[0].Request)
	}
	if captured[0].Beta != openAIResponsesWebsocketBetaHeaderValue {
		t.Fatalf("upstream beta header: got %q, want %q", captured[0].Beta, openAIResponsesWebsocketBetaHeaderValue)
	}
}

func TestHandleResponsesWebsocket_ReusesUpstreamWebsocketAcrossClientReconnects(t *testing.T) {
	var (
		mu             sync.Mutex
		handshakeCount int
		captured       []map[string]any
	)

	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "websocket required", http.StatusMethodNotAllowed)
			return
		}

		mu.Lock()
		handshakeCount++
		mu.Unlock()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade upstream websocket: %v", err)
		}
		defer func() { _ = conn.Close() }()

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var body map[string]any
			if err := json.Unmarshal(message, &body); err != nil {
				t.Fatalf("decode websocket request: %v", err)
			}

			mu.Lock()
			idx := len(captured)
			captured = append(captured, body)
			mu.Unlock()

			responseID := "resp-native-1"
			if idx > 0 {
				responseID = "resp-native-2"
			}
			if err := conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"response.created","response":{"id":"%s"}}`, responseID))); err != nil {
				t.Fatalf("write created event: %v", err)
			}
			if err := conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":"%s"}}`, responseID))); err != nil {
				t.Fatalf("write completed event: %v", err)
			}
		}
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"gpt-5.4": {BaseURL: upstream.URL, APIKey: "proxy-key"},
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

	headers := http.Header{}
	headers.Set("session_id", "sess-reconnect")
	headers.Set("X-Client-Request-Id", "sess-reconnect")
	headers.Set("OpenAI-Beta", openAIResponsesWebsocketBetaHeaderValue)

	firstConn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/responses", headers)
	if err != nil {
		t.Fatalf("dial first websocket: %v", err)
	}
	first := map[string]any{
		"type":         "response.create",
		"model":        "gpt-5.4",
		"instructions": "",
		"input":        []map[string]any{{"type": "message", "role": "user", "content": "hi"}},
		"stream":       true,
		"store":        true,
	}
	firstBytes, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first request: %v", err)
	}
	if err := firstConn.WriteMessage(websocket.TextMessage, firstBytes); err != nil {
		t.Fatalf("write first websocket request: %v", err)
	}
	readResponsesWebsocketUntilCompleted(t, firstConn)
	_ = firstConn.Close()

	secondConn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/responses", headers)
	if err != nil {
		t.Fatalf("dial second websocket: %v", err)
	}
	defer func() { _ = secondConn.Close() }()

	second := map[string]any{
		"type":                 "response.create",
		"model":                "gpt-5.4",
		"instructions":         "",
		"input":                []map[string]any{{"type": "message", "role": "user", "content": "next"}},
		"stream":               true,
		"store":                true,
		"previous_response_id": "resp-native-1",
	}
	secondBytes, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second request: %v", err)
	}
	if err := secondConn.WriteMessage(websocket.TextMessage, secondBytes); err != nil {
		t.Fatalf("write second websocket request: %v", err)
	}
	readResponsesWebsocketUntilCompleted(t, secondConn)

	mu.Lock()
	defer mu.Unlock()

	if handshakeCount != 1 {
		t.Fatalf("upstream websocket handshakes: got %d, want 1", handshakeCount)
	}
	if len(captured) != 2 {
		t.Fatalf("captured requests: got %d, want 2", len(captured))
	}
	if got := captured[1]["previous_response_id"]; got != "resp-native-1" {
		t.Fatalf("second previous_response_id: got %#v, want %q", got, "resp-native-1")
	}
}

func TestHandleResponsesWebsocket_PreservesCodexTurnStateAcrossRequests(t *testing.T) {
	type capturedRequest struct {
		TurnState string
		Body      map[string]any
	}

	var (
		mu       sync.Mutex
		captured []capturedRequest
	)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.Error(w, "upgrade required", http.StatusUpgradeRequired)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		mu.Lock()
		idx := len(captured)
		captured = append(captured, capturedRequest{
			TurnState: r.Header.Get(codexTurnStateHeader),
			Body:      body,
		})
		mu.Unlock()

		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		if idx == 0 {
			w.Header().Set(codexTurnStateHeader, "turn-123")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-native-1\"}}\n\n")
			flusher.Flush()
			_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-native-1\"}}\n\n")
			flusher.Flush()
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-native-2\"}}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-native-2\"}}\n\n")
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

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/responses", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	first := map[string]any{
		"type":                "response.create",
		"model":               "gpt-5.4",
		"instructions":        "",
		"input":               []map[string]any{{"type": "message", "role": "user", "content": "hi"}},
		"tools":               []any{},
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
		"stream":              true,
		"store":               true,
		"include":             []any{},
	}
	firstBytes, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first request: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, firstBytes); err != nil {
		t.Fatalf("write first websocket request: %v", err)
	}

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read first websocket message: %v", err)
		}
		if strings.Contains(string(message), `"type":"response.completed"`) {
			break
		}
	}

	second := map[string]any{
		"type":                 "response.create",
		"model":                "gpt-5.4",
		"instructions":         "",
		"input":                []map[string]any{{"type": "message", "role": "user", "content": "next"}},
		"tools":                []any{},
		"tool_choice":          "auto",
		"parallel_tool_calls":  true,
		"stream":               true,
		"store":                true,
		"include":              []any{},
		"previous_response_id": "resp-native-1",
	}
	secondBytes, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second request: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, secondBytes); err != nil {
		t.Fatalf("write second websocket request: %v", err)
	}

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read second websocket message: %v", err)
		}
		if strings.Contains(string(message), `"type":"response.completed"`) {
			break
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if len(captured) != 2 {
		t.Fatalf("captured requests: got %d, want 2", len(captured))
	}
	if captured[0].TurnState != "" {
		t.Fatalf("first turn state: got %q, want empty", captured[0].TurnState)
	}
	if captured[1].TurnState != "turn-123" {
		t.Fatalf("second turn state: got %q, want %q", captured[1].TurnState, "turn-123")
	}
	if got := captured[1].Body["previous_response_id"]; got != "resp-native-1" {
		t.Fatalf("second previous_response_id: got %#v, want %q", got, "resp-native-1")
	}
}

func TestHandleResponses_PreservesCodexTurnStateHeader(t *testing.T) {
	var capturedHeaders []string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = append(capturedHeaders, r.Header.Get(codexTurnStateHeader))
		w.Header().Set("Content-Type", "application/json")
		if len(capturedHeaders) == 1 {
			w.Header().Set(codexTurnStateHeader, "turn-http-123")
		}
		_, _ = w.Write([]byte(`{"id":"resp-http","object":"response","status":"completed","output":[]}`))
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

	body := `{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":"hi"}]}`
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/responses", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if got := resp.Header.Get(codexTurnStateHeader); got != "turn-http-123" {
		t.Fatalf("first response turn state: got %q, want %q", got, "turn-http-123")
	}

	req, err = http.NewRequest(http.MethodPost, "http://"+addr+"/v1/responses", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new second request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(codexTurnStateHeader, "turn-http-123")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if len(capturedHeaders) != 2 {
		t.Fatalf("captured headers: got %d, want 2", len(capturedHeaders))
	}
	if capturedHeaders[0] != "" {
		t.Fatalf("first upstream turn state: got %q, want empty", capturedHeaders[0])
	}
	if capturedHeaders[1] != "turn-http-123" {
		t.Fatalf("second upstream turn state: got %q, want %q", capturedHeaders[1], "turn-http-123")
	}
}

func TestHandleResponses_ForwardsCompatibilityHeadersWithProxyAuth(t *testing.T) {
	var sessionID string
	var requestID string
	var subagent string
	var beta string
	var turnMetadata string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionID = r.Header.Get("session_id")
		requestID = r.Header.Get("X-Client-Request-Id")
		subagent = r.Header.Get("X-OpenAI-Subagent")
		beta = r.Header.Get("X-Codex-Beta-Features")
		turnMetadata = r.Header.Get(codexTurnMetadataHeader)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-http","object":"response","status":"completed","output":[]}`))
	}))
	defer upstream.Close()

	p := NewProxy(ProxyConfig{
		Listen: ":0",
		Targets: map[string]Target{
			"gpt-5.4": {BaseURL: upstream.URL, APIKey: "proxy-key"},
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

	body := `{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":"hi"}]}`
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/responses", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("session_id", "sess_456")
	req.Header.Set("X-Client-Request-Id", "req_456")
	req.Header.Set("X-OpenAI-Subagent", "review")
	req.Header.Set("X-Codex-Beta-Features", "ws_v2")
	req.Header.Set(codexTurnMetadataHeader, `{"turn_id":"turn-456"}`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if sessionID != "sess_456" {
		t.Fatalf("session_id: got %q, want sess_456", sessionID)
	}
	if requestID != "req_456" {
		t.Fatalf("x-client-request-id: got %q, want req_456", requestID)
	}
	if subagent != "review" {
		t.Fatalf("x-openai-subagent: got %q, want review", subagent)
	}
	if beta != "ws_v2" {
		t.Fatalf("x-codex-beta-features: got %q, want ws_v2", beta)
	}
	if turnMetadata != `{"turn_id":"turn-456"}` {
		t.Fatalf("turn metadata: got %q", turnMetadata)
	}
}

func readResponsesWebsocketUntilCompleted(t *testing.T, conn *websocket.Conn) {
	t.Helper()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read websocket message: %v", err)
		}
		if strings.Contains(string(message), `"type":"response.completed"`) {
			return
		}
		if strings.Contains(string(message), `"type":"error"`) {
			t.Fatalf("unexpected websocket error: %s", string(message))
		}
	}
}

func TestHandleResponsesWebsocket_ForwardsClientMetadataAsHeaders(t *testing.T) {
	var turnMetadata string
	var traceparent string
	var tracestate string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.Error(w, "upgrade required", http.StatusUpgradeRequired)
			return
		}
		turnMetadata = r.Header.Get(codexTurnMetadataHeader)
		traceparent = r.Header.Get("Traceparent")
		tracestate = r.Header.Get("Tracestate")

		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-native\"}}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-native\"}}\n\n")
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

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/responses", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	request := map[string]any{
		"type":                "response.create",
		"model":               "gpt-5.4",
		"instructions":        "",
		"input":               []map[string]any{{"type": "message", "role": "user", "content": "hi"}},
		"tools":               []any{},
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
		"stream":              true,
		"store":               true,
		"include":             []any{},
		"client_metadata": map[string]string{
			"x-codex-turn-metadata":         `{"turn_id":"turn-789"}`,
			"ws_request_header_traceparent": "00-abc-123-01",
			"ws_request_header_tracestate":  "vendor=value",
		},
	}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, requestBytes); err != nil {
		t.Fatalf("write websocket request: %v", err)
	}

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read websocket message: %v", err)
		}
		if strings.Contains(string(message), `"type":"response.completed"`) {
			break
		}
	}

	if turnMetadata != `{"turn_id":"turn-789"}` {
		t.Fatalf("turn metadata: got %q", turnMetadata)
	}
	if traceparent != "00-abc-123-01" {
		t.Fatalf("traceparent: got %q", traceparent)
	}
	if tracestate != "vendor=value" {
		t.Fatalf("tracestate: got %q", tracestate)
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

func TestHandleResponses_NativeResponsesAuditsCodexWasteRemoval(t *testing.T) {
	var captured map[string]any

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-proof","object":"response","status":"completed","output":[]}`))
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
			StaleReads:      true,
			OrphanedResults: true,
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"gpt-5.4","instructions":"Follow repo policy.","input":[{"type":"message","role":"developer","content":"Follow repo policy."},{"type":"message","role":"assistant","content":"{\"tool_calls\":[{\"id\":\"call_read_1\",\"function\":{\"name\":\"Read\",\"arguments\":{\"file_path\":\"/repo/README.md\"}}}]}"},{"type":"message","role":"tool","content":"{\"tool_call_id\":\"call_missing\",\"content\":\"stale orphaned output\"}"},{"type":"message","role":"assistant","content":"{\"tool_calls\":[{\"id\":\"call_read_2\",\"function\":{\"name\":\"Read\",\"arguments\":{\"file_path\":\"/repo/README.md\"}}}]}"},{"type":"message","role":"tool","content":"{\"tool_call_id\":\"call_read_2\",\"content\":\"fresh output\"}"},{"type":"shell_call_output","call_id":"call_shell","output":"pwd","status":"completed"},{"type":"message","role":"user","content":"Summarize the repo."}],"metadata":{"session_id":"codex-proof"}}`
	resp, err := http.Post(localProxyURL(addr, "/v1/responses"), "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(b))
	}

	if _, ok := captured["instructions"]; ok {
		t.Fatalf("instructions should have been removed after dedupe: %+v", captured)
	}

	input := captured["input"].([]any)
	if len(input) != 5 {
		t.Fatalf("input items: got %d, want 5", len(input))
	}
	if input[0].(map[string]any)["role"] != "developer" {
		t.Fatalf("first input should remain developer: %+v", input[0])
	}
	if input[1].(map[string]any)["content"] != `{"tool_calls":[{"id":"call_read_2","function":{"name":"Read","arguments":{"file_path":"/repo/README.md"}}}]}` {
		t.Fatalf("stale read was not removed: %+v", input[1])
	}
	if input[2].(map[string]any)["content"] != `{"tool_call_id":"call_read_2","content":"fresh output"}` {
		t.Fatalf("valid tool result should remain: %+v", input[2])
	}
	if input[3].(map[string]any)["type"] != "shell_call_output" {
		t.Fatalf("non-message item should be preserved: %+v", input[3])
	}

	audit := fetchAuditPayload(t, addr, "codex-proof")
	if audit.Count != 1 {
		t.Fatalf("audit count: got %d, want 1", audit.Count)
	}
	entry := audit.Entries[0]
	if entry.BytesBefore <= entry.BytesAfter {
		t.Fatalf("expected bytes to shrink, got before=%d after=%d", entry.BytesBefore, entry.BytesAfter)
	}
	if entry.BytesRemoved <= 0 {
		t.Fatalf("expected bytes removed, got %d", entry.BytesRemoved)
	}
	if entry.BytesRemoved < 100 {
		t.Fatalf("expected material byte reduction, got %d bytes removed", entry.BytesRemoved)
	}
	if got, want := strings.Join(entry.FiltersRun, ","), "system_reminders,stale_reads,orphaned_results"; got != want {
		t.Fatalf("filters run: got %q, want %q", got, want)
	}
}

func TestHandleResponses_NativeResponsesAuditsLargeCodexWasteRemoval(t *testing.T) {
	var captured map[string]any

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-proof-large","object":"response","status":"completed","output":[]}`))
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
			StaleReads:      true,
			OrphanedResults: true,
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	largeOrphanedOutput := strings.Repeat("orphaned codex tool output line\n", 5000)
	largeOrphanedContent, err := json.Marshal(map[string]string{
		"tool_call_id": "call_missing",
		"content":      largeOrphanedOutput,
	})
	if err != nil {
		t.Fatalf("marshal orphaned tool content: %v", err)
	}

	body := map[string]any{
		"model":        "gpt-5.4",
		"instructions": "Follow repo policy.",
		"input": []map[string]any{
			{
				"type":    "message",
				"role":    "developer",
				"content": "Follow repo policy.",
			},
			{
				"type":    "message",
				"role":    "assistant",
				"content": `{"tool_calls":[{"id":"call_read_1","function":{"name":"Read","arguments":{"file_path":"/repo/README.md"}}}]}`,
			},
			{
				"type":    "message",
				"role":    "tool",
				"content": string(largeOrphanedContent),
			},
			{
				"type":    "message",
				"role":    "assistant",
				"content": `{"tool_calls":[{"id":"call_read_2","function":{"name":"Read","arguments":{"file_path":"/repo/README.md"}}}]}`,
			},
			{
				"type":    "message",
				"role":    "tool",
				"content": `{"tool_call_id":"call_read_2","content":"fresh output"}`,
			},
			{
				"type":    "shell_call_output",
				"call_id": "call_shell",
				"output":  "pwd",
				"status":  "completed",
			},
			{
				"type":    "message",
				"role":    "user",
				"content": "Summarize the repo.",
			},
		},
		"metadata": map[string]any{
			"session_id": "codex-proof-large",
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}

	resp, err := http.Post(localProxyURL(addr, "/v1/responses"), "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(b))
	}

	input := captured["input"].([]any)
	if len(input) != 5 {
		t.Fatalf("input items: got %d, want 5", len(input))
	}
	if tool := input[2].(map[string]any)["content"]; tool != `{"tool_call_id":"call_read_2","content":"fresh output"}` {
		t.Fatalf("valid tool result should remain after cleanup: %+v", input[2])
	}

	audit := fetchAuditPayload(t, addr, "codex-proof-large")
	if audit.Count != 1 {
		t.Fatalf("audit count: got %d, want 1", audit.Count)
	}
	entry := audit.Entries[0]
	if entry.BytesBefore <= entry.BytesAfter {
		t.Fatalf("expected bytes to shrink, got before=%d after=%d", entry.BytesBefore, entry.BytesAfter)
	}
	if entry.BytesRemoved < 100*1024 {
		t.Fatalf("expected >100KB removed, got %d bytes", entry.BytesRemoved)
	}
	if got, want := strings.Join(entry.FiltersRun, ","), "system_reminders,stale_reads,orphaned_results"; got != want {
		t.Fatalf("filters run: got %q, want %q", got, want)
	}
}

func TestHandleResponses_NativeResponsesAuditsLargeStructuredCodexWasteRemoval(t *testing.T) {
	var captured map[string]any

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-proof-structured","object":"response","status":"completed","output":[]}`))
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
			StaleReads:      true,
			OrphanedResults: true,
		},
	})
	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	largeOrphanedOutput := strings.Repeat("structured orphaned tool output line\n", 5000)
	body := map[string]any{
		"model": "gpt-5.4",
		"input": []map[string]any{
			{
				"type":      "function_call",
				"call_id":   "call_read_1",
				"name":      "Read",
				"arguments": `{"file_path":"/repo/README.md"}`,
			},
			{
				"type":    "function_call_output",
				"call_id": "call_read_1",
				"output":  "stale output",
			},
			{
				"type":    "function_call_output",
				"call_id": "call_missing",
				"output":  largeOrphanedOutput,
			},
			{
				"type":      "function_call",
				"call_id":   "call_read_2",
				"name":      "Read",
				"arguments": `{"file_path":"/repo/README.md"}`,
			},
			{
				"type":    "function_call_output",
				"call_id": "call_read_2",
				"output":  "fresh output",
			},
			{
				"type":    "message",
				"role":    "user",
				"content": "Summarize the repo.",
			},
		},
		"metadata": map[string]any{
			"session_id": "codex-proof-structured",
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}

	resp, err := http.Post(localProxyURL(addr, "/v1/responses"), "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(b))
	}

	input := captured["input"].([]any)
	if len(input) != 3 {
		t.Fatalf("input items: got %d, want 3", len(input))
	}
	if input[0].(map[string]any)["call_id"] != "call_read_2" {
		t.Fatalf("expected latest read call to remain, got %+v", input[0])
	}
	if input[1].(map[string]any)["call_id"] != "call_read_2" {
		t.Fatalf("expected latest read output to remain, got %+v", input[1])
	}

	audit := fetchAuditPayload(t, addr, "codex-proof-structured")
	if audit.Count != 1 {
		t.Fatalf("audit count: got %d, want 1", audit.Count)
	}
	entry := audit.Entries[0]
	if entry.BytesBefore <= entry.BytesAfter {
		t.Fatalf("expected bytes to shrink, got before=%d after=%d", entry.BytesBefore, entry.BytesAfter)
	}
	if entry.BytesRemoved < 100*1024 {
		t.Fatalf("expected >100KB removed, got %d bytes", entry.BytesRemoved)
	}
	if got, want := strings.Join(entry.FiltersRun, ","), "stale_reads,orphaned_results"; got != want {
		t.Fatalf("filters run: got %q, want %q", got, want)
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

func TestHandleResponses_AuditIsolationBySession(t *testing.T) {
	p := NewProxy(ProxyConfig{
		Listen: ":0",
		DryRun: true,
		Targets: map[string]Target{
			"test": {BaseURL: "https://example.invalid"},
		},
		Neurocache: NeurocacheConfig{Enabled: true},
	})

	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"test","input":[{"type":"message","role":"user","content":"hi"}]}`
	postSessionRequest(t, addr, "alpha", body)
	postSessionRequest(t, addr, "beta", body)

	alphaAudit := fetchAuditPayload(t, addr, "alpha")
	if alphaAudit.Count != 1 {
		t.Fatalf("alpha audit count: got %d, want 1", alphaAudit.Count)
	}

	betaAudit := fetchAuditPayload(t, addr, "beta")
	if betaAudit.Count != 1 {
		t.Fatalf("beta audit count: got %d, want 1", betaAudit.Count)
	}
}

func TestHandleResponses_SessionScopedSuggestionsAndDND(t *testing.T) {
	p := NewProxy(ProxyConfig{
		Listen: ":0",
		DryRun: true,
		Targets: map[string]Target{
			"test": {BaseURL: "https://example.invalid"},
		},
		Neurocache: NeurocacheConfig{Enabled: true},
	})

	alphaRuntime := p.runtimeForSession("alpha")
	betaRuntime := p.runtimeForSession("beta")
	seedDNDSuggestionsInRuntime(t, alphaRuntime)
	seedDNDSuggestionsInRuntime(t, betaRuntime)

	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	alphaSuggestions := fetchSuggestionsPayload(t, addr, "alpha")
	if len(alphaSuggestions.Suggestions) != 2 {
		t.Fatalf("alpha suggestions before dnd: got %d, want 2", len(alphaSuggestions.Suggestions))
	}

	betaSuggestions := fetchSuggestionsPayload(t, addr, "beta")
	if len(betaSuggestions.Suggestions) != 2 {
		t.Fatalf("beta suggestions before dnd: got %d, want 2", len(betaSuggestions.Suggestions))
	}

	postDNDToggleForSession(t, addr, "alpha", true)

	alphaSnapshot := fetchDNDSnapshotForSession(t, addr, "alpha")
	if !alphaSnapshot.Active {
		t.Fatal("expected alpha dnd to be active")
	}

	betaSnapshot := fetchDNDSnapshotForSession(t, addr, "beta")
	if betaSnapshot.Active {
		t.Fatal("expected beta dnd to remain inactive")
	}

	body := `{"model":"test","input":[{"type":"message","role":"user","content":"hi"}]}`
	alphaResp := postSessionRequest(t, addr, "alpha", body)
	defer func() { _ = alphaResp.Body.Close() }()
	if got := alphaResp.Header.Get("X-Neurorouter-Suggestions"); got != "1" {
		t.Fatalf("alpha header: got %q, want 1", got)
	}

	betaResp := postSessionRequest(t, addr, "beta", body)
	defer func() { _ = betaResp.Body.Close() }()
	if got := betaResp.Header.Get("X-Neurorouter-Suggestions"); got != "2" {
		t.Fatalf("beta header: got %q, want 2", got)
	}
}

func TestHandleResponses_UsesMetadataSessionKey(t *testing.T) {
	p := NewProxy(ProxyConfig{
		Listen: ":0",
		DryRun: true,
		Targets: map[string]Target{
			"test": {BaseURL: "https://example.invalid"},
		},
		Neurocache: NeurocacheConfig{Enabled: true},
	})

	addr, err := p.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	body := `{"model":"test","input":[{"type":"message","role":"user","content":"hi"}],"metadata":{"session_id":"codex-thread-1"}}`
	resp, err := http.Post(localProxyURL(addr, "/v1/responses"), "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	audit := fetchAuditPayload(t, addr, "codex-thread-1")
	if audit.Count != 1 {
		t.Fatalf("metadata audit count: got %d, want 1", audit.Count)
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

func fetchDNDSnapshotForSession(t *testing.T, addr, session string) DNDSnapshot {
	t.Helper()

	resp, err := http.Get(localProxyURL(addr, "/v1/dnd?session="+session))
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

func postDNDToggleForSession(t *testing.T, addr, session string, enabled bool) DNDSnapshot {
	t.Helper()

	body := fmt.Sprintf(`{"enabled":%t}`, enabled)
	resp, err := http.Post(localProxyURL(addr, "/v1/dnd?session="+session), "application/json", strings.NewReader(body))
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

	seedDNDSuggestionsInRuntime(t, p.runtimeForSession(defaultSessionKey))
}

func seedDNDSuggestionsInRuntime(t *testing.T, runtime *sessionRuntime) {
	t.Helper()

	if runtime == nil || runtime.pipeline == nil || runtime.pipeline.neurocache == nil {
		t.Fatal("proxy neurocache is not initialized")
	}

	runtime.pipeline.neurocache.fileReads["/tmp/repeat.txt"] = 3
	runtime.pipeline.neurocache.uniqueRemBytes = 1
	runtime.pipeline.neurocache.reminderBytes = 7_000_000
}

func postSessionRequest(t *testing.T, addr, session, body string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, localProxyURL(addr, "/v1/responses"), strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(sessionHeaderName, session)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post session request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("status %d: %s", resp.StatusCode, string(data))
	}
	return resp
}

func fetchSuggestionsPayload(t *testing.T, addr, session string) struct {
	Suggestions []Suggestion `json:"suggestions"`
} {
	t.Helper()

	resp, err := http.Get(localProxyURL(addr, "/v1/suggestions?session="+session))
	if err != nil {
		t.Fatalf("suggestions: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("suggestions status: got %d, want 200", resp.StatusCode)
	}

	var payload struct {
		Suggestions []Suggestion `json:"suggestions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode suggestions: %v", err)
	}
	return payload
}

func fetchAuditPayload(t *testing.T, addr, session string) struct {
	Count   int          `json:"count"`
	Entries []AuditEntry `json:"entries"`
} {
	t.Helper()

	resp, err := http.Get(localProxyURL(addr, "/v1/audit?session="+session))
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("audit status: got %d, want 200", resp.StatusCode)
	}

	var payload struct {
		Count   int          `json:"count"`
		Entries []AuditEntry `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode audit: %v", err)
	}
	return payload
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

func compressZstd(t *testing.T, body []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	writer, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("new zstd writer: %v", err)
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatalf("write zstd body: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zstd writer: %v", err)
	}
	return buf.Bytes()
}
