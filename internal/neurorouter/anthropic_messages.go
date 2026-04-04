package neurorouter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const defaultAnthropicAPIVersion = "2023-06-01"

const anthropicSystemSource = "anthropic.system"

// MessagesRequest is the Anthropic Messages API request body.
// Only fields we need to inspect are typed; unknown fields pass through unchanged.
type MessagesRequest struct {
	Model     string                     `json:"model"`
	Messages  []APIMessage               `json:"messages"`
	System    json.RawMessage            `json:"system,omitempty"`
	Stream    bool                       `json:"stream,omitempty"`
	StreamSet bool                       `json:"-"`
	RawFields map[string]json.RawMessage `json:"-"`
}

// APIMessage is one Anthropic messages-array entry.
type APIMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// UnmarshalMessagesRequest parses an Anthropic Messages request while preserving unknown fields.
func UnmarshalMessagesRequest(data []byte) (*MessagesRequest, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	req := &MessagesRequest{RawFields: make(map[string]json.RawMessage)}

	if v, ok := raw["model"]; ok {
		if err := json.Unmarshal(v, &req.Model); err != nil {
			return nil, fmt.Errorf("decode model: %w", err)
		}
		delete(raw, "model")
	}
	if v, ok := raw["messages"]; ok {
		if err := json.Unmarshal(v, &req.Messages); err != nil {
			return nil, fmt.Errorf("decode messages: %w", err)
		}
		delete(raw, "messages")
	}
	if v, ok := raw["system"]; ok {
		req.System = append(json.RawMessage(nil), v...)
		delete(raw, "system")
	}
	if v, ok := raw["stream"]; ok {
		if err := json.Unmarshal(v, &req.Stream); err != nil {
			return nil, fmt.Errorf("decode stream: %w", err)
		}
		req.StreamSet = true
		delete(raw, "stream")
	}

	for k, v := range raw {
		req.RawFields[k] = append(json.RawMessage(nil), v...)
	}
	return req, nil
}

// MarshalMessagesRequest re-encodes an Anthropic Messages request while preserving unknown fields.
func MarshalMessagesRequest(req *MessagesRequest) ([]byte, error) {
	out := make(map[string]json.RawMessage, len(req.RawFields)+4)
	for k, v := range req.RawFields {
		out[k] = append(json.RawMessage(nil), v...)
	}

	if req.Model != "" {
		v, err := json.Marshal(req.Model)
		if err != nil {
			return nil, fmt.Errorf("encode model: %w", err)
		}
		out["model"] = v
	}
	if len(req.Messages) > 0 {
		v, err := json.Marshal(req.Messages)
		if err != nil {
			return nil, fmt.Errorf("encode messages: %w", err)
		}
		out["messages"] = v
	}
	if req.System != nil {
		out["system"] = append(json.RawMessage(nil), req.System...)
	}
	if req.StreamSet {
		v, err := json.Marshal(req.Stream)
		if err != nil {
			return nil, fmt.Errorf("encode stream: %w", err)
		}
		out["stream"] = v
	}

	return json.Marshal(out)
}

// ExtractAnthropicMessages flattens system and message content into the shared filter format.
func ExtractAnthropicMessages(req *MessagesRequest) ([]ChatMessage, error) {
	msgs := make([]ChatMessage, 0, len(req.Messages)+1)

	if len(req.System) > 0 {
		content, err := anthropicRawToFilterString(req.System)
		if err != nil {
			return nil, fmt.Errorf("decode system: %w", err)
		}
		if strings.TrimSpace(content) != "" {
			msgs = append(msgs, ChatMessage{Role: "system", Content: content, Source: anthropicSystemSource})
		}
	}

	for i, msg := range req.Messages {
		content, err := anthropicRawToFilterString(msg.Content)
		if err != nil {
			return nil, fmt.Errorf("decode message %d: %w", i, err)
		}
		msgs = append(msgs, ChatMessage{
			Role:    msg.Role,
			Content: content,
			Source:  anthropicMessageSource(i),
		})
	}

	return msgs, nil
}

// RewriteAnthropicMessagesRequest rewrites only the filtered message content while preserving unknown fields.
func RewriteAnthropicMessagesRequest(rawBody []byte, originalMsgs, filteredMsgs []ChatMessage) (*AnthropicMessagesRewriteResult, error) {
	if chatMessagesEqual(originalMsgs, filteredMsgs) {
		return &AnthropicMessagesRewriteResult{
			Body:        append([]byte(nil), rawBody...),
			BytesBefore: len(rawBody),
			BytesAfter:  len(rawBody),
		}, nil
	}

	req, err := UnmarshalMessagesRequest(rawBody)
	if err != nil {
		return nil, err
	}

	kept := make(map[string]ChatMessage, len(filteredMsgs))
	for _, msg := range filteredMsgs {
		kept[msg.Source] = msg
	}

	if len(req.System) > 0 {
		if filtered, ok := kept[anthropicSystemSource]; ok {
			req.System, err = marshalAnthropicFilteredContent(req.System, filtered.Content)
			if err != nil {
				return nil, fmt.Errorf("rewrite system: %w", err)
			}
		} else {
			req.System = nil
		}
	}

	rewritten := make([]APIMessage, 0, len(req.Messages))
	rewrittenOriginalIdx := make([]int, 0, len(req.Messages))
	for i, msg := range req.Messages {
		filtered, ok := kept[anthropicMessageSource(i)]
		if !ok {
			continue
		}

		msg.Content, err = marshalAnthropicFilteredContent(msg.Content, filtered.Content)
		if err != nil {
			return nil, fmt.Errorf("rewrite message %d: %w", i, err)
		}
		if len(msg.Content) == 0 {
			continue
		}
		rewritten = append(rewritten, msg)
		rewrittenOriginalIdx = append(rewrittenOriginalIdx, i)
	}
	req.Messages = rewritten

	if err := validateAnthropicRewrittenRequest(req, rewrittenOriginalIdx); err != nil {
		return nil, err
	}

	body, err := MarshalMessagesRequest(req)
	if err != nil {
		return nil, err
	}

	// Compact the rewritten body to eliminate any serialization overhead
	// (whitespace, key reordering) that could make the filtered output
	// larger than the original despite content removal.
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, body); err == nil && compacted.Len() < len(body) {
		body = compacted.Bytes()
	}

	return &AnthropicMessagesRewriteResult{
		Body:        body,
		BytesBefore: len(rawBody),
		BytesAfter:  len(body),
	}, nil
}

// AnthropicMessagesRewriteResult captures the rewritten body and raw request sizes.
type AnthropicMessagesRewriteResult struct {
	Body        []byte
	BytesBefore int
	BytesAfter  int
}

func anthropicMessageSource(index int) string {
	return "anthropic.message." + strconv.Itoa(index)
}

func anthropicRawToFilterString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString, nil
	}

	if textOnly, ok := anthropicTextOnlyBlocksFilterString(raw); ok {
		return textOnly, nil
	}

	trimmed := strings.TrimSpace(string(raw))
	if !json.Valid(raw) {
		return "", fmt.Errorf("invalid anthropic content JSON")
	}
	return trimmed, nil
}

func marshalAnthropicFilteredContent(original json.RawMessage, filtered string) (json.RawMessage, error) {
	filtered = strings.TrimSpace(filtered)
	if filtered == "" {
		return nil, nil
	}

	if anthropicContentWasJSONString(original) {
		encoded, err := json.Marshal(filtered)
		if err != nil {
			return nil, err
		}
		return finalizeAnthropicMarshaledContent(encoded), nil
	}

	if anthropicContentIsTextOnlyBlocks(original) {
		return marshalAnthropicTextBlocks(filtered)
	}

	if json.Valid([]byte(filtered)) {
		return finalizeAnthropicMarshaledContent(json.RawMessage(filtered)), nil
	}

	if anthropicContentContainsToolBlocks(original) {
		return finalizeAnthropicMarshaledContent(append(json.RawMessage(nil), original...)), nil
	}

	encoded, err := json.Marshal(filtered)
	if err != nil {
		return nil, err
	}
	return finalizeAnthropicMarshaledContent(encoded), nil
}

func anthropicContentWasJSONString(raw json.RawMessage) bool {
	var s string
	return json.Unmarshal(raw, &s) == nil
}

func anthropicContentContainsToolBlocks(raw json.RawMessage) bool {
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return false
	}
	for _, block := range blocks {
		blockType, err := rawJSONStringValue(block["type"])
		if err != nil {
			continue
		}
		if blockType == "tool_use" || blockType == "tool_result" {
			return true
		}
	}
	return false
}

// stripEmptyTextBlocks removes empty text blocks left behind by filters such as
// system_reminders so Anthropic never receives invalid blank text content.
func stripEmptyTextBlocks(raw []byte) []byte {
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return raw
	}

	out := make([]json.RawMessage, 0, len(blocks))
	for _, block := range blocks {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(block, &obj); err != nil {
			out = append(out, block)
			continue
		}

		blockType, err := rawJSONStringValue(obj["type"])
		if err != nil || blockType != "text" {
			out = append(out, block)
			continue
		}

		text, err := rawJSONStringValue(obj["text"])
		if err != nil {
			out = append(out, block)
			continue
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, block)
	}

	if len(out) == 0 {
		return nil
	}

	result, err := json.Marshal(out)
	if err != nil {
		return raw
	}
	return result
}

func finalizeAnthropicMarshaledContent(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}

	cleaned := stripEmptyTextBlocks(raw)
	if len(cleaned) == 0 {
		return nil
	}

	var text string
	if err := json.Unmarshal(cleaned, &text); err == nil && strings.TrimSpace(text) == "" {
		return nil
	}

	return cleaned
}

func anthropicContentIsTextOnlyBlocks(raw json.RawMessage) bool {
	_, ok := anthropicTextOnlyBlocksFilterString(raw)
	return ok
}

func anthropicTextOnlyBlocksFilterString(raw json.RawMessage) (string, bool) {
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", false
	}

	var b strings.Builder
	for _, block := range blocks {
		blockType, err := rawJSONStringValue(block["type"])
		if err != nil {
			return "", false
		}

		switch blockType {
		case "text":
			text, err := rawJSONStringValue(block["text"])
			if err != nil {
				return "", false
			}
			b.WriteString(text)
		case "thinking":
			thinking, err := anthropicBlockString(block, "thinking")
			if err != nil {
				return "", false
			}
			b.WriteString("<thinking>")
			b.WriteString(thinking)
			b.WriteString("</thinking>")
		case "redacted_thinking":
			b.WriteString("<thinking>[redacted]</thinking>")
		default:
			return "", false
		}
	}

	return b.String(), true
}

func anthropicBlockString(block map[string]json.RawMessage, field string) (string, error) {
	if raw, ok := block[field]; ok {
		return rawJSONStringValue(raw)
	}
	return "", fmt.Errorf("missing %s", field)
}

func marshalAnthropicTextBlocks(filtered string) (json.RawMessage, error) {
	blocks := []map[string]string{{
		"type": "text",
		"text": filtered,
	}}
	encoded, err := json.Marshal(blocks)
	if err != nil {
		return nil, err
	}
	return finalizeAnthropicMarshaledContent(encoded), nil
}

func validateAnthropicRewrittenRequest(req *MessagesRequest, originalIdx []int) error {
	if err := validateAnthropicContentBlocks(req.System, "system"); err != nil {
		return err
	}

	for i, msg := range req.Messages {
		if len(msg.Content) == 0 {
			return fmt.Errorf("anthropic rewrite produced empty content for messages[%d]", i)
		}
		if err := validateAnthropicContentBlocks(msg.Content, fmt.Sprintf("messages[%d].content", i)); err != nil {
			return err
		}
		if i == 0 {
			continue
		}
		if req.Messages[i-1].Role == msg.Role && !adjacentInOriginal(originalIdx, i-1, i) {
			return fmt.Errorf(
				"anthropic rewrite produced consecutive %q roles at messages[%d] and messages[%d]",
				msg.Role,
				i-1,
				i,
			)
		}
	}

	return nil
}

func adjacentInOriginal(originalIdx []int, left, right int) bool {
	if left < 0 || right >= len(originalIdx) {
		return false
	}
	return originalIdx[right]-originalIdx[left] == 1
}

func validateAnthropicContentBlocks(raw json.RawMessage, path string) error {
	if len(raw) == 0 {
		return nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("%s is empty after rewrite", path)
		}
		return nil
	}

	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}
	if len(blocks) == 0 {
		return fmt.Errorf("%s has no remaining content blocks after rewrite", path)
	}

	for i, block := range blocks {
		blockType, err := rawJSONStringValue(block["type"])
		if err != nil || blockType != "text" {
			continue
		}
		text, err := rawJSONStringValue(block["text"])
		if err != nil {
			continue
		}
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("%s[%d] is an empty text block after rewrite", path, i)
		}
	}

	return nil
}

func chatMessagesEqual(a, b []ChatMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Role != b[i].Role || a[i].Content != b[i].Content || a[i].Source != b[i].Source {
			return false
		}
	}
	return true
}
