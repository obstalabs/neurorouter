package neurorouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Provider abstracts an upstream LLM endpoint.
// Implementations handle the specifics of sending requests
// and parsing responses for a given API format.
type Provider interface {
	// Name returns the provider identifier (e.g. "openai", "anthropic").
	Name() string

	// Send sends a ChatRequest to the upstream and returns the raw HTTP response.
	// The caller is responsible for closing the response body.
	Send(ctx context.Context, req *ChatRequest) (*http.Response, error)
}

// OpenAIProvider handles OpenAI-compatible Chat Completions endpoints.
// Works with OpenAI, Groq, DeepSeek, Ollama, and any OpenAI-compatible API.
type OpenAIProvider struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// Name returns "openai".
func (p *OpenAIProvider) Name() string { return "openai" }

// Send sends a ChatRequest to the OpenAI-compatible endpoint.
func (p *OpenAIProvider) Send(ctx context.Context, req *ChatRequest) (*http.Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimSuffix(p.BaseURL, "/") + "/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
	}

	client := p.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(httpReq)
}

// ProviderFromTarget creates the appropriate Provider for a Target.
// Currently all targets are OpenAI-compatible.
// Future: detect Anthropic by URL pattern and return AnthropicProvider.
func ProviderFromTarget(t Target, httpClient *http.Client) Provider {
	return &OpenAIProvider{
		BaseURL:    t.BaseURL,
		APIKey:     t.APIKey,
		HTTPClient: httpClient,
	}
}

// ProviderRegistry maps model names to providers for smart routing.
type ProviderRegistry struct {
	providers map[string]Provider // model → provider
	fallback  Provider            // default provider
}

// NewProviderRegistry creates a registry with a fallback provider.
func NewProviderRegistry(fallback Provider) *ProviderRegistry {
	return &ProviderRegistry{
		providers: make(map[string]Provider),
		fallback:  fallback,
	}
}

// Register maps a model name to a provider.
func (r *ProviderRegistry) Register(model string, p Provider) {
	r.providers[model] = p
}

// Resolve returns the provider for a model, falling back to the default.
func (r *ProviderRegistry) Resolve(model string) Provider {
	if p, ok := r.providers[model]; ok {
		return p
	}
	return r.fallback
}

// SendToProvider is a convenience function that sends a ChatRequest through the
// appropriate provider based on model name. Returns the response body bytes
// and status code.
func (r *ProviderRegistry) SendToProvider(ctx context.Context, req *ChatRequest) ([]byte, int, error) {
	p := r.Resolve(req.Model)
	resp, err := p.Send(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	return body, resp.StatusCode, nil
}
