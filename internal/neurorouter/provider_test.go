package neurorouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIProvider_Name(t *testing.T) {
	p := &OpenAIProvider{BaseURL: "https://api.openai.com"}
	if p.Name() != "openai" {
		t.Errorf("expected 'openai', got %q", p.Name())
	}
}

func TestOpenAIProvider_Send(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("expected /v1/chat/completions, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer test-key, got %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"test","choices":[{"message":{"content":"hello"}}]}`))
	}))
	defer server.Close()

	p := &OpenAIProvider{BaseURL: server.URL, APIKey: "test-key"}
	resp, err := p.Send(context.Background(), &ChatRequest{
		Model:    "gpt-4o",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestOpenAIProvider_NoAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("expected no Authorization header when APIKey is empty")
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"test","choices":[{"message":{"content":"hello"}}]}`))
	}))
	defer server.Close()

	p := &OpenAIProvider{BaseURL: server.URL}
	resp, err := p.Send(context.Background(), &ChatRequest{
		Model:    "test",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp.Body.Close()
}

func TestProviderFromTarget(t *testing.T) {
	target := Target{BaseURL: "https://api.groq.com", APIKey: "gsk_test"}
	p := ProviderFromTarget(target, nil)

	if p.Name() != "openai" {
		t.Errorf("expected openai provider, got %q", p.Name())
	}
}

func TestProviderRegistry_Resolve(t *testing.T) {
	fallback := &OpenAIProvider{BaseURL: "https://fallback.com"}
	specific := &OpenAIProvider{BaseURL: "https://specific.com"}

	reg := NewProviderRegistry(fallback)
	reg.Register("gpt-4o", specific)

	// Registered model.
	p := reg.Resolve("gpt-4o")
	if p.(*OpenAIProvider).BaseURL != "https://specific.com" {
		t.Error("expected specific provider for gpt-4o")
	}

	// Unregistered model falls back.
	p = reg.Resolve("unknown-model")
	if p.(*OpenAIProvider).BaseURL != "https://fallback.com" {
		t.Error("expected fallback provider for unknown model")
	}
}

func TestProviderRegistry_SendToProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"test","choices":[{"message":{"content":"response"}}]}`))
	}))
	defer server.Close()

	p := &OpenAIProvider{BaseURL: server.URL}
	reg := NewProviderRegistry(p)

	body, status, err := reg.SendToProvider(context.Background(), &ChatRequest{
		Model:    "test",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != 200 {
		t.Errorf("expected 200, got %d", status)
	}
	if len(body) == 0 {
		t.Error("expected non-empty body")
	}
}
