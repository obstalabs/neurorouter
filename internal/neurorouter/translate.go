package neurorouter

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// --- Responses API types (what codex sends) ---

// ResponsesRequest is the Responses API request body.
type ResponsesRequest struct {
	Model           string          `json:"model"`
	Input           []InputItem     `json:"input"`
	Instructions    string          `json:"instructions,omitempty"`
	MaxOutputTokens int             `json:"max_output_tokens,omitempty"`
	Stream          bool            `json:"stream"`
	Text            *ResponseText   `json:"text,omitempty"`
	Tools           []ResponseTool  `json:"tools,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"top_p,omitempty"`
	Extra           json.RawMessage `json:"-"` // catch-all for unknown fields
}

// InputItem is a single item in the Responses API input array.
type InputItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
}

// ContentPart is one element in a content array.
type ContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ResponseText describes text output configuration for Responses API requests.
type ResponseText struct {
	Format *ResponseFormat `json:"format,omitempty"`
}

// ResponseFormat captures structured-output mode when present.
type ResponseFormat struct {
	Type string `json:"type,omitempty"`
}

// ResponseTool is an intentionally loose placeholder for tool definitions.
type ResponseTool struct {
	Type string `json:"type,omitempty"`
}

// ResponsesAPIResponse is what codex expects back (non-streaming).
type ResponsesAPIResponse struct {
	ID     string         `json:"id"`
	Object string         `json:"object"`
	Status string         `json:"status"`
	Output []OutputItem   `json:"output"`
	Model  string         `json:"model,omitempty"`
	Usage  *ResponseUsage `json:"usage,omitempty"`
}

// OutputItem is a single output element.
type OutputItem struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Role    string          `json:"role,omitempty"`
	Content []OutputContent `json:"content"`
	Status  string          `json:"status,omitempty"`
}

// OutputContent is a content part in an output item.
type OutputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ResponseUsage tracks token usage in Responses API format.
type ResponseUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// --- Chat Completions types (what providers expect) ---

// ChatRequest is the Chat Completions API request body.
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream"`
	Temperature *float64      `json:"temperature,omitempty"`
	TopP        *float64      `json:"top_p,omitempty"`
}

// ChatMessage is a single message in the Chat Completions format.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Source  string `json:"-"`
}

// ChatCompletionResponse is the non-streaming Chat Completions response.
type ChatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

// Choice is a single completion choice.
type Choice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// Usage tracks token usage in Chat Completions format.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// --- Streaming types ---

// ChatChunk is a single streaming chunk from Chat Completions.
type ChatChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Model   string        `json:"model"`
	Choices []ChunkChoice `json:"choices"`
	Usage   *Usage        `json:"usage,omitempty"`
}

// ChunkChoice is a choice in a streaming chunk.
type ChunkChoice struct {
	Index        int        `json:"index"`
	Delta        ChunkDelta `json:"delta"`
	FinishReason *string    `json:"finish_reason"`
}

// ChunkDelta is the incremental content in a streaming chunk.
type ChunkDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// SSEEvent is a Server-Sent Event with named event type.
type SSEEvent struct {
	Event string
	Data  string
}

// Format returns the SSE wire format.
func (e SSEEvent) Format() string {
	return fmt.Sprintf("event: %s\ndata: %s\n\n", e.Event, e.Data)
}

// --- Translation functions ---

// TranslateRequest converts a Responses API request to a Chat Completions request.
func TranslateRequest(req *ResponsesRequest) (*ChatRequest, error) {
	msgs, err := ExtractRequestMessages(req)
	if err != nil {
		return nil, err
	}
	return BuildChatRequest(req, msgs)
}

// extractContent handles content that is either a plain string or an array of ContentParts.
func extractContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}

	// try string first
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}

	// try array of content parts
	var parts []ContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", fmt.Errorf("content is neither string nor array: %w", err)
	}

	var texts []string
	for _, p := range parts {
		switch p.Type {
		case "input_text", "text", "output_text":
			if p.Text != "" {
				texts = append(texts, p.Text)
			}
		}
	}
	return strings.Join(texts, "\n"), nil
}

// TranslateResponse converts a Chat Completions response to a Responses API response.
func TranslateResponse(chatResp *ChatCompletionResponse) *ResponsesAPIResponse {
	resp := &ResponsesAPIResponse{
		ID:     "resp_" + chatResp.ID,
		Object: "response",
		Status: "completed",
		Model:  chatResp.Model,
	}

	for _, choice := range chatResp.Choices {
		resp.Output = append(resp.Output, OutputItem{
			Type: "message",
			ID:   "msg_" + generateID(),
			Role: "assistant",
			Content: []OutputContent{
				{Type: "output_text", Text: choice.Message.Content},
			},
			Status: "completed",
		})
	}

	if chatResp.Usage != nil {
		resp.Usage = &ResponseUsage{
			InputTokens:  chatResp.Usage.PromptTokens,
			OutputTokens: chatResp.Usage.CompletionTokens,
			TotalTokens:  chatResp.Usage.TotalTokens,
		}
	}

	return resp
}

// --- Stream translator ---

// StreamTranslator converts a sequence of Chat Completion chunks into Responses API SSE events.
type StreamTranslator struct {
	responseID string
	model      string
	fullText   strings.Builder
	started    bool
}

// NewStreamTranslator creates a translator for one streaming response.
func NewStreamTranslator() *StreamTranslator {
	return &StreamTranslator{
		responseID: "resp_" + generateID(),
	}
}

// TranslateChunk converts one chat completion chunk into Responses API SSE events.
// Returns the events and whether the stream is done.
func (st *StreamTranslator) TranslateChunk(chunk *ChatChunk) ([]SSEEvent, bool) {
	var events []SSEEvent

	if !st.started {
		st.started = true
		st.model = chunk.Model

		events = append(events,
			st.sseJSON("response.created", map[string]any{
				"id": st.responseID, "object": "response",
				"status": "in_progress", "model": st.model,
			}),
			st.sseJSON("response.output_item.added", map[string]any{
				"type": "message", "role": "assistant",
				"content": []any{}, "status": "in_progress",
			}),
			st.sseJSON("response.content_part.added", map[string]any{
				"type": "output_text", "text": "",
			}),
		)
	}

	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			st.fullText.WriteString(choice.Delta.Content)
			events = append(events, st.sseJSON("response.output_text.delta", map[string]any{
				"type": "output_text", "delta": choice.Delta.Content,
			}))
		}

		if choice.FinishReason != nil && *choice.FinishReason != "" {
			text := st.fullText.String()
			var usage any
			if chunk.Usage != nil {
				usage = map[string]int{
					"input_tokens":  chunk.Usage.PromptTokens,
					"output_tokens": chunk.Usage.CompletionTokens,
					"total_tokens":  chunk.Usage.TotalTokens,
				}
			}

			events = append(events,
				st.sseJSON("response.output_text.done", map[string]any{
					"type": "output_text", "text": text,
				}),
				st.sseJSON("response.content_part.done", map[string]any{
					"type": "output_text", "text": text,
				}),
				st.sseJSON("response.output_item.done", map[string]any{
					"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": text}},
					"status":  "completed",
				}),
				st.sseJSON("response.completed", map[string]any{
					"id": st.responseID, "object": "response",
					"status": "completed", "model": st.model,
					"output": []any{map[string]any{
						"type": "message", "role": "assistant",
						"content": []any{map[string]any{"type": "output_text", "text": text}},
						"status":  "completed",
					}},
					"usage": usage,
				}),
			)
			return events, true
		}
	}

	return events, false
}

func (st *StreamTranslator) sseJSON(event string, data any) SSEEvent {
	b, _ := json.Marshal(data)
	return SSEEvent{Event: event, Data: string(b)}
}

func generateID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
