package neurorouter

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func largeFunctionToolCatalog(count int) []map[string]any {
	tools := make([]map[string]any, 0, count)
	for i := 0; i < count; i++ {
		tools = append(tools, map[string]any{
			"type":        "function",
			"name":        "tool_" + strconv.Itoa(i),
			"description": strings.Repeat("This tool handles a long operational policy and safety guidance block. ", 32),
			"parameters": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"mode", "target"},
				"properties": map[string]any{
					"mode": map[string]any{
						"type":        "string",
						"title":       "Mode",
						"description": strings.Repeat("Select the execution mode for this tool call. ", 16),
						"enum":        []string{"fast", "safe", "exact"},
					},
					"target": map[string]any{
						"type":        "string",
						"title":       "Target",
						"description": strings.Repeat("Provide the primary target path or identifier for the tool call. ", 16),
					},
				},
			},
		})
	}
	return tools
}

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

func TestRewriteResponsesRequestWithConfig_DropsSupersededStructuredOutputsForSameCallID(t *testing.T) {
	rawBody := []byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"function_call","call_id":"call_build","name":"Read","arguments":"{\"file_path\":\"/repo/README.md\"}"},
			{"type":"function_call_output","call_id":"call_build","output":"stale output"},
			{"type":"function_call_output","call_id":"call_build","output":"fresh output"},
			{"type":"custom_tool_call","call_id":"call_shell","name":"shell","input":"git status"},
			{"type":"custom_tool_call_output","call_id":"call_shell","output":"clean"},
			{"type":"message","role":"user","content":"continue"}
		]
	}`)

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "function_call"},
			{Type: "function_call_output"},
			{Type: "function_call_output"},
			{Type: "custom_tool_call"},
			{Type: "custom_tool_call_output"},
			{Type: "message", Role: "user", Content: json.RawMessage(`"continue"`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		OrphanedResults: true,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if got, want := strings.Join(result.FiltersRun, ","), "orphaned_results"; got != want {
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
	if len(input) != 5 {
		t.Fatalf("input length: got %d, want 5", len(input))
	}
	if input[0].(map[string]any)["call_id"] != "call_build" {
		t.Fatalf("expected function call to remain, got %+v", input[0])
	}
	if input[1].(map[string]any)["call_id"] != "call_build" {
		t.Fatalf("expected latest function output to remain, got %+v", input[1])
	}
	if input[1].(map[string]any)["output"] != "fresh output" {
		t.Fatalf("expected latest function output payload, got %+v", input[1])
	}
	if input[2].(map[string]any)["call_id"] != "call_shell" {
		t.Fatalf("expected distinct custom tool call to remain, got %+v", input[2])
	}
	if input[3].(map[string]any)["call_id"] != "call_shell" {
		t.Fatalf("expected distinct custom tool output to remain, got %+v", input[3])
	}
	if input[4].(map[string]any)["type"] != "message" {
		t.Fatalf("user message should remain, got %+v", input[4])
	}
}

func TestRewriteResponsesRequestWithConfig_DropsDuplicateCompactionSummaries(t *testing.T) {
	summaryA := codexCompactionSummaryPrefix + "\nSummary A"
	summaryB := codexCompactionSummaryPrefix + "\nSummary B"
	summaryARaw, err := json.Marshal([]map[string]any{{"type": "input_text", "text": summaryA}})
	if err != nil {
		t.Fatalf("marshal summary A content: %v", err)
	}
	summaryBRaw, err := json.Marshal([]map[string]any{{"type": "input_text", "text": summaryB}})
	if err != nil {
		t.Fatalf("marshal summary B content: %v", err)
	}
	rawBody, err := json.Marshal(map[string]any{
		"model": "gpt-5.4",
		"input": []map[string]any{
			{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": summaryA}}},
			{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": summaryA}}},
			{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": summaryB}}},
			{"type": "message", "role": "user", "content": "continue"},
		},
	})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "message", Role: "user", Content: summaryARaw},
			{Type: "message", Role: "user", Content: summaryARaw},
			{Type: "message", Role: "user", Content: summaryBRaw},
			{Type: "message", Role: "user", Content: json.RawMessage(`"continue"`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		SystemReminders: true,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if got, want := strings.Join(result.FiltersRun, ","), "system_reminders"; got != want {
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
	gotSummaryA := input[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"]
	if gotSummaryA != summaryA {
		t.Fatalf("expected latest duplicate summary to remain, got %+v", input[0])
	}
	gotSummaryB := input[1].(map[string]any)["content"].([]any)[0].(map[string]any)["text"]
	if gotSummaryB != summaryB {
		t.Fatalf("expected distinct summary to remain, got %+v", input[1])
	}
	if input[2].(map[string]any)["content"] != "continue" {
		t.Fatalf("expected user message to remain, got %+v", input[2])
	}
}

func TestRewriteResponsesRequestWithConfig_KeepsDistinctCompactionSummaries(t *testing.T) {
	summaryA := codexCompactionSummaryPrefix + "\nSummary A"
	summaryB := codexCompactionSummaryPrefix + "\nSummary B"
	summaryARaw, err := json.Marshal([]map[string]any{{"type": "input_text", "text": summaryA}})
	if err != nil {
		t.Fatalf("marshal summary A content: %v", err)
	}
	summaryBRaw, err := json.Marshal([]map[string]any{{"type": "input_text", "text": summaryB}})
	if err != nil {
		t.Fatalf("marshal summary B content: %v", err)
	}
	rawBody, err := json.Marshal(map[string]any{
		"model": "gpt-5.4",
		"input": []map[string]any{
			{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": summaryA}}},
			{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": summaryB}}},
			{"type": "message", "role": "user", "content": "continue"},
		},
	})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "message", Role: "user", Content: summaryARaw},
			{Type: "message", Role: "user", Content: summaryBRaw},
			{Type: "message", Role: "user", Content: json.RawMessage(`"continue"`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		SystemReminders: true,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if got := strings.Join(result.FiltersRun, ","); got != "" {
		t.Fatalf("filters run: got %q, want none", got)
	}
	if result.BytesBefore != result.BytesAfter {
		t.Fatalf("expected no rewrite size change, got before=%d after=%d", result.BytesBefore, result.BytesAfter)
	}

	var rewritten map[string]any
	if err := json.Unmarshal(result.Body, &rewritten); err != nil {
		t.Fatalf("decode rewritten: %v", err)
	}
	input := rewritten["input"].([]any)
	if len(input) != 3 {
		t.Fatalf("input length: got %d, want 3", len(input))
	}
}

func TestRewriteResponsesRequestWithConfig_BudgetsCompactionBoundaryHistory(t *testing.T) {
	oldPrompt := strings.Repeat("audit prompt line\n", 5000)
	oldAnswer := strings.Repeat("summary line\n", 5000)
	lastPrompt := strings.Repeat("verify line\n", 400)
	postCompact := "Post-compaction delta."
	rawBody := []byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"message","role":"user","content":` + strconv.Quote(oldPrompt) + `},
			{"type":"message","role":"assistant","content":` + strconv.Quote(oldAnswer) + `},
			{"type":"compaction","encrypted_content":"opaque-old"},
			{"type":"message","role":"developer","content":"Preserve these repo guardrails."},
			{"type":"message","role":"user","content":` + strconv.Quote(lastPrompt) + `},
			{"type":"compaction","encrypted_content":"opaque-current"},
			{"type":"message","role":"assistant","content":` + strconv.Quote(postCompact) + `}
		]
	}`)

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "message", Role: "user", Content: json.RawMessage(strconv.Quote(oldPrompt))},
			{Type: "message", Role: "assistant", Content: json.RawMessage(strconv.Quote(oldAnswer))},
			{Type: "compaction"},
			{Type: "message", Role: "developer", Content: json.RawMessage(`"Preserve these repo guardrails."`)},
			{Type: "message", Role: "user", Content: json.RawMessage(strconv.Quote(lastPrompt))},
			{Type: "compaction"},
			{Type: "message", Role: "assistant", Content: json.RawMessage(strconv.Quote(postCompact))},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		OversizedBlocks: true,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if got, want := strings.Join(result.FiltersRun, ","), "oversized_blocks"; got != want {
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
	if len(input) != 4 {
		t.Fatalf("input length: got %d, want 4", len(input))
	}
	if got := input[0].(map[string]any)["role"]; got != "developer" {
		t.Fatalf("expected developer message to remain, got %+v", input[0])
	}
	if got := input[1].(map[string]any)["content"]; got != lastPrompt {
		t.Fatalf("expected latest pre-compaction prompt to remain, got %+v", input[1])
	}
	if got := input[2].(map[string]any)["encrypted_content"]; got != "opaque-current" {
		t.Fatalf("expected latest compaction item to remain, got %+v", input[2])
	}
	if got := input[3].(map[string]any)["content"]; got != postCompact {
		t.Fatalf("expected post-compaction delta to remain, got %+v", input[3])
	}
}

func TestRewriteResponsesRequestWithConfig_BudgetsHistoricalReasoningWithPreviousResponseID(t *testing.T) {
	inputJSON := make([]string, 0, 7)
	for i := 0; i < 6; i++ {
		inputJSON = append(inputJSON, testReasoningInputJSON(strings.Repeat("r", 5000), i))
	}
	inputJSON = append(inputJSON, `{"type":"message","role":"user","content":"Continue."}`)

	rawBody := []byte("{\n" +
		`  "model":"gpt-5.4",` + "\n" +
		`  "previous_response_id":"resp_prev_123",` + "\n" +
		`  "input":[` + strings.Join(inputJSON, ",") + "]\n" +
		"}")

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "reasoning"},
			{Type: "reasoning"},
			{Type: "reasoning"},
			{Type: "reasoning"},
			{Type: "reasoning"},
			{Type: "reasoning"},
			{Type: "message", Role: "user", Content: json.RawMessage(`"Continue."`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		OversizedBlocks: true,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if got, want := strings.Join(result.FiltersRun, ","), "oversized_blocks"; got != want {
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
	reasoningCount := 0
	for _, item := range input {
		if item.(map[string]any)["type"] == "reasoning" {
			reasoningCount++
		}
	}
	if reasoningCount != defaultStructuredReasoningKeepRecent {
		t.Fatalf("reasoning count: got %d, want %d", reasoningCount, defaultStructuredReasoningKeepRecent)
	}
	if input[len(input)-1].(map[string]any)["content"] != "Continue." {
		t.Fatalf("expected trailing user message to remain, got %+v", input[len(input)-1])
	}
}

func TestRewriteResponsesRequestWithConfig_BudgetsHistoricalReasoningAfterCompaction(t *testing.T) {
	rawBody := []byte("{\n" +
		`  "model":"gpt-5.4",` + "\n" +
		`  "input":[` +
		testReasoningInputJSON(strings.Repeat("r", 5000), 0) + `,` +
		testReasoningInputJSON(strings.Repeat("s", 5000), 1) + `,` +
		`{"type":"compaction","encrypted_content":"opaque-current"},` +
		testReasoningInputJSON(strings.Repeat("t", 5000), 2) + `,` +
		`{"type":"message","role":"assistant","content":"Post-compaction delta."}` +
		"]\n" +
		"}")

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "reasoning"},
			{Type: "reasoning"},
			{Type: "compaction"},
			{Type: "reasoning"},
			{Type: "message", Role: "assistant", Content: json.RawMessage(`"Post-compaction delta."`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		OversizedBlocks: true,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if got, want := strings.Join(result.FiltersRun, ","), "oversized_blocks"; got != want {
		t.Fatalf("filters run: got %q, want %q", got, want)
	}

	var rewritten map[string]any
	if err := json.Unmarshal(result.Body, &rewritten); err != nil {
		t.Fatalf("decode rewritten: %v", err)
	}
	input := rewritten["input"].([]any)
	reasoningCount := 0
	for _, item := range input {
		if item.(map[string]any)["type"] == "reasoning" {
			reasoningCount++
		}
	}
	if reasoningCount != defaultStructuredReasoningKeepRecent {
		t.Fatalf("reasoning count: got %d, want %d", reasoningCount, defaultStructuredReasoningKeepRecent)
	}
}

func TestRewriteResponsesRequestWithConfig_KeepsReasoningWithoutContinuityGate(t *testing.T) {
	inputJSON := make([]string, 0, 5)
	for i := 0; i < 4; i++ {
		inputJSON = append(inputJSON, testReasoningInputJSON(strings.Repeat("r", 5000), i))
	}
	inputJSON = append(inputJSON, `{"type":"message","role":"user","content":"Continue."}`)

	rawBody := []byte("{\n" +
		`  "model":"gpt-5.4",` + "\n" +
		`  "input":[` + strings.Join(inputJSON, ",") + "]\n" +
		"}")

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "reasoning"},
			{Type: "reasoning"},
			{Type: "reasoning"},
			{Type: "reasoning"},
			{Type: "message", Role: "user", Content: json.RawMessage(`"Continue."`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		OversizedBlocks: true,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if len(result.FiltersRun) != 0 {
		t.Fatalf("expected no reasoning cleanup without continuity gate, got %v", result.FiltersRun)
	}
	if result.BytesBefore != result.BytesAfter {
		t.Fatalf("expected no rewrite size change, got before=%d after=%d", result.BytesBefore, result.BytesAfter)
	}
}

func TestRewriteResponsesRequestWithConfig_PreservesRawBodyWhenUnchanged(t *testing.T) {
	rawBody := []byte("{\n  \"model\":\"gpt-5.4\",\n  \"input\":[{\"role\":\"user\",\"type\":\"message\",\"content\":\"hello\"},{\"type\":\"shell_call_output\",\"call_id\":\"call_123\",\"output\":\"stdout\",\"status\":\"completed\"}]\n}")

	req := &ResponsesRequest{
		Model: "gpt-5.4",
		Input: []InputItem{
			{Type: "message", Role: "user", Content: json.RawMessage(`"hello"`)},
			{Type: "shell_call_output"},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if result.BytesBefore != len(rawBody) || result.BytesAfter != len(rawBody) {
		t.Fatalf("expected raw byte counts to be preserved, got before=%d after=%d want=%d", result.BytesBefore, result.BytesAfter, len(rawBody))
	}
	if string(result.Body) != string(rawBody) {
		t.Fatalf("expected unchanged raw body to be preserved, got %q", string(result.Body))
	}
	if got := strings.Join(result.FiltersRun, ","); got != "" {
		t.Fatalf("filters run: got %q, want none", got)
	}
}

func TestRewriteResponsesRequestWithConfig_NormalizesRequestTextPartTypesByRoleWhenUnchanged(t *testing.T) {
	templateBody := `{
		"model":"gpt-5.4",
		"input":[
			{"type":"message","role":"ROLE","content":[
				{"type":"PART_TYPE","text":"hello"},
				{"type":"input_image","image_url":"file://image.png"}
			]}
		]
	}`
	templateContent := `[{"type":"PART_TYPE","text":"hello"},{"type":"input_image","image_url":"file://image.png"}]`

	cases := []struct {
		name     string
		role     string
		partType string
		wantType string
	}{
		{name: "user_text", role: "user", partType: "text", wantType: "input_text"},
		{name: "user_output_text", role: "user", partType: "output_text", wantType: "input_text"},
		{name: "assistant_text", role: "assistant", partType: "text", wantType: "output_text"},
		{name: "assistant_input_text", role: "assistant", partType: "input_text", wantType: "output_text"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rawBody := []byte(strings.NewReplacer("ROLE", tc.role, "PART_TYPE", tc.partType).Replace(templateBody))
			content := strings.ReplaceAll(templateContent, "PART_TYPE", tc.partType)

			req := &ResponsesRequest{
				Model: "gpt-5.4",
				Input: []InputItem{
					{Type: "message", Role: tc.role, Content: json.RawMessage(content)},
				},
			}

			original, err := ExtractRequestMessages(req)
			if err != nil {
				t.Fatalf("extract original: %v", err)
			}

			result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{})
			if err != nil {
				t.Fatalf("rewrite: %v", err)
			}

			if string(result.Body) == string(rawBody) {
				t.Fatal("expected rewritten body to normalize text part type")
			}
			if got := strings.Join(result.FiltersRun, ","); got != "" {
				t.Fatalf("filters run: got %q, want none", got)
			}

			var rewritten map[string]any
			if err := json.Unmarshal(result.Body, &rewritten); err != nil {
				t.Fatalf("decode rewritten: %v", err)
			}

			input := rewritten["input"].([]any)
			contentParts := input[0].(map[string]any)["content"].([]any)
			first := contentParts[0].(map[string]any)
			if first["type"] != tc.wantType {
				t.Fatalf("text part type: got %v, want %s", first["type"], tc.wantType)
			}
			if first["text"] != "hello" {
				t.Fatalf("text part text: got %v, want hello", first["text"])
			}
			if contentParts[1].(map[string]any)["type"] != "input_image" {
				t.Fatalf("non-text part changed: %+v", contentParts[1])
			}
		})
	}
}

func TestRewriteResponsesRequestWithConfig_RewritesAssistantTextPartsAsOutputText(t *testing.T) {
	rawBody := []byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"message","role":"assistant","content":[{"type":"input_text","text":"before"}]}
		]
	}`)

	req := &ResponsesRequest{
		Model: "gpt-5.4",
		Input: []InputItem{
			{Type: "message", Role: "assistant", Content: json.RawMessage(`[{"type":"input_text","text":"before"}]`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	processed := []ChatMessage{{
		Role:    "assistant",
		Content: "after",
		Source:  original[0].Source,
	}}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, processed, FilterConfig{})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	var rewritten map[string]any
	if err := json.Unmarshal(result.Body, &rewritten); err != nil {
		t.Fatalf("decode rewritten: %v", err)
	}

	input := rewritten["input"].([]any)
	contentParts := input[0].(map[string]any)["content"].([]any)
	first := contentParts[0].(map[string]any)
	if first["type"] != "output_text" {
		t.Fatalf("text part type: got %v, want output_text", first["type"])
	}
	if first["text"] != "after" {
		t.Fatalf("text part text: got %v, want after", first["text"])
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

func TestRewriteResponsesRequestWithConfig_CompactsOversizedSearchOutputs(t *testing.T) {
	tools := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		tools = append(tools, `{"title":"Result `+strconv.Itoa(i)+`","url":"https://docs.example/`+strconv.Itoa(i)+`","snippet":"`+strings.Repeat("search result snippet ", 64)+`"}`)
	}
	rawBody := []byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"tool_search_output","call_id":"search_call_1","status":"completed","execution":"search","tools":[` + strings.Join(tools, ",") + `]},
			{"type":"message","role":"user","content":"Summarize the docs findings."}
		]
	}`)

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "tool_search_output"},
			{Type: "message", Role: "user", Content: json.RawMessage(`"Summarize the docs findings."`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		OversizedBlocks: true,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if got, want := strings.Join(result.FiltersRun, ","), "oversized_blocks"; got != want {
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
	compactedTools := input[0].(map[string]any)["tools"].([]any)
	if got, want := len(compactedTools), defaultStructuredSearchToolsKeep; got != want {
		t.Fatalf("tools length: got %d, want %d", got, want)
	}
	if compactedTools[0].(map[string]any)["title"] != "Result 0" {
		t.Fatalf("expected top-ranked result to remain first, got %+v", compactedTools[0])
	}
}

func TestRewriteResponsesRequestWithConfig_KeepsSmallSearchOutputs(t *testing.T) {
	rawBody := []byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"tool_search_output","call_id":"search_call_1","status":"completed","execution":"search","tools":[{"title":"Result 0","url":"https://docs.example/0","snippet":"Short snippet"}]},
			{"type":"message","role":"user","content":"Summarize the docs findings."}
		]
	}`)

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "tool_search_output"},
			{Type: "message", Role: "user", Content: json.RawMessage(`"Summarize the docs findings."`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		OversizedBlocks: true,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if len(result.FiltersRun) != 0 {
		t.Fatalf("expected no compaction, got %v", result.FiltersRun)
	}
	if result.BytesBefore != result.BytesAfter {
		t.Fatalf("expected no rewrite size change, got before=%d after=%d", result.BytesBefore, result.BytesAfter)
	}
}

func TestRewriteResponsesRequestWithConfig_CompactsOversizedTopLevelTools(t *testing.T) {
	body := map[string]any{
		"model": "gpt-5.4",
		"tools": largeFunctionToolCatalog(12),
		"input": []map[string]any{
			{
				"type":    "message",
				"role":    "user",
				"content": "Summarize the docs findings.",
			},
		},
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal raw body: %v", err)
	}

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "message", Role: "user", Content: json.RawMessage(`"Summarize the docs findings."`)},
		},
	}
	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	originalDescription := body["tools"].([]map[string]any)[0]["description"].(string)
	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		OversizedBlocks: true,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if got, want := strings.Join(result.FiltersRun, ","), "oversized_blocks"; got != want {
		t.Fatalf("filters run: got %q, want %q", got, want)
	}
	if result.BytesBefore <= result.BytesAfter {
		t.Fatalf("expected bytes to shrink, got before=%d after=%d", result.BytesBefore, result.BytesAfter)
	}

	var rewritten map[string]any
	if err := json.Unmarshal(result.Body, &rewritten); err != nil {
		t.Fatalf("decode rewritten: %v", err)
	}
	tools := rewritten["tools"].([]any)
	if got, want := len(tools), 12; got != want {
		t.Fatalf("tools length: got %d, want %d", got, want)
	}

	firstTool := tools[0].(map[string]any)
	if got, want := firstTool["name"], "tool_0"; got != want {
		t.Fatalf("tool name: got %v, want %v", got, want)
	}
	description := firstTool["description"].(string)
	if len(description) >= len(originalDescription) {
		t.Fatalf("expected shorter tool description, got %d want < %d", len(description), len(originalDescription))
	}

	parameters := firstTool["parameters"].(map[string]any)
	required := parameters["required"].([]any)
	if got, want := len(required), 2; got != want {
		t.Fatalf("required length: got %d, want %d", got, want)
	}
	properties := parameters["properties"].(map[string]any)
	mode := properties["mode"].(map[string]any)
	enumValues := mode["enum"].([]any)
	if got, want := len(enumValues), 3; got != want {
		t.Fatalf("enum length: got %d, want %d", got, want)
	}
	if _, ok := mode["title"]; ok {
		t.Fatalf("expected schema title to be removed, got %+v", mode)
	}
}

func TestRewriteResponsesRequestWithConfig_KeepsSmallTopLevelTools(t *testing.T) {
	body := map[string]any{
		"model": "gpt-5.4",
		"tools": []map[string]any{
			{
				"type":        "function",
				"name":        "small_tool",
				"description": "Short description.",
				"parameters": map[string]any{
					"type":     "object",
					"required": []string{"target"},
					"properties": map[string]any{
						"target": map[string]any{
							"type":        "string",
							"description": "Short field description.",
						},
					},
				},
			},
		},
		"input": []map[string]any{
			{
				"type":    "message",
				"role":    "user",
				"content": "Summarize the docs findings.",
			},
		},
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal raw body: %v", err)
	}

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "message", Role: "user", Content: json.RawMessage(`"Summarize the docs findings."`)},
		},
	}
	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		OversizedBlocks: true,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if len(result.FiltersRun) != 0 {
		t.Fatalf("expected no compaction, got %v", result.FiltersRun)
	}
	if result.BytesBefore != result.BytesAfter {
		t.Fatalf("expected no rewrite size change, got before=%d after=%d", result.BytesBefore, result.BytesAfter)
	}
}

func TestRewriteResponsesRequestWithConfig_StripsDuplicateShellTranscriptChains(t *testing.T) {
	rawBody := []byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"local_shell_call","call_id":"shell_call_1","status":"completed","action":{"type":"exec","command":["git","status"],"working_directory":"/repo"}},
			{"type":"shell_call_output","call_id":"shell_call_1","output":"On branch main\nnothing to commit\n","status":"completed"},
			{"type":"local_shell_call","call_id":"shell_call_2","status":"completed","action":{"type":"exec","working_directory":"/repo","command":["git","status"]}},
			{"type":"shell_call_output","call_id":"shell_call_2","status":"completed","output":"On branch main\nnothing to commit\n"},
			{"type":"message","role":"user","content":"Continue."}
		]
	}`)

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "local_shell_call"},
			{Type: "shell_call_output"},
			{Type: "local_shell_call"},
			{Type: "shell_call_output"},
			{Type: "message", Role: "user", Content: json.RawMessage(`"Continue."`)},
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
	if input[0].(map[string]any)["call_id"] != "shell_call_2" {
		t.Fatalf("expected latest shell call to remain, got %+v", input[0])
	}
	if input[1].(map[string]any)["call_id"] != "shell_call_2" {
		t.Fatalf("expected latest shell output to remain, got %+v", input[1])
	}
	if input[2].(map[string]any)["type"] != "message" {
		t.Fatalf("user message should remain, got %+v", input[2])
	}
}

func TestRewriteResponsesRequestWithConfig_KeepsDistinctShellTranscriptChains(t *testing.T) {
	rawBody := []byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"local_shell_call","call_id":"shell_call_1","status":"completed","action":{"type":"exec","command":["git","status"],"working_directory":"/repo"}},
			{"type":"shell_call_output","call_id":"shell_call_1","output":"On branch main\nnothing to commit\n","status":"completed"},
			{"type":"local_shell_call","call_id":"shell_call_2","status":"completed","action":{"type":"exec","working_directory":"/repo","command":["git","status"]}},
			{"type":"shell_call_output","call_id":"shell_call_2","output":"On branch feature\nmodified: README.md\n","status":"completed"},
			{"type":"message","role":"user","content":"Continue."}
		]
	}`)

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "local_shell_call"},
			{Type: "shell_call_output"},
			{Type: "local_shell_call"},
			{Type: "shell_call_output"},
			{Type: "message", Role: "user", Content: json.RawMessage(`"Continue."`)},
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

	if len(result.FiltersRun) != 0 {
		t.Fatalf("expected no shell cleanup for distinct outputs, got %v", result.FiltersRun)
	}
	if result.BytesBefore != result.BytesAfter {
		t.Fatalf("expected no rewrite size change, got before=%d after=%d", result.BytesBefore, result.BytesAfter)
	}

	var rewritten map[string]any
	if err := json.Unmarshal(result.Body, &rewritten); err != nil {
		t.Fatalf("decode rewritten: %v", err)
	}
	input := rewritten["input"].([]any)
	if len(input) != 5 {
		t.Fatalf("input length: got %d, want 5", len(input))
	}
}

func TestRewriteResponsesRequestWithConfig_DropsSupersededFailedShellRetries(t *testing.T) {
	rawBody := []byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"local_shell_call","call_id":"shell_call_1","status":"completed","action":{"type":"exec","command":["go","test","./..."],"working_directory":"/repo"}},
			{"type":"shell_call_output","call_id":"shell_call_1","output":"FAIL\t./...\nexit status 1\n","status":"completed","exit_code":1},
			{"type":"local_shell_call","call_id":"shell_call_2","status":"completed","action":{"type":"exec","working_directory":"/repo","command":["go","test","./..."]}},
			{"type":"shell_call_output","call_id":"shell_call_2","output":"ok\t./...\n","status":"completed","exit_code":0},
			{"type":"message","role":"user","content":"Continue."}
		]
	}`)

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "local_shell_call"},
			{Type: "shell_call_output"},
			{Type: "local_shell_call"},
			{Type: "shell_call_output"},
			{Type: "message", Role: "user", Content: json.RawMessage(`"Continue."`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		FailedRetries: true,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if got, want := strings.Join(result.FiltersRun, ","), "failed_retries"; got != want {
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
	if input[0].(map[string]any)["call_id"] != "shell_call_2" {
		t.Fatalf("expected successful retry call to remain, got %+v", input[0])
	}
	if input[1].(map[string]any)["call_id"] != "shell_call_2" {
		t.Fatalf("expected successful retry output to remain, got %+v", input[1])
	}
}

func TestRewriteResponsesRequestWithConfig_KeepsUnresolvedFailedShellRetries(t *testing.T) {
	rawBody := []byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"local_shell_call","call_id":"shell_call_1","status":"completed","action":{"type":"exec","command":["go","test","./..."],"working_directory":"/repo"}},
			{"type":"shell_call_output","call_id":"shell_call_1","output":"FAIL\t./...\nexit status 1\n","status":"completed","exit_code":1},
			{"type":"local_shell_call","call_id":"shell_call_2","status":"completed","action":{"type":"exec","working_directory":"/repo","command":["git","status"]}},
			{"type":"shell_call_output","call_id":"shell_call_2","output":"On branch main\n","status":"completed","exit_code":0},
			{"type":"message","role":"user","content":"Continue."}
		]
	}`)

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "local_shell_call"},
			{Type: "shell_call_output"},
			{Type: "local_shell_call"},
			{Type: "shell_call_output"},
			{Type: "message", Role: "user", Content: json.RawMessage(`"Continue."`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		FailedRetries: true,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if len(result.FiltersRun) != 0 {
		t.Fatalf("expected unresolved failure to remain, got %v", result.FiltersRun)
	}
	if result.BytesBefore != result.BytesAfter {
		t.Fatalf("expected no rewrite size change, got before=%d after=%d", result.BytesBefore, result.BytesAfter)
	}

	var rewritten map[string]any
	if err := json.Unmarshal(result.Body, &rewritten); err != nil {
		t.Fatalf("decode rewritten: %v", err)
	}
	input := rewritten["input"].([]any)
	if len(input) != 5 {
		t.Fatalf("input length: got %d, want 5", len(input))
	}
}

func TestRewriteResponsesRequestWithConfig_TruncatesOversizedShellOutputs(t *testing.T) {
	largeOutput := strings.Repeat("build log line\n", 32) + "fatal: no such file or directory\nexit status 1\n"
	rawBody := []byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"local_shell_call","call_id":"shell_call_1","status":"completed","action":{"type":"exec","command":["go","test","./..."],"working_directory":"/repo"}},
			{"type":"shell_call_output","call_id":"shell_call_1","output":` + strconv.Quote(largeOutput) + `,"status":"completed","exit_code":1},
			{"type":"message","role":"user","content":"Continue."}
		]
	}`)

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "local_shell_call"},
			{Type: "shell_call_output"},
			{Type: "message", Role: "user", Content: json.RawMessage(`"Continue."`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		StructuredShellMaxBytes: 96,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if got, want := strings.Join(result.FiltersRun, ","), "oversized_blocks"; got != want {
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
	output := input[1].(map[string]any)["output"].(string)
	wantOutput := truncateStructuredShellOutput(largeOutput, 96)
	if output != wantOutput {
		t.Fatalf("unexpected truncated output:\n got: %q\nwant: %q", output, wantOutput)
	}
	if !strings.Contains(output, "[truncated by neurorouter") {
		t.Fatalf("missing truncation marker: %q", output)
	}
	if !strings.Contains(output, "exit status 1\n") {
		t.Fatalf("tail context should remain: %q", output)
	}
}

func TestRewriteResponsesRequestWithConfig_KeepsShellOutputsWhenCapDisabled(t *testing.T) {
	largeOutput := strings.Repeat("build log line\n", 32) + "fatal: no such file or directory\nexit status 1\n"
	rawBody := []byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"local_shell_call","call_id":"shell_call_1","status":"completed","action":{"type":"exec","command":["go","test","./..."],"working_directory":"/repo"}},
			{"type":"shell_call_output","call_id":"shell_call_1","output":` + strconv.Quote(largeOutput) + `,"status":"completed","exit_code":1},
			{"type":"message","role":"user","content":"Continue."}
		]
	}`)

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "local_shell_call"},
			{Type: "shell_call_output"},
			{Type: "message", Role: "user", Content: json.RawMessage(`"Continue."`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if len(result.FiltersRun) != 0 {
		t.Fatalf("expected no truncation by default, got %v", result.FiltersRun)
	}
	if result.BytesBefore != result.BytesAfter {
		t.Fatalf("expected no rewrite size change, got before=%d after=%d", result.BytesBefore, result.BytesAfter)
	}
}

func TestRewriteResponsesRequestWithConfig_TruncatesReadStyleFunctionOutputs(t *testing.T) {
	largeBody := strings.Repeat("  1737\tfunc TestSomething(t *testing.T) {}\n", 512)
	transcript := testReadStyleFunctionOutputTranscript(
		`/bin/zsh -lc "nl -ba internal/neurorouter/proxy_test.go | sed -n '1737,2898p'"`,
		largeBody,
	)
	rawBody := []byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"function_call_output","call_id":"call_read_1","output":` + strconv.Quote(transcript) + `},
			{"type":"message","role":"user","content":"Continue."}
		]
	}`)

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "function_call_output"},
			{Type: "message", Role: "user", Content: json.RawMessage(`"Continue."`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		OversizedBlocks: true,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if got, want := strings.Join(result.FiltersRun, ","), "oversized_blocks"; got != want {
		t.Fatalf("filters run: got %q, want %q", got, want)
	}
	if result.BytesBefore <= result.BytesAfter {
		t.Fatalf("expected bytes to shrink, got before=%d after=%d", result.BytesBefore, result.BytesAfter)
	}

	expectedOutput, changed := truncateReadStyleFunctionTranscript(transcript, defaultStructuredReadOutputMax)
	if !changed {
		t.Fatal("expected read-style transcript to truncate")
	}

	var rewritten map[string]any
	if err := json.Unmarshal(result.Body, &rewritten); err != nil {
		t.Fatalf("decode rewritten: %v", err)
	}
	input := rewritten["input"].([]any)
	output := input[0].(map[string]any)["output"].(string)
	if output != expectedOutput {
		t.Fatalf("unexpected truncated output:\n got: %q\nwant: %q", output, expectedOutput)
	}
	if !strings.Contains(output, "[truncated by neurorouter") {
		t.Fatalf("missing truncation marker: %q", output)
	}
	if !strings.Contains(output, "Command: /bin/zsh -lc") {
		t.Fatalf("missing command prefix: %q", output)
	}
}

func TestRewriteResponsesRequestWithConfig_TruncatesOversizedNonReadFunctionOutputs(t *testing.T) {
	largeBody := strings.Repeat("PASS\n", 6000)
	transcript := testReadStyleFunctionOutputTranscript(`/bin/zsh -lc "go test ./..."`, largeBody)
	rawBody := []byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"function_call_output","call_id":"call_exec_1","output":` + strconv.Quote(transcript) + `},
			{"type":"message","role":"user","content":"Continue."}
		]
	}`)

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "function_call_output"},
			{Type: "message", Role: "user", Content: json.RawMessage(`"Continue."`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		OversizedBlocks: true,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if got, want := strings.Join(result.FiltersRun, ","), "oversized_blocks"; got != want {
		t.Fatalf("filters run: got %q, want %q", got, want)
	}
	if result.BytesBefore <= result.BytesAfter {
		t.Fatalf("expected bytes to shrink, got before=%d after=%d", result.BytesBefore, result.BytesAfter)
	}

	expectedOutput, changed := truncateNonReadFunctionTranscript(transcript, defaultStructuredReadOutputMax)
	if !changed {
		t.Fatal("expected non-read transcript to truncate")
	}

	var rewritten map[string]any
	if err := json.Unmarshal(result.Body, &rewritten); err != nil {
		t.Fatalf("decode rewritten: %v", err)
	}
	input := rewritten["input"].([]any)
	output := input[0].(map[string]any)["output"].(string)
	if output != expectedOutput {
		t.Fatalf("unexpected truncated output:\n got: %q\nwant: %q", output, expectedOutput)
	}
	if !strings.Contains(output, "[truncated by neurorouter") {
		t.Fatalf("missing truncation marker: %q", output)
	}
	if !strings.Contains(output, `Command: /bin/zsh -lc "go test ./..."`) {
		t.Fatalf("missing command prefix: %q", output)
	}
	if !strings.Contains(output, "PASS") {
		t.Fatalf("expected retained body context: %q", output)
	}
}

func TestRewriteResponsesRequestWithConfig_KeepsSmallNonReadFunctionOutputs(t *testing.T) {
	smallBody := strings.Repeat("PASS\n", 16)
	transcript := testReadStyleFunctionOutputTranscript(`/bin/zsh -lc "go test ./..."`, smallBody)
	rawBody := []byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"function_call_output","call_id":"call_exec_1","output":` + strconv.Quote(transcript) + `},
			{"type":"message","role":"user","content":"Continue."}
		]
	}`)

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "function_call_output"},
			{Type: "message", Role: "user", Content: json.RawMessage(`"Continue."`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		OversizedBlocks: true,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if len(result.FiltersRun) != 0 {
		t.Fatalf("expected no truncation, got %v", result.FiltersRun)
	}
	if result.BytesBefore != result.BytesAfter {
		t.Fatalf("expected no rewrite size change, got before=%d after=%d", result.BytesBefore, result.BytesAfter)
	}
}

func TestRewriteResponsesRequestWithConfig_BudgetsOlderReadStyleFunctionOutputs(t *testing.T) {
	largeBody := strings.Repeat("  1737\tfunc TestSomething(t *testing.T) {}\n", 140)
	inputJSON := make([]string, 0, 8)
	transcripts := make([]string, 0, 7)

	for i := 0; i < 7; i++ {
		transcript := testReadStyleFunctionOutputTranscript(
			`/bin/zsh -lc "nl -ba internal/neurorouter/file_`+strconv.Itoa(i)+`.go | sed -n '1,200p'"`,
			largeBody,
		)
		transcripts = append(transcripts, transcript)
		inputJSON = append(inputJSON, `{"type":"function_call_output","call_id":"call_read_`+strconv.Itoa(i)+`","output":`+strconv.Quote(transcript)+`}`)
	}
	inputJSON = append(inputJSON, `{"type":"message","role":"user","content":"Continue."}`)

	rawBody := []byte("{\n" +
		`  "model":"gpt-5.4",` + "\n" +
		`  "input":[` + strings.Join(inputJSON, ",") + "]\n" +
		"}")

	req := &ResponsesRequest{
		Input: []InputItem{
			{Type: "function_call_output"},
			{Type: "function_call_output"},
			{Type: "function_call_output"},
			{Type: "function_call_output"},
			{Type: "function_call_output"},
			{Type: "function_call_output"},
			{Type: "function_call_output"},
			{Type: "message", Role: "user", Content: json.RawMessage(`"Continue."`)},
		},
	}

	original, err := ExtractRequestMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	result, err := RewriteResponsesRequestWithConfig(rawBody, original, original, FilterConfig{
		OversizedBlocks: true,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if got, want := strings.Join(result.FiltersRun, ","), "oversized_blocks"; got != want {
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
	if len(input) != 8 {
		t.Fatalf("input length: got %d, want 8", len(input))
	}

	olderBodyBytes := 0
	for i := 0; i < 7; i++ {
		output := input[i].(map[string]any)["output"].(string)
		_, _, body, ok := parseReadStyleFunctionTranscript(output)
		if !ok {
			t.Fatalf("item %d lost read-style transcript framing", i)
		}
		if i >= 4 && output != transcripts[i] {
			t.Fatalf("recent transcript %d should remain intact", i)
		}
		if i < 4 {
			olderBodyBytes += len(body)
		}
	}

	if olderBodyBytes > defaultStructuredReadHistoryMax {
		t.Fatalf("older read transcript budget exceeded: got %d, want <= %d", olderBodyBytes, defaultStructuredReadHistoryMax)
	}
	if output := input[0].(map[string]any)["output"].(string); output == transcripts[0] {
		t.Fatal("expected oldest transcript to shrink")
	}
	if output := input[1].(map[string]any)["output"].(string); !strings.Contains(output, "[truncated by neurorouter") {
		t.Fatalf("expected older transcript to include truncation marker: %q", output)
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

func testReadStyleFunctionOutputTranscript(command, body string) string {
	return "Command: " + command + "\n" +
		"Chunk ID: chunk_test\n" +
		"Wall time: 0.0000 seconds\n" +
		"Process exited with code 0\n" +
		"Original token count: 2048\n" +
		"Output:\n" + body
}

func testReasoningInputJSON(payload string, i int) string {
	return `{"type":"reasoning","id":"rs_` + strconv.Itoa(i) + `","content":null,"encrypted_content":` + strconv.Quote(payload) + `,"summary":[]}`
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
