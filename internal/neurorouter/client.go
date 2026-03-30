package neurorouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ErrRateLimited is returned when the upstream API responds with HTTP 429.
// Callers can check with errors.Is(err, ErrRateLimited) to distinguish
// rate limiting from other failures and implement backpressure.
var ErrRateLimited = errors.New("neurorouter: rate limited")

// defaultTimeout is the HTTP client timeout when Client.HTTPClient is nil.
const defaultTimeout = 60 * time.Second

// Client is an OpenAI-compatible Chat Completions client.
type Client struct {
	BaseURL    string       // Chat Completions endpoint URL (e.g. "https://api.groq.com/openai/v1/chat/completions")
	APIKey     string       // API key (sent as Bearer token if non-empty)
	Model      string       // default model name
	HTTPClient *http.Client // if nil, uses default with 60s timeout
	RateLimit  *RateLimit   // optional client-side rate limiting

	limiterOnce sync.Once
	limiter     *rateLimiter
}

// CompletionRequest is a Chat Completions request.
type CompletionRequest struct {
	Messages    []ChatMessage // required
	Model       string        // overrides Client.Model if non-empty
	MaxTokens   int
	Temperature *float64
}

// CompletionResponse is a Chat Completions response.
type CompletionResponse struct {
	ID      string // completion ID from the API
	Model   string // model that generated the response
	Content string // extracted text from choices[0].message.content
	Usage   *Usage
}

// Complete sends a Chat Completions request and returns the response.
func (c *Client) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	// Client-side rate limiting (optional).
	if c.RateLimit != nil {
		c.limiterOnce.Do(func() {
			c.limiter = newRateLimiter(*c.RateLimit)
		})
		if !c.limiter.Allow() {
			return nil, fmt.Errorf("%w: client-side limit exceeded", ErrRateLimited)
		}
	}

	model := req.Model
	if model == "" {
		model = c.Model
	}

	payload := map[string]any{
		"model":    model,
		"messages": req.Messages,
	}
	if req.MaxTokens > 0 {
		payload["max_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("%w: HTTP 429: %s", ErrRateLimited, strings.TrimSpace(string(respBody)))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var result ChatCompletionResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("empty response: no choices")
	}

	return &CompletionResponse{
		ID:      result.ID,
		Model:   result.Model,
		Content: strings.TrimSpace(result.Choices[0].Message.Content),
		Usage:   result.Usage,
	}, nil
}
