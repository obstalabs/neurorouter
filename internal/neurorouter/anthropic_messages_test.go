package neurorouter

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestExtractAnthropicMessages_TextOnlyBlocksFlattenSemantically(t *testing.T) {
	req := &MessagesRequest{
		Messages: []APIMessage{{
			Role: "assistant",
			Content: json.RawMessage(`[
				{"type":"thinking","thinking":"internal reasoning"},
				{"type":"text","text":"Visible answer"}
			]`),
		}},
	}

	msgs, err := ExtractAnthropicMessages(req)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages: got %d, want 1", len(msgs))
	}
	if got, want := msgs[0].Content, "<thinking>internal reasoning</thinking>Visible answer"; got != want {
		t.Fatalf("content: got %q, want %q", got, want)
	}
}

func TestRewriteAnthropicMessagesRequest_ThinkingFilterShrinksTextBlocks(t *testing.T) {
	rawBody := []byte(`{
		"model":"claude-sonnet",
		"messages":[
			{
				"role":"assistant",
				"content":[
					{"type":"thinking","thinking":"internal reasoning"},
					{"type":"text","text":"Visible answer"}
				]
			},
			{"role":"user","content":"hello"}
		]
	}`)

	req, err := UnmarshalMessagesRequest(rawBody)
	if err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	original, err := ExtractAnthropicMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	filtered := FilterThinking(original)
	rewrite, err := RewriteAnthropicMessagesRequest(rawBody, original, filtered)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if rewrite.BytesBefore <= rewrite.BytesAfter {
		t.Fatalf("expected bytes to shrink, got before=%d after=%d", rewrite.BytesBefore, rewrite.BytesAfter)
	}

	rewrittenReq, err := UnmarshalMessagesRequest(rewrite.Body)
	if err != nil {
		t.Fatalf("unmarshal rewritten request: %v", err)
	}

	var blocks []map[string]string
	if err := json.Unmarshal(rewrittenReq.Messages[0].Content, &blocks); err != nil {
		t.Fatalf("decode content blocks: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks: got %d, want 1", len(blocks))
	}
	if got := blocks[0]["type"]; got != "text" {
		t.Fatalf("block type: got %q, want text", got)
	}
	if got := blocks[0]["text"]; got != "Visible answer" {
		t.Fatalf("block text: got %q, want %q", got, "Visible answer")
	}
	if strings.Contains(string(rewrite.Body), `"content":"[{\"type\"`) {
		t.Fatalf("expected content to remain structured blocks, got %s", string(rewrite.Body))
	}
}

func TestRewriteAnthropicMessagesRequest_SystemReminderFilterShrinksTextBlocks(t *testing.T) {
	reminder := "<system-reminder>Follow repo policy.</system-reminder>"
	rawBody := []byte(fmt.Sprintf(`{
		"model":"claude-sonnet",
		"messages":[
			{"role":"user","content":[{"type":"text","text":"first %s"}]},
			{"role":"user","content":[{"type":"text","text":"second %s"}]}
		]
	}`, reminder, reminder))

	req, err := UnmarshalMessagesRequest(rawBody)
	if err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	original, err := ExtractAnthropicMessages(req)
	if err != nil {
		t.Fatalf("extract original: %v", err)
	}

	filtered := FilterSystemReminders(original)
	rewrite, err := RewriteAnthropicMessagesRequest(rawBody, original, filtered)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if rewrite.BytesBefore <= rewrite.BytesAfter {
		t.Fatalf("expected bytes to shrink, got before=%d after=%d", rewrite.BytesBefore, rewrite.BytesAfter)
	}

	rewrittenReq, err := UnmarshalMessagesRequest(rewrite.Body)
	if err != nil {
		t.Fatalf("unmarshal rewritten request: %v", err)
	}
	if len(rewrittenReq.Messages) != 1 {
		t.Fatalf("messages: got %d, want 1 merged user message", len(rewrittenReq.Messages))
	}

	var mergedBlocks []map[string]string
	if err := json.Unmarshal(rewrittenReq.Messages[0].Content, &mergedBlocks); err != nil {
		t.Fatalf("decode merged content blocks: %v", err)
	}
	if len(mergedBlocks) != 2 {
		t.Fatalf("merged blocks: got %d, want 2", len(mergedBlocks))
	}
	if strings.Contains(mergedBlocks[0]["text"], "system-reminder") {
		t.Fatalf("expected first reminder removed, got %q", mergedBlocks[0]["text"])
	}
	if !strings.Contains(mergedBlocks[1]["text"], "system-reminder") {
		t.Fatalf("expected last reminder retained, got %q", mergedBlocks[1]["text"])
	}
}

func TestRewriteAnthropicMessages_EmptyTextBlockAfterSystemReminderStrip(t *testing.T) {
	body := []byte(`{
		"model": "claude-opus-4-6",
		"max_tokens": 16000,
		"messages": [
			{"role": "assistant", "content": [
				{"type": "tool_use", "id": "toolu_01A", "name": "Read", "input": {"file_path": "/tmp/test.go"}},
				{"type": "tool_use", "id": "toolu_01B", "name": "Read", "input": {"file_path": "/tmp/other.go"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "toolu_01A", "content": "file contents A"},
				{"type": "tool_result", "tool_use_id": "toolu_01B", "content": "file contents B"},
				{"type": "text", "text": "<system-reminder>\nThe user sent a message.\n</system-reminder>"}
			]}
		]
	}`)

	req, err := UnmarshalMessagesRequest(body)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	original, err := ExtractAnthropicMessages(req)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	filtered := FilterSystemReminders(original)

	result, err := RewriteAnthropicMessagesRequest(body, original, filtered)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	var rewritten struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(result.Body, &rewritten); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	for i, msg := range rewritten.Messages {
		var blocks []map[string]any
		if err := json.Unmarshal(msg.Content, &blocks); err != nil {
			continue
		}
		for j, block := range blocks {
			if block["type"] == "text" {
				text, _ := block["text"].(string)
				if strings.TrimSpace(text) == "" {
					t.Fatalf("message[%d] content[%d]: empty text block survived filtering", i, j)
				}
			}
		}
	}

	var userBlocks []map[string]any
	if err := json.Unmarshal(rewritten.Messages[1].Content, &userBlocks); err != nil {
		t.Fatalf("unmarshal user content: %v", err)
	}

	toolResults := 0
	for _, block := range userBlocks {
		if block["type"] == "tool_result" {
			toolResults++
		}
	}
	if toolResults != 2 {
		t.Fatalf("expected 2 tool_result blocks preserved, got %d", toolResults)
	}
}

func TestMarshalAnthropicTextBlocks_StripsWhitespaceOnlyText(t *testing.T) {
	content, err := marshalAnthropicTextBlocks("  \n\t  ")
	if err != nil {
		t.Fatalf("marshal text blocks: %v", err)
	}
	if content != nil {
		t.Fatalf("expected whitespace-only text blocks to be dropped, got %s", string(content))
	}
}

func TestMarshalAnthropicFilteredContent_ToolFallbackStripsEmptyTextBlocks(t *testing.T) {
	original := json.RawMessage(`[
		{"type":"tool_result","tool_use_id":"toolu_01A","content":"file contents"},
		{"type":"text","text":"   "}
	]`)

	content, err := marshalAnthropicFilteredContent(original, "visible fallback")
	if err != nil {
		t.Fatalf("marshal filtered content: %v", err)
	}

	var blocks []map[string]any
	if err := json.Unmarshal(content, &blocks); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks: got %d, want 1", len(blocks))
	}
	if got := blocks[0]["type"]; got != "tool_result" {
		t.Fatalf("type: got %v, want tool_result", got)
	}
}

func TestRewriteAnthropicMessagesRequest_MergesBrokenRoleAlternation(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet",
		"messages":[
			{"role":"user","content":"first"},
			{"role":"assistant","content":[{"type":"text","text":"assistant reply"}]},
			{"role":"user","content":"This session is being continued from a previous conversation.\n[coalesced]\nsummary body"},
			{"role":"assistant","content":[{"type":"text","text":"assistant follow-up"}]},
			{"role":"user","content":"This session is being continued from a previous conversation.\n[coalesced]\nsummary body"}
		]
	}`)

	req, err := UnmarshalMessagesRequest(body)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	original, err := ExtractAnthropicMessages(req)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	filtered := []ChatMessage{
		original[0],
		original[1],
		original[3],
		original[4],
	}

	rewrite, err := RewriteAnthropicMessagesRequest(body, original, filtered)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	req, err = UnmarshalMessagesRequest(rewrite.Body)
	if err != nil {
		t.Fatalf("unmarshal rewritten request: %v", err)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("messages: got %d, want 3", len(req.Messages))
	}
	if req.Messages[1].Role != "assistant" {
		t.Fatalf("merged role: got %q, want assistant", req.Messages[1].Role)
	}

	var blocks []map[string]string
	if err := json.Unmarshal(req.Messages[1].Content, &blocks); err != nil {
		t.Fatalf("decode merged assistant content: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("merged blocks: got %d, want 2", len(blocks))
	}
	if blocks[0]["text"] != "assistant reply" || blocks[1]["text"] != "assistant follow-up" {
		t.Fatalf("merged blocks: %+v", blocks)
	}
}

func TestAnthropicRewriteStatusCode(t *testing.T) {
	if got := anthropicRewriteStatusCode(&anthropicRewriteValidationError{err: fmt.Errorf("bad rewrite")}); got != 400 {
		t.Fatalf("status for validation error: got %d, want 400", got)
	}
	if got := anthropicRewriteStatusCode(fmt.Errorf("boom")); got != 500 {
		t.Fatalf("status for internal error: got %d, want 500", got)
	}
}

func TestRewriteAnthropicMessagesRequest_ReturnsValidationErrorForUnsupportedMerge(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet",
		"messages":[
			{"role":"user","content":"first"},
			{"role":"assistant","content":{"bad":"object"}},
			{"role":"user","content":"remove me"},
			{"role":"assistant","content":[{"type":"text","text":"assistant follow-up"}]}
		]
	}`)

	req, err := UnmarshalMessagesRequest(body)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	original, err := ExtractAnthropicMessages(req)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	filtered := []ChatMessage{
		original[0],
		original[1],
		original[3],
	}

	_, err = RewriteAnthropicMessagesRequest(body, original, filtered)
	if err == nil {
		t.Fatal("expected merge validation error, got nil")
	}
	var validationErr *anthropicRewriteValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected anthropicRewriteValidationError, got %T", err)
	}
	if !strings.Contains(err.Error(), "unsupported anthropic content encoding") {
		t.Fatalf("unexpected error: %v", err)
	}
}
