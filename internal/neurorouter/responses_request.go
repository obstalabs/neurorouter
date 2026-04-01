package neurorouter

import (
	"encoding/json"
	"fmt"
	"strings"
)

const instructionMessageSource = "instructions"

type ResponsesRewriteResult struct {
	Body        []byte
	BytesBefore int
	BytesAfter  int
	FiltersRun  []string
}

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
	result, err := RewriteResponsesRequestWithConfig(rawBody, originalMsgs, processedMsgs, FilterConfig{})
	if err != nil {
		return nil, err
	}
	return result.Body, nil
}

func RewriteResponsesRequestWithConfig(rawBody []byte, originalMsgs, processedMsgs []ChatMessage, cfg FilterConfig) (*ResponsesRewriteResult, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &doc); err != nil {
		return nil, fmt.Errorf("decode responses request: %w", err)
	}
	originalCanonical, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal original responses request: %w", err)
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
		body, err := json.Marshal(doc)
		if err != nil {
			return nil, fmt.Errorf("marshal responses request without input: %w", err)
		}
		return &ResponsesRewriteResult{
			Body:        body,
			BytesBefore: len(originalCanonical),
			BytesAfter:  len(body),
		}, nil
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

	structuredFilters, err := cleanupStructuredResponsesItems(updated, cfg)
	if err != nil {
		return nil, err
	}
	updated = structuredFilters.Items

	inputBytes, err := json.Marshal(updated)
	if err != nil {
		return nil, fmt.Errorf("marshal input items: %w", err)
	}
	doc["input"] = inputBytes

	body, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal rewritten responses request: %w", err)
	}
	return &ResponsesRewriteResult{
		Body:        body,
		BytesBefore: len(originalCanonical),
		BytesAfter:  len(body),
		FiltersRun:  structuredFilters.FiltersRun,
	}, nil
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

type structuredCleanupResult struct {
	Items      []json.RawMessage
	FiltersRun []string
}

func cleanupStructuredResponsesItems(items []json.RawMessage, cfg FilterConfig) (*structuredCleanupResult, error) {
	out := append([]json.RawMessage(nil), items...)
	var filters []string

	if cfg.StaleReads {
		var changed bool
		next, readChanged, err := dropStructuredStaleReadItems(out)
		if err != nil {
			return nil, err
		}
		out = next
		changed = changed || readChanged

		next, searchChanged, err := dropStructuredStaleSearchItems(out)
		if err != nil {
			return nil, err
		}
		out = next
		changed = changed || searchChanged

		if changed {
			filters = append(filters, "stale_reads")
		}
	}

	if cfg.OrphanedResults {
		var changed bool
		next, dedupeChanged, err := dropStructuredSupersededOutputItems(out)
		if err != nil {
			return nil, err
		}
		out = next
		changed = changed || dedupeChanged

		next, orphanChanged, err := dropStructuredOrphanedOutputItems(out)
		if err != nil {
			return nil, err
		}
		out = next
		changed = changed || orphanChanged
		if changed {
			filters = append(filters, "orphaned_results")
		}
	}

	return &structuredCleanupResult{Items: out, FiltersRun: filters}, nil
}

type structuredCallRecord struct {
	Index  int
	CallID string
	Path   string
}

type structuredSearchRecord struct {
	Index     int
	CallID    string
	Signature string
}

func dropStructuredStaleReadItems(items []json.RawMessage) ([]json.RawMessage, bool, error) {
	reads := make(map[string][]structuredCallRecord)
	writes := make(map[string]int)
	outputsByCallID := make(map[string][]int)

	for i, rawItem := range items {
		item, err := decodeRawResponsesItem(rawItem)
		if err != nil {
			return nil, false, fmt.Errorf("decode structured item %d: %w", i, err)
		}

		callID, _ := rawJSONStringValue(item["call_id"])
		switch rawItemType(item) {
		case "function_call":
			name, _ := rawJSONStringValue(item["name"])
			args, _ := rawJSONStringValue(item["arguments"])
			path := extractStructuredPath(args)
			switch {
			case isStructuredReadToolName(name) && path != "" && callID != "":
				reads[path] = append(reads[path], structuredCallRecord{Index: i, CallID: callID, Path: path})
			case isStructuredWriteToolName(name) && path != "":
				writes[path] = i
			}
		case "custom_tool_call":
			name, _ := rawJSONStringValue(item["name"])
			input, _ := rawJSONStringValue(item["input"])
			path := extractStructuredPath(input)
			switch {
			case isStructuredReadToolName(name) && path != "" && callID != "":
				reads[path] = append(reads[path], structuredCallRecord{Index: i, CallID: callID, Path: path})
			case isStructuredWriteToolName(name) && path != "":
				writes[path] = i
			}
		case "function_call_output", "custom_tool_call_output":
			if callID != "" {
				outputsByCallID[callID] = append(outputsByCallID[callID], i)
			}
		}
	}

	drop := make(map[int]bool)
	for path, records := range reads {
		if len(records) <= 1 {
			continue
		}
		last := records[len(records)-1]
		latestWrite, hasWrite := writes[path]
		for _, record := range records[:len(records)-1] {
			if hasWrite && latestWrite > record.Index && latestWrite < last.Index {
				continue
			}
			drop[record.Index] = true
			for _, outputIndex := range outputsByCallID[record.CallID] {
				drop[outputIndex] = true
			}
		}
	}

	if len(drop) == 0 {
		return items, false, nil
	}
	return filterRawResponsesItems(items, drop), true, nil
}

func dropStructuredStaleSearchItems(items []json.RawMessage) ([]json.RawMessage, bool, error) {
	searches := make(map[string][]structuredSearchRecord)
	outputsByCallID := make(map[string][]int)

	for i, rawItem := range items {
		item, err := decodeRawResponsesItem(rawItem)
		if err != nil {
			return nil, false, fmt.Errorf("decode structured item %d: %w", i, err)
		}

		callID, _ := rawJSONStringValue(item["call_id"])
		switch rawItemType(item) {
		case "tool_search_call":
			if callID == "" {
				continue
			}
			signature, ok, err := structuredSearchSignature(item)
			if err != nil {
				return nil, false, fmt.Errorf("search signature for item %d: %w", i, err)
			}
			if !ok {
				continue
			}
			searches[signature] = append(searches[signature], structuredSearchRecord{
				Index:     i,
				CallID:    callID,
				Signature: signature,
			})
		case "tool_search_output":
			if callID != "" {
				outputsByCallID[callID] = append(outputsByCallID[callID], i)
			}
		}
	}

	drop := make(map[int]bool)
	for _, records := range searches {
		if len(records) <= 1 {
			continue
		}
		for _, record := range records[:len(records)-1] {
			drop[record.Index] = true
			for _, outputIndex := range outputsByCallID[record.CallID] {
				drop[outputIndex] = true
			}
		}
	}

	if len(drop) == 0 {
		return items, false, nil
	}
	return filterRawResponsesItems(items, drop), true, nil
}

func dropStructuredOrphanedOutputItems(items []json.RawMessage) ([]json.RawMessage, bool, error) {
	callIDs := make(map[string]bool)
	for i, rawItem := range items {
		item, err := decodeRawResponsesItem(rawItem)
		if err != nil {
			return nil, false, fmt.Errorf("decode structured item %d: %w", i, err)
		}
		callID, _ := rawJSONStringValue(item["call_id"])
		if callID == "" {
			continue
		}
		switch rawItemType(item) {
		case "function_call", "custom_tool_call", "local_shell_call", "tool_search_call":
			callIDs[callID] = true
		}
	}

	if len(callIDs) == 0 {
		return items, false, nil
	}

	drop := make(map[int]bool)
	for i, rawItem := range items {
		item, err := decodeRawResponsesItem(rawItem)
		if err != nil {
			return nil, false, fmt.Errorf("decode structured item %d: %w", i, err)
		}
		callID, _ := rawJSONStringValue(item["call_id"])
		if callID == "" {
			continue
		}
		switch rawItemType(item) {
		case "function_call_output", "custom_tool_call_output", "tool_search_output":
			if !callIDs[callID] {
				drop[i] = true
			}
		}
	}

	if len(drop) == 0 {
		return items, false, nil
	}
	return filterRawResponsesItems(items, drop), true, nil
}

func dropStructuredSupersededOutputItems(items []json.RawMessage) ([]json.RawMessage, bool, error) {
	outputsByKey := make(map[string][]int)

	for i, rawItem := range items {
		item, err := decodeRawResponsesItem(rawItem)
		if err != nil {
			return nil, false, fmt.Errorf("decode structured item %d: %w", i, err)
		}

		callID, _ := rawJSONStringValue(item["call_id"])
		if callID == "" {
			continue
		}

		switch rawItemType(item) {
		case "function_call_output", "custom_tool_call_output":
			key := rawItemType(item) + "|" + callID
			outputsByKey[key] = append(outputsByKey[key], i)
		}
	}

	drop := make(map[int]bool)
	for _, indices := range outputsByKey {
		if len(indices) <= 1 {
			continue
		}
		for _, index := range indices[:len(indices)-1] {
			drop[index] = true
		}
	}

	if len(drop) == 0 {
		return items, false, nil
	}
	return filterRawResponsesItems(items, drop), true, nil
}

func filterRawResponsesItems(items []json.RawMessage, drop map[int]bool) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(items)-len(drop))
	for i, item := range items {
		if drop[i] {
			continue
		}
		out = append(out, item)
	}
	return out
}

func decodeRawResponsesItem(rawItem json.RawMessage) (map[string]json.RawMessage, error) {
	var item map[string]json.RawMessage
	if err := json.Unmarshal(rawItem, &item); err != nil {
		return nil, err
	}
	return item, nil
}

func rawItemType(item map[string]json.RawMessage) string {
	typ, _ := rawJSONStringValue(item["type"])
	return typ
}

func extractStructuredPath(raw string) string {
	if raw == "" {
		return ""
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return ""
	}
	path, _ := rawJSONStringValue(doc["file_path"])
	if path != "" {
		return path
	}
	path, _ = rawJSONStringValue(doc["path"])
	return path
}

func structuredSearchSignature(item map[string]json.RawMessage) (string, bool, error) {
	execution, _ := rawJSONStringValue(item["execution"])
	arguments, err := canonicalizeRawJSON(item["arguments"])
	if err != nil {
		return "", false, err
	}
	if execution == "" && arguments == "" {
		return "", false, nil
	}
	return execution + "|" + arguments, true, nil
}

func canonicalizeRawJSON(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func isStructuredReadToolName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read", "read_file", "readfile", "view":
		return true
	default:
		return false
	}
}

func isStructuredWriteToolName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "write", "edit", "write_file", "writefile", "notebookedit":
		return true
	default:
		return false
	}
}

func mergeFilterNames(base, extra []string) []string {
	if len(extra) == 0 {
		return append([]string(nil), base...)
	}
	out := append([]string(nil), base...)
	seen := make(map[string]bool, len(out))
	for _, name := range out {
		seen[name] = true
	}
	for _, name := range extra {
		if seen[name] {
			continue
		}
		out = append(out, name)
		seen[name] = true
	}
	return out
}
