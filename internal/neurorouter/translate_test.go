package neurorouter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTranslateRequest_Basic(t *testing.T) {
	req := &ResponsesRequest{
		Model:           "deepseek-chat",
		Instructions:    "you are a helper",
		MaxOutputTokens: 1000,
		Input: []InputItem{
			{Type: "message", Role: "user", Content: json.RawMessage(`"hello"`)},
		},
	}

	chat, err := TranslateRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chat.Model != "deepseek-chat" {
		t.Fatalf("model: got %s", chat.Model)
	}
	if chat.MaxTokens != 1000 {
		t.Fatalf("max_tokens: got %d", chat.MaxTokens)
	}
	if len(chat.Messages) != 2 {
		t.Fatalf("messages: got %d, want 2", len(chat.Messages))
	}
	if chat.Messages[0].Role != "system" || chat.Messages[0].Content != "you are a helper" {
		t.Fatalf("system message: %+v", chat.Messages[0])
	}
	if chat.Messages[1].Role != "user" || chat.Messages[1].Content != "hello" {
		t.Fatalf("user message: %+v", chat.Messages[1])
	}
}

func TestTranslateRequest_DeveloperRole(t *testing.T) {
	req := &ResponsesRequest{
		Model: "test",
		Input: []InputItem{
			{Type: "message", Role: "developer", Content: json.RawMessage(`"dev instructions"`)},
			{Type: "message", Role: "user", Content: json.RawMessage(`"question"`)},
		},
	}

	chat, err := TranslateRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chat.Messages[0].Role != "system" {
		t.Fatalf("developer should map to system, got %s", chat.Messages[0].Role)
	}
}

func TestTranslateRequest_ArrayContent(t *testing.T) {
	content := `[{"type":"input_text","text":"part1"},{"type":"input_text","text":"part2"}]`
	req := &ResponsesRequest{
		Model: "test",
		Input: []InputItem{
			{Type: "message", Role: "user", Content: json.RawMessage(content)},
		},
	}

	chat, err := TranslateRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chat.Messages[0].Content != "part1\npart2" {
		t.Fatalf("content: got %q", chat.Messages[0].Content)
	}
}

func TestTranslateRequest_NonMessageItemsSkipped(t *testing.T) {
	req := &ResponsesRequest{
		Model: "test",
		Input: []InputItem{
			{Type: "reasoning", Content: json.RawMessage(`"thinking..."`)},
			{Type: "message", Role: "user", Content: json.RawMessage(`"hello"`)},
			{Type: "shell_call_output", Content: json.RawMessage(`"output"`)},
		},
	}

	chat, err := TranslateRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chat.Messages) != 1 {
		t.Fatalf("messages: got %d, want 1", len(chat.Messages))
	}
}

func TestExtractRequestMessages_PreservesMessageSources(t *testing.T) {
	req := &ResponsesRequest{
		Instructions: "sys",
		Input: []InputItem{
			{Type: "message", Role: "developer", Content: json.RawMessage(`"dev"`)},
			{Type: "shell_call_output", Content: json.RawMessage(`"stdout"`)},
			{Type: "message", Role: "user", Content: json.RawMessage(`"hello"`)},
		},
	}

	msgs, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("messages: got %d, want 3", len(msgs))
	}
	if msgs[0].Source != instructionMessageSource {
		t.Fatalf("instructions source: got %q", msgs[0].Source)
	}
	if msgs[1].Role != "system" || msgs[1].Source != "input:0" {
		t.Fatalf("developer source: %+v", msgs[1])
	}
	if msgs[2].Source != "input:2" {
		t.Fatalf("user source: %+v", msgs[2])
	}
}

func TestTranslateRequest_NoMessages(t *testing.T) {
	req := &ResponsesRequest{
		Model: "test",
		Input: []InputItem{
			{Type: "reasoning", Content: json.RawMessage(`"thinking"`)},
		},
	}

	_, err := TranslateRequest(req)
	if err == nil {
		t.Fatal("expected error for no messages")
	}
}

func TestRewriteResponsesRequest_PreservesNonMessageItems(t *testing.T) {
	rawBody := []byte(`{
		"model":"gpt-5.4",
		"instructions":"shared system",
		"input":[
			{"type":"message","role":"developer","content":"shared system"},
			{"type":"shell_call_output","call_id":"call_123","output":"stdout","status":"completed"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"},{"type":"input_image","image_url":"file://image.png"}]}
		],
		"metadata":{"trace_id":"abc"}
	}`)

	req := &ResponsesRequest{
		Instructions: "shared system",
		Input: []InputItem{
			{Type: "message", Role: "developer", Content: json.RawMessage(`"shared system"`)},
			{Type: "shell_call_output"},
			{Type: "message", Role: "user", Content: json.RawMessage(`[{"type":"input_text","text":"hello"},{"type":"input_image","image_url":"file://image.png"}]`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	processed := []ChatMessage{
		{Role: "system", Content: "shared system", Source: "input:0"},
		{Role: "user", Content: "hello cleaned", Source: "input:2"},
	}

	rewritten, err := RewriteResponsesRequest(rawBody, original, processed)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(rewritten, &result); err != nil {
		t.Fatalf("decode rewritten: %v", err)
	}
	if _, ok := result["instructions"]; ok {
		t.Fatal("instructions should have been removed after duplicate-system filtering")
	}
	if result["metadata"].(map[string]any)["trace_id"] != "abc" {
		t.Fatalf("metadata lost: %+v", result["metadata"])
	}

	input := result["input"].([]any)
	if len(input) != 3 {
		t.Fatalf("input length: got %d, want 3", len(input))
	}
	shellOutput := input[1].(map[string]any)
	if shellOutput["type"] != "shell_call_output" || shellOutput["call_id"] != "call_123" {
		t.Fatalf("shell_call_output changed: %+v", shellOutput)
	}

	user := input[2].(map[string]any)
	content := user["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content length: got %d, want 2", len(content))
	}
	if content[0].(map[string]any)["text"] != "hello cleaned" {
		t.Fatalf("user text: %+v", content[0])
	}
	if content[1].(map[string]any)["type"] != "input_image" {
		t.Fatalf("non-text part lost: %+v", content[1])
	}
}

func TestRewriteResponsesRequestWithConfig_StripsStructuredStaleReadsAndOrphanedOutputs(t *testing.T) {
	rawBody := []byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"function_call","call_id":"call_read_1","name":"Read","arguments":"{\"file_path\":\"/repo/README.md\"}"},
			{"type":"function_call_output","call_id":"call_read_1","output":"stale output"},
			{"type":"function_call_output","call_id":"call_missing","output":"orphaned output"},
			{"type":"function_call","call_id":"call_read_2","name":"Read","arguments":"{\"file_path\":\"/repo/README.md\"}"},
			{"type":"function_call_output","call_id":"call_read_2","output":"fresh output"},
			{"type":"message","role":"user","content":"summarize the repo"}
		]
	}`)

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "function_call"},
			{Type: "function_call_output"},
			{Type: "function_call_output"},
			{Type: "function_call"},
			{Type: "function_call_output"},
			{Type: "message", Role: "user", Content: json.RawMessage(`"summarize the repo"`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		StaleReads:      true,
		OrphanedResults: true,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if got, want := strings.Join(result.FiltersRun, ","), "stale_reads,orphaned_results"; got != want {
		t.Fatalf("filters run: got %q, want %q", got, want)
	}
	if result.BytesBefore <= result.BytesAfter {
		t.Fatalf("expected bytes to shrink, got before=%d after=%d", result.BytesBefore, result.BytesAfter)
	}

	var rewritten map[string]any
	if err := json.Unmarshal(result.Body, &rewritten); err != nil {
		t.Fatalf("decode rewritten: %v", err)
	}
	input := rewritten["input"].([]any)
	if len(input) != 3 {
		t.Fatalf("input length: got %d, want 3", len(input))
	}
	if input[0].(map[string]any)["call_id"] != "call_read_2" {
		t.Fatalf("expected latest read to remain, got %+v", input[0])
	}
	if input[1].(map[string]any)["call_id"] != "call_read_2" {
		t.Fatalf("expected latest read output to remain, got %+v", input[1])
	}
	if input[2].(map[string]any)["type"] != "message" {
		t.Fatalf("user message should remain, got %+v", input[2])
	}
}

func TestRewriteResponsesRequestWithConfig_StripsStructuredStaleSearchChains(t *testing.T) {
	rawBody := []byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"tool_search_call","call_id":"search_call_1","execution":"search","arguments":{"query":"codex openai base url","provider":"docs"}},
			{"type":"tool_search_output","call_id":"search_call_1","status":"completed","execution":"search","tools":[{"title":"Old result"}]},
			{"type":"tool_search_call","call_id":"search_call_2","execution":"search","arguments":{"provider":"docs","query":"codex openai base url"}},
			{"type":"tool_search_output","call_id":"search_call_2","status":"completed","execution":"search","tools":[{"title":"Fresh result"}]},
			{"type":"message","role":"user","content":"Summarize the docs findings."}
		]
	}`)

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "tool_search_call"},
			{Type: "tool_search_output"},
			{Type: "tool_search_call"},
			{Type: "tool_search_output"},
			{Type: "message", Role: "user", Content: json.RawMessage(`"Summarize the docs findings."`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		StaleReads: true,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if got, want := strings.Join(result.FiltersRun, ","), "stale_reads"; got != want {
		t.Fatalf("filters run: got %q, want %q", got, want)
	}
	if result.BytesBefore <= result.BytesAfter {
		t.Fatalf("expected bytes to shrink, got before=%d after=%d", result.BytesBefore, result.BytesAfter)
	}

	var rewritten map[string]any
	if err := json.Unmarshal(result.Body, &rewritten); err != nil {
		t.Fatalf("decode rewritten: %v", err)
	}
	input := rewritten["input"].([]any)
	if len(input) != 3 {
		t.Fatalf("input length: got %d, want 3", len(input))
	}
	if input[0].(map[string]any)["call_id"] != "search_call_2" {
		t.Fatalf("expected latest search call to remain, got %+v", input[0])
	}
	if input[1].(map[string]any)["call_id"] != "search_call_2" {
		t.Fatalf("expected latest search output to remain, got %+v", input[1])
	}
	if input[2].(map[string]any)["type"] != "message" {
		t.Fatalf("user message should remain, got %+v", input[2])
	}
}

func TestTranslateRequest_FieldMapping(t *testing.T) {
	temp := 0.7
	topP := 0.9
	req := &ResponsesRequest{
		Model:           "test",
		MaxOutputTokens: 2048,
		Temperature:     &temp,
		TopP:            &topP,
		Stream:          true,
		Input: []InputItem{
			{Type: "message", Role: "user", Content: json.RawMessage(`"hi"`)},
		},
	}

	chat, err := TranslateRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chat.MaxTokens != 2048 {
		t.Fatalf("max_tokens: got %d", chat.MaxTokens)
	}
	if *chat.Temperature != 0.7 {
		t.Fatalf("temperature: got %f", *chat.Temperature)
	}
	if *chat.TopP != 0.9 {
		t.Fatalf("top_p: got %f", *chat.TopP)
	}
	if !chat.Stream {
		t.Fatal("stream should be true")
	}
}

func TestTranslateResponse_Basic(t *testing.T) {
	chatResp := &ChatCompletionResponse{
		ID:    "chatcmpl-abc",
		Model: "deepseek-chat",
		Choices: []Choice{
			{Index: 0, Message: ChatMessage{Role: "assistant", Content: "hello back"}},
		},
		Usage: &Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}

	resp := TranslateResponse(chatResp)
	if resp.Object != "response" {
		t.Fatalf("object: got %s", resp.Object)
	}
	if resp.Status != "completed" {
		t.Fatalf("status: got %s", resp.Status)
	}
	if len(resp.Output) != 1 {
		t.Fatalf("output: got %d items", len(resp.Output))
	}
	if resp.Output[0].Content[0].Text != "hello back" {
		t.Fatalf("text: got %q", resp.Output[0].Content[0].Text)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 {
		t.Fatalf("usage: %+v", resp.Usage)
	}
}

func TestStreamTranslator_FullSequence(t *testing.T) {
	st := NewStreamTranslator()
	stop := "stop"

	// first chunk with role
	events1, done1 := st.TranslateChunk(&ChatChunk{
		ID: "c1", Model: "deepseek-chat",
		Choices: []ChunkChoice{{Delta: ChunkDelta{Role: "assistant", Content: "hello"}}},
	})
	if done1 {
		t.Fatal("should not be done after first chunk")
	}
	// should have: response.created, output_item.added, content_part.added, text.delta
	if len(events1) != 4 {
		t.Fatalf("first chunk events: got %d, want 4", len(events1))
	}
	if events1[0].Event != "response.created" {
		t.Fatalf("first event: got %s", events1[0].Event)
	}
	if events1[3].Event != "response.output_text.delta" {
		t.Fatalf("fourth event: got %s", events1[3].Event)
	}

	// middle chunk
	events2, done2 := st.TranslateChunk(&ChatChunk{
		ID: "c2", Model: "deepseek-chat",
		Choices: []ChunkChoice{{Delta: ChunkDelta{Content: " world"}}},
	})
	if done2 {
		t.Fatal("should not be done")
	}
	if len(events2) != 1 || events2[0].Event != "response.output_text.delta" {
		t.Fatalf("middle chunk: got %d events", len(events2))
	}

	// final chunk with finish_reason
	events3, done3 := st.TranslateChunk(&ChatChunk{
		ID: "c3", Model: "deepseek-chat",
		Choices: []ChunkChoice{{Delta: ChunkDelta{}, FinishReason: &stop}},
		Usage:   &Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
	})
	if !done3 {
		t.Fatal("should be done")
	}
	// should have: text.done, content_part.done, output_item.done, response.completed
	if len(events3) != 4 {
		t.Fatalf("final chunk events: got %d, want 4", len(events3))
	}
	if events3[3].Event != "response.completed" {
		t.Fatalf("last event: got %s", events3[3].Event)
	}

	// verify full text is in completed event
	var completed map[string]any
	if err := json.Unmarshal([]byte(events3[3].Data), &completed); err != nil {
		t.Fatalf("parse completed: %v", err)
	}
	output := completed["output"].([]any)
	msg := output[0].(map[string]any)
	content := msg["content"].([]any)
	part := content[0].(map[string]any)
	if part["text"] != "hello world" {
		t.Fatalf("full text: got %q", part["text"])
	}
}

func TestStreamTranslator_EmptyContent(t *testing.T) {
	st := NewStreamTranslator()

	// chunk with empty content should still emit preamble but no text delta
	events, done := st.TranslateChunk(&ChatChunk{
		ID: "c1", Model: "test",
		Choices: []ChunkChoice{{Delta: ChunkDelta{Role: "assistant"}}},
	})
	if done {
		t.Fatal("should not be done")
	}
	// preamble events only (created, item.added, content_part.added), no text delta
	if len(events) != 3 {
		t.Fatalf("events: got %d, want 3", len(events))
	}
}
