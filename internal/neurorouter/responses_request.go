package neurorouter

import (
	"encoding/json"
	"fmt"
)

const instructionMessageSource = "instructions"

// ExtractRequestMessages pulls the text-bearing messages that should run through
// filtering/protection. Non-message Responses items stay in the original request
// and are handled by native passthrough when supported.
func ExtractRequestMessages(req *ResponsesRequest) ([]ChatMessage, error) {
	var msgs []ChatMessage

	if req.Instructions != "" {
		msgs = append(msgs, ChatMessage{
			Role:    "system",
			Content: req.Instructions,
			Source:  instructionMessageSource,
		})
	}

	for i, item := range req.Input {
		if item.Type != "message" {
			continue
		}

		role := normalizeMessageRole(item.Role)
		content, err := extractContent(item.Content)
		if err != nil {
			return nil, fmt.Errorf("extract content for %s message: %w", role, err)
		}
		if content == "" {
			continue
		}

		msgs = append(msgs, ChatMessage{
			Role:    role,
			Content: content,
			Source:  responseInputSource(i),
		})
	}

	return msgs, nil
}

// BuildChatRequest converts extracted text messages into a Chat Completions request.
func BuildChatRequest(req *ResponsesRequest, msgs []ChatMessage) (*ChatRequest, error) {
	chat := &ChatRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxOutputTokens,
		Stream:      req.Stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
	}

	for _, msg := range msgs {
		if msg.Content == "" {
			continue
		}
		chat.Messages = append(chat.Messages, ChatMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	if len(chat.Messages) == 0 {
		return nil, fmt.Errorf("no messages after translation")
	}

	return chat, nil
}

// RewriteResponsesRequest applies filtered text back onto the original Responses
// request body without disturbing non-message items or unknown fields.
func RewriteResponsesRequest(rawBody []byte, originalMsgs, processedMsgs []ChatMessage) ([]byte, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &doc); err != nil {
		return nil, fmt.Errorf("decode responses request: %w", err)
	}

	originalSources := make(map[string]struct{}, len(originalMsgs))
	for _, msg := range originalMsgs {
		if msg.Source == "" {
			continue
		}
		originalSources[msg.Source] = struct{}{}
	}

	processedBySource := make(map[string]ChatMessage, len(processedMsgs))
	for _, msg := range processedMsgs {
		if msg.Source == "" {
			continue
		}
		processedBySource[msg.Source] = msg
	}

	if _, ok := originalSources[instructionMessageSource]; ok {
		if msg, ok := processedBySource[instructionMessageSource]; ok && msg.Content != "" {
			doc["instructions"] = marshalRawJSONString(msg.Content)
		} else {
			delete(doc, "instructions")
		}
	}

	rawInput, ok := doc["input"]
	if !ok {
		return json.Marshal(doc)
	}

	var items []json.RawMessage
	if err := json.Unmarshal(rawInput, &items); err != nil {
		return nil, fmt.Errorf("decode input items: %w", err)
	}

	updated := make([]json.RawMessage, 0, len(items))
	for i, rawItem := range items {
		source := responseInputSource(i)
		if _, ok := originalSources[source]; !ok {
			updated = append(updated, rawItem)
			continue
		}

		newText := ""
		if msg, ok := processedBySource[source]; ok {
			newText = msg.Content
		}

		rewritten, keep, err := rewriteRawMessageItem(rawItem, newText)
		if err != nil {
			return nil, fmt.Errorf("rewrite input item %d: %w", i, err)
		}
		if keep {
			updated = append(updated, rewritten)
		}
	}

	inputBytes, err := json.Marshal(updated)
	if err != nil {
		return nil, fmt.Errorf("marshal input items: %w", err)
	}
	doc["input"] = inputBytes

	return json.Marshal(doc)
}

func rewriteRawMessageItem(rawItem json.RawMessage, newText string) (json.RawMessage, bool, error) {
	var item map[string]json.RawMessage
	if err := json.Unmarshal(rawItem, &item); err != nil {
		return nil, false, fmt.Errorf("decode raw item: %w", err)
	}

	contentRaw, ok := item["content"]
	if !ok || len(contentRaw) == 0 {
		return rawItem, true, nil
	}

	var text string
	if err := json.Unmarshal(contentRaw, &text); err == nil {
		if newText == "" {
			return nil, false, nil
		}
		item["content"] = marshalRawJSONString(newText)
		out, err := json.Marshal(item)
		if err != nil {
			return nil, false, fmt.Errorf("marshal string-content item: %w", err)
		}
		return out, true, nil
	}

	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(contentRaw, &parts); err != nil {
		return nil, false, fmt.Errorf("decode content parts: %w", err)
	}

	outParts := make([]map[string]json.RawMessage, 0, len(parts))
	wroteText := false
	for _, part := range parts {
		partType, err := rawJSONStringValue(part["type"])
		if err != nil {
			return nil, false, fmt.Errorf("decode content part type: %w", err)
		}

		if isTextContentType(partType) {
			if newText == "" || wroteText {
				continue
			}
			part["text"] = marshalRawJSONString(newText)
			outParts = append(outParts, part)
			wroteText = true
			continue
		}

		outParts = append(outParts, part)
	}

	if !wroteText && newText != "" {
		outParts = append(outParts, map[string]json.RawMessage{
			"type": marshalRawJSONString("input_text"),
			"text": marshalRawJSONString(newText),
		})
	}

	if len(outParts) == 0 {
		return nil, false, nil
	}

	item["content"] = marshalRawValue(outParts)

	out, err := json.Marshal(item)
	if err != nil {
		return nil, false, fmt.Errorf("marshal array-content item: %w", err)
	}
	return out, true, nil
}

func normalizeMessageRole(role string) string {
	if role == "developer" {
		return "system"
	}
	return role
}

func responseInputSource(index int) string {
	return fmt.Sprintf("input:%d", index)
}

func isTextContentType(partType string) bool {
	switch partType {
	case "input_text", "text", "output_text":
		return true
	default:
		return false
	}
}

func rawJSONStringValue(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var out string
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return out, nil
}

func marshalRawJSONString(value string) json.RawMessage {
	return marshalRawValue(value)
}

func marshalRawValue(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}
