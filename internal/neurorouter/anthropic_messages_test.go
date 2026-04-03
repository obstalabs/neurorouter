package neurorouter

import (
	"encoding/json"
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

	var firstBlocks []map[string]string
	if err := json.Unmarshal(rewrittenReq.Messages[0].Content, &firstBlocks); err != nil {
		t.Fatalf("decode first content blocks: %v", err)
	}
	if strings.Contains(firstBlocks[0]["text"], "system-reminder") {
		t.Fatalf("expected first reminder removed, got %q", firstBlocks[0]["text"])
	}

	var secondBlocks []map[string]string
	if err := json.Unmarshal(rewrittenReq.Messages[1].Content, &secondBlocks); err != nil {
		t.Fatalf("decode second content blocks: %v", err)
	}
	if !strings.Contains(secondBlocks[0]["text"], "system-reminder") {
		t.Fatalf("expected last reminder retained, got %q", secondBlocks[0]["text"])
	}
}
