package neurorouter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func mockChatServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Fatalf("method: got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("content-type: got %s", r.Header.Get("Content-Type"))
		}

		resp := ChatCompletionResponse{
			ID: "cmpl-test", Model: "test-model",
			Choices: []Choice{{Message: ChatMessage{Role: "assistant", Content: content}}},
			Usage:   &Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestClient_Basic(t *testing.T) {
	srv := mockChatServer(t, "hello back")
	defer srv.Close()

	client := &Client{
		BaseURL: srv.URL,
		Model:   "test-model",
	}

	resp, err := client.Complete(context.Background(), &CompletionRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "hello back" {
		t.Fatalf("Content = %q", resp.Content)
	}
	if resp.Model != "test-model" {
		t.Fatalf("Model = %q", resp.Model)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 8 {
		t.Fatalf("Usage = %+v", resp.Usage)
	}
}

func TestClient_APIKeyHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		resp := ChatCompletionResponse{
			ID: "cmpl-1", Choices: []Choice{{Message: ChatMessage{Content: "ok"}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, APIKey: "sk-test-123", Model: "m"}
	_, err := client.Complete(context.Background(), &CompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotAuth != "Bearer sk-test-123" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}

func TestClient_NoAPIKey(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		resp := ChatCompletionResponse{
			ID: "cmpl-1", Choices: []Choice{{Message: ChatMessage{Content: "ok"}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, Model: "m"}
	_, err := client.Complete(context.Background(), &CompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotAuth != "" {
		t.Fatalf("Authorization should be empty, got %q", gotAuth)
	}
}

func TestClient_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"server error"}`))
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, Model: "m"}
	_, err := client.Complete(context.Background(), &CompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if errors.Is(err, ErrRateLimited) {
		t.Fatal("500 should not be ErrRateLimited")
	}
}

func TestClient_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, Model: "m"}
	_, err := client.Complete(context.Background(), &CompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for HTTP 429")
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
}

func TestClient_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := ChatCompletionResponse{ID: "cmpl-1", Choices: []Choice{}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, Model: "m"}
	_, err := client.Complete(context.Background(), &CompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
}

func TestClient_ModelOverride(t *testing.T) {
	var gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotModel = req.Model
		resp := ChatCompletionResponse{
			ID: "cmpl-1", Choices: []Choice{{Message: ChatMessage{Content: "ok"}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, Model: "default-model"}
	_, err := client.Complete(context.Background(), &CompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Model:    "override-model",
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotModel != "override-model" {
		t.Fatalf("Model = %q, want override-model", gotModel)
	}
}

func TestClient_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// hang forever — context should cancel
		select {}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	client := &Client{BaseURL: srv.URL, Model: "m"}
	_, err := client.Complete(ctx, &CompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestClient_RequestFields(t *testing.T) {
	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		resp := ChatCompletionResponse{
			ID: "cmpl-1", Choices: []Choice{{Message: ChatMessage{Content: "ok"}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	temp := float64(0)
	client := &Client{BaseURL: srv.URL, Model: "m"}
	_, err := client.Complete(context.Background(), &CompletionRequest{
		Messages:    []ChatMessage{{Role: "system", Content: "be helpful"}, {Role: "user", Content: "hi"}},
		MaxTokens:   500,
		Temperature: &temp,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotReq["max_tokens"].(float64) != 500 {
		t.Fatalf("max_tokens = %v", gotReq["max_tokens"])
	}
	if gotReq["temperature"].(float64) != 0 {
		t.Fatalf("temperature = %v", gotReq["temperature"])
	}
}

func TestClient_RateLimitRejects(t *testing.T) {
	srv := mockChatServer(t, "ok")
	defer srv.Close()

	client := &Client{
		BaseURL:   srv.URL,
		Model:     "m",
		RateLimit: &RateLimit{RequestsPerMinute: 60, BurstSize: 2},
	}

	// First 2 requests should succeed (burst).
	for i := 0; i < 2; i++ {
		_, err := client.Complete(context.Background(), &CompletionRequest{
			Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}

	// Third request should be rate limited.
	_, err := client.Complete(context.Background(), &CompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
}

func TestClient_NoRateLimitByDefault(t *testing.T) {
	srv := mockChatServer(t, "ok")
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, Model: "m"}

	// Without RateLimit set, many requests should work.
	for i := 0; i < 10; i++ {
		_, err := client.Complete(context.Background(), &CompletionRequest{
			Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
}
