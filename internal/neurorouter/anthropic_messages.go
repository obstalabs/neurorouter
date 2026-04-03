package neurorouter

import (
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
	for i, msg := range req.Messages {
		filtered, ok := kept[anthropicMessageSource(i)]
		if !ok {
			continue
		}

		msg.Content, err = marshalAnthropicFilteredContent(msg.Content, filtered.Content)
		if err != nil {
			return nil, fmt.Errorf("rewrite message %d: %w", i, err)
		}
		rewritten = append(rewritten, msg)
	}
	req.Messages = rewritten

	body, err := MarshalMessagesRequest(req)
	if err != nil {
		return nil, err
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
		return encoded, nil
	}

	if json.Valid([]byte(filtered)) {
		return json.RawMessage(filtered), nil
	}

	if anthropicContentContainsToolBlocks(original) {
		return append(json.RawMessage(nil), original...), nil
	}

	encoded, err := json.Marshal(filtered)
	if err != nil {
		return nil, err
	}
	return encoded, nil
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
