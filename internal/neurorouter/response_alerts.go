package neurorouter

import (
	"encoding/json"
	"strings"
)

type responsesAlertStreamState struct {
	prefix   string
	injected bool
}

func newResponsesAlertStreamState(alerts []Alert) *responsesAlertStreamState {
	prefix := FormatAlerts(alerts)
	if prefix == "" {
		return nil
	}
	return &responsesAlertStreamState{prefix: prefix}
}

func prependAlertsToResponsesAPIResponse(resp *ResponsesAPIResponse, alerts []Alert) {
	if resp == nil {
		return
	}

	prefix := FormatAlerts(alerts)
	if prefix == "" {
		return
	}

	if injectAlertPrefixIntoTypedOutput(resp.Output, prefix) {
		return
	}

	resp.Output = append([]OutputItem{{
		Type:   "message",
		Role:   "assistant",
		Status: "completed",
		Content: []OutputContent{{
			Type: "output_text",
			Text: prefix,
		}},
	}}, resp.Output...)
}

func injectAlertsIntoResponsesBody(body []byte, alerts []Alert) ([]byte, error) {
	prefix := FormatAlerts(alerts)
	if prefix == "" {
		return body, nil
	}

	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	injectAlertPrefixIntoResponseDoc(doc, prefix)
	return json.Marshal(doc)
}

func rewriteResponsesEventPayload(payload []byte, eventName string, state *responsesAlertStreamState) ([]byte, error) {
	if state == nil || state.prefix == "" {
		return payload, nil
	}

	var doc map[string]any
	if err := json.Unmarshal(payload, &doc); err != nil {
		return nil, err
	}

	if eventName == "" {
		if rawType, ok := doc["type"].(string); ok {
			eventName = rawType
		}
	}

	changed := false
	switch eventName {
	case "response.output_text.delta":
		if delta, ok := doc["delta"].(string); ok && !state.injected {
			doc["delta"] = state.prefix + delta
			state.injected = true
			changed = true
		}
	case "response.output_text.done", "response.content_part.done":
		if text, ok := doc["text"].(string); ok {
			updated := ensureAlertPrefix(state.prefix, text)
			if updated != text {
				doc["text"] = updated
				changed = true
			}
			state.injected = true
		}
	case "response.output_item.done":
		if injectAlertPrefixIntoContentField(doc, state.prefix) {
			state.injected = true
			changed = true
		}
	case "response.completed":
		if injectAlertPrefixIntoCompletedDoc(doc, state.prefix) {
			state.injected = true
			changed = true
		}
	}

	if !changed {
		return payload, nil
	}
	return json.Marshal(doc)
}

func ensureAlertPrefix(prefix, text string) string {
	if prefix == "" || strings.HasPrefix(text, prefix) {
		return text
	}
	return prefix + text
}

func injectAlertPrefixIntoTypedOutput(output []OutputItem, prefix string) bool {
	for outputIndex := range output {
		for contentIndex := range output[outputIndex].Content {
			part := &output[outputIndex].Content[contentIndex]
			if part.Type != "output_text" && part.Type != "text" {
				continue
			}
			part.Text = ensureAlertPrefix(prefix, part.Text)
			return true
		}
	}
	return false
}

func injectAlertPrefixIntoResponseDoc(doc map[string]any, prefix string) bool {
	if rawOutput, ok := doc["output"].([]any); ok {
		changed := injectAlertPrefixIntoGenericOutput(&rawOutput, prefix)
		doc["output"] = rawOutput
		return changed
	}

	doc["output"] = []any{newAlertMessageItem(prefix)}
	return true
}

func injectAlertPrefixIntoCompletedDoc(doc map[string]any, prefix string) bool {
	if rawResponse, ok := doc["response"].(map[string]any); ok {
		changed := injectAlertPrefixIntoResponseDoc(rawResponse, prefix)
		doc["response"] = rawResponse
		return changed
	}
	return injectAlertPrefixIntoResponseDoc(doc, prefix)
}

func injectAlertPrefixIntoContentField(doc map[string]any, prefix string) bool {
	rawContent, ok := doc["content"].([]any)
	if !ok {
		doc["content"] = []any{newAlertContentPart(prefix)}
		return true
	}
	changed := injectAlertPrefixIntoGenericContent(&rawContent, prefix)
	doc["content"] = rawContent
	return changed
}

func injectAlertPrefixIntoGenericOutput(output *[]any, prefix string) bool {
	for index := range *output {
		item, ok := (*output)[index].(map[string]any)
		if !ok {
			continue
		}
		if injectAlertPrefixIntoMessageItem(item, prefix) {
			(*output)[index] = item
			return true
		}
	}

	*output = append([]any{newAlertMessageItem(prefix)}, *output...)
	return true
}

func injectAlertPrefixIntoMessageItem(item map[string]any, prefix string) bool {
	rawContent, ok := item["content"].([]any)
	if !ok {
		item["content"] = []any{newAlertContentPart(prefix)}
		return true
	}

	changed := injectAlertPrefixIntoGenericContent(&rawContent, prefix)
	item["content"] = rawContent
	return changed
}

func injectAlertPrefixIntoGenericContent(content *[]any, prefix string) bool {
	for index := range *content {
		part, ok := (*content)[index].(map[string]any)
		if !ok {
			continue
		}
		partType, _ := part["type"].(string)
		if partType != "output_text" && partType != "text" {
			continue
		}
		text, _ := part["text"].(string)
		part["text"] = ensureAlertPrefix(prefix, text)
		(*content)[index] = part
		return true
	}

	*content = append([]any{newAlertContentPart(prefix)}, *content...)
	return true
}

func newAlertMessageItem(prefix string) map[string]any {
	return map[string]any{
		"type":   "message",
		"role":   "assistant",
		"status": "completed",
		"content": []any{
			newAlertContentPart(prefix),
		},
	}
}

func newAlertContentPart(prefix string) map[string]any {
	return map[string]any{
		"type": "output_text",
		"text": prefix,
	}
}
