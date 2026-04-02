package neurorouter

import (
	"encoding/json"
	"fmt"
	"strings"
)

const instructionMessageSource = "instructions"

const (
	structuredShellOutputHeadShare   = 1
	structuredShellOutputShareTotal  = 3
	defaultStructuredReadOutputMax   = 16 * 1024
	defaultStructuredReadHistoryMax  = 16 * 1024
	defaultStructuredReadKeepRecent  = 3
	defaultStructuredSearchOutputMax = 8 * 1024
	defaultStructuredSearchToolsKeep = 8
	defaultStructuredToolsSchemaMax  = 12 * 1024
	defaultToolDescriptionMax        = 512
	defaultToolSchemaDescriptionMax  = 256
)

// codexCompactionSummaryPrefix mirrors Codex's SUMMARY_PREFIX template so we can
// identify exact compaction-summary messages without fuzzy heuristics.
const codexCompactionSummaryPrefix = "Another language model started to solve this problem and produced a summary of its thinking process. You also have access to the state of the tools that were used by that language model. Use this to build on the work that has already been done and avoid duplicating work. Here is the summary produced by the other language model, use the information in this summary to assist with your own analysis:"

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

	originalSources := make(map[string]struct{}, len(originalMsgs))
	originalBySource := make(map[string]ChatMessage, len(originalMsgs))
	for _, msg := range originalMsgs {
		if msg.Source == "" {
			continue
		}
		originalSources[msg.Source] = struct{}{}
		originalBySource[msg.Source] = msg
	}

	processedBySource := make(map[string]ChatMessage, len(processedMsgs))
	for _, msg := range processedMsgs {
		if msg.Source == "" {
			continue
		}
		processedBySource[msg.Source] = msg
	}

	changed := false
	if _, ok := originalSources[instructionMessageSource]; ok {
		if msg, ok := processedBySource[instructionMessageSource]; ok && msg.Content != "" {
			if msg.Content != originalBySource[instructionMessageSource].Content {
				changed = true
			}
			doc["instructions"] = marshalRawJSONString(msg.Content)
		} else {
			changed = true
			delete(doc, "instructions")
		}
	}

	rawInput, ok := doc["input"]
	if !ok {
		if !changed {
			return &ResponsesRewriteResult{
				Body:        append([]byte(nil), rawBody...),
				BytesBefore: len(rawBody),
				BytesAfter:  len(rawBody),
			}, nil
		}
		body, err := json.Marshal(doc)
		if err != nil {
			return nil, fmt.Errorf("marshal responses request without input: %w", err)
		}
		return &ResponsesRewriteResult{
			Body:        body,
			BytesBefore: len(rawBody),
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

		original := originalBySource[source]
		if msg, ok := processedBySource[source]; ok {
			if msg.Content == original.Content {
				updated = append(updated, rawItem)
				continue
			}
		}

		newText := ""
		if msg, ok := processedBySource[source]; ok {
			newText = msg.Content
		}
		rewritten, keep, err := rewriteRawMessageItem(rawItem, newText)
		if err != nil {
			return nil, fmt.Errorf("rewrite input item %d: %w", i, err)
		}
		changed = true
		if keep {
			updated = append(updated, rewritten)
		}
	}

	structuredFilters, err := cleanupStructuredResponsesItems(updated, cfg)
	if err != nil {
		return nil, err
	}
	updated = structuredFilters.Items
	if len(structuredFilters.FiltersRun) > 0 {
		changed = true
	}

	var toolFilters []string
	if cfg.OversizedBlocks {
		if rawTools, ok := doc["tools"]; ok {
			compactedTools, toolsChanged, err := compactStructuredOversizedToolDefinitions(rawTools, defaultStructuredToolsSchemaMax)
			if err != nil {
				return nil, fmt.Errorf("compact tools schema: %w", err)
			}
			if toolsChanged {
				doc["tools"] = compactedTools
				changed = true
				toolFilters = append(toolFilters, "oversized_blocks")
			}
		}
	}

	if !changed {
		return &ResponsesRewriteResult{
			Body:        append([]byte(nil), rawBody...),
			BytesBefore: len(rawBody),
			BytesAfter:  len(rawBody),
		}, nil
	}

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
		BytesBefore: len(rawBody),
		BytesAfter:  len(body),
		FiltersRun:  mergeFilterNames(structuredFilters.FiltersRun, toolFilters),
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

	if cfg.SystemReminders {
		next, changed, err := dropStructuredDuplicateCompactionSummaries(out)
		if err != nil {
			return nil, err
		}
		out = next
		if changed {
			filters = append(filters, "system_reminders")
		}
	}

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

		next, shellChanged, err := dropStructuredStaleShellItems(out)
		if err != nil {
			return nil, err
		}
		out = next
		changed = changed || shellChanged

		if changed {
			filters = append(filters, "stale_reads")
		}
	}

	if cfg.FailedRetries {
		next, changed, err := dropStructuredFailedShellRetryItems(out)
		if err != nil {
			return nil, err
		}
		out = next
		if changed {
			filters = append(filters, "failed_retries")
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

	oversizedChanged := false
	if cfg.OversizedBlocks {
		next, changed, err := truncateStructuredOversizedReadFunctionOutputItems(out, defaultStructuredReadOutputMax)
		if err != nil {
			return nil, err
		}
		out = next
		oversizedChanged = oversizedChanged || changed

		next, changed, err = budgetStructuredHistoricalReadFunctionOutputItems(out, defaultStructuredReadHistoryMax, defaultStructuredReadKeepRecent)
		if err != nil {
			return nil, err
		}
		out = next
		oversizedChanged = oversizedChanged || changed

		next, changed, err = compactStructuredOversizedSearchOutputItems(out, defaultStructuredSearchOutputMax, defaultStructuredSearchToolsKeep)
		if err != nil {
			return nil, err
		}
		out = next
		oversizedChanged = oversizedChanged || changed
	}

	if cfg.StructuredShellMaxBytes > 0 {
		next, changed, err := truncateStructuredOversizedShellOutputItems(out, cfg.StructuredShellMaxBytes)
		if err != nil {
			return nil, err
		}
		out = next
		oversizedChanged = oversizedChanged || changed
	}

	if oversizedChanged {
		filters = append(filters, "oversized_blocks")
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

type structuredShellRecord struct {
	Index     int
	CallID    string
	Signature string
}

type structuredShellAttempt struct {
	Index         int
	CallID        string
	Signature     string
	OutputIndices []int
	Failed        bool
	Succeeded     bool
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

func dropStructuredStaleShellItems(items []json.RawMessage) ([]json.RawMessage, bool, error) {
	shells := make(map[string][]structuredShellRecord)
	outputsByCallID := make(map[string][]int)
	outputSignaturesByCallID := make(map[string][]string)

	for i, rawItem := range items {
		item, err := decodeRawResponsesItem(rawItem)
		if err != nil {
			return nil, false, fmt.Errorf("decode structured item %d: %w", i, err)
		}

		callID, _ := rawJSONStringValue(item["call_id"])
		switch rawItemType(item) {
		case "local_shell_call", "shell_call":
			if callID == "" {
				continue
			}
			signature, ok, err := structuredShellSignature(item)
			if err != nil {
				return nil, false, fmt.Errorf("shell signature for item %d: %w", i, err)
			}
			if !ok {
				continue
			}
			shells[signature] = append(shells[signature], structuredShellRecord{
				Index:     i,
				CallID:    callID,
				Signature: signature,
			})
		case "shell_call_output", "local_shell_call_output":
			if callID == "" {
				continue
			}
			signature, ok, err := structuredShellOutputSignature(item)
			if err != nil {
				return nil, false, fmt.Errorf("shell output signature for item %d: %w", i, err)
			}
			if !ok {
				continue
			}
			outputsByCallID[callID] = append(outputsByCallID[callID], i)
			outputSignaturesByCallID[callID] = append(outputSignaturesByCallID[callID], signature)
		}
	}

	drop := make(map[int]bool)
	for _, records := range shells {
		if len(records) <= 1 {
			continue
		}
		latest := records[len(records)-1]
		latestOutputSignatures, ok := outputSignaturesByCallID[latest.CallID]
		if !ok || len(latestOutputSignatures) == 0 {
			continue
		}
		for _, record := range records[:len(records)-1] {
			if !equalStringSlices(outputSignaturesByCallID[record.CallID], latestOutputSignatures) {
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

func dropStructuredFailedShellRetryItems(items []json.RawMessage) ([]json.RawMessage, bool, error) {
	attemptsBySignature := make(map[string][]*structuredShellAttempt)
	attemptsByCallID := make(map[string]*structuredShellAttempt)

	for i, rawItem := range items {
		item, err := decodeRawResponsesItem(rawItem)
		if err != nil {
			return nil, false, fmt.Errorf("decode structured item %d: %w", i, err)
		}

		callID, _ := rawJSONStringValue(item["call_id"])
		switch rawItemType(item) {
		case "local_shell_call", "shell_call":
			if callID == "" {
				continue
			}
			signature, ok, err := structuredShellSignature(item)
			if err != nil {
				return nil, false, fmt.Errorf("shell signature for item %d: %w", i, err)
			}
			if !ok {
				continue
			}
			attempt := &structuredShellAttempt{
				Index:     i,
				CallID:    callID,
				Signature: signature,
			}
			attemptsBySignature[signature] = append(attemptsBySignature[signature], attempt)
			attemptsByCallID[callID] = attempt
		case "shell_call_output", "local_shell_call_output":
			if callID == "" {
				continue
			}
			attempt := attemptsByCallID[callID]
			if attempt == nil {
				continue
			}
			failed, succeeded, err := structuredShellOutputOutcome(item)
			if err != nil {
				return nil, false, fmt.Errorf("shell output outcome for item %d: %w", i, err)
			}
			attempt.OutputIndices = append(attempt.OutputIndices, i)
			attempt.Failed = attempt.Failed || failed
			attempt.Succeeded = attempt.Succeeded || succeeded
		}
	}

	drop := make(map[int]bool)
	for _, attempts := range attemptsBySignature {
		if len(attempts) <= 1 {
			continue
		}
		laterSuccess := false
		for i := len(attempts) - 1; i >= 0; i-- {
			attempt := attempts[i]
			if attempt.Succeeded && !attempt.Failed && len(attempt.OutputIndices) > 0 {
				laterSuccess = true
				continue
			}
			if !laterSuccess {
				continue
			}
			if !attempt.Failed || attempt.Succeeded || len(attempt.OutputIndices) == 0 {
				continue
			}
			drop[attempt.Index] = true
			for _, outputIndex := range attempt.OutputIndices {
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

func truncateStructuredOversizedShellOutputItems(items []json.RawMessage, threshold int) ([]json.RawMessage, bool, error) {
	out := append([]json.RawMessage(nil), items...)
	changed := false

	for i, rawItem := range items {
		rewritten, itemChanged, err := truncateStructuredOversizedShellOutputItem(rawItem, threshold)
		if err != nil {
			return nil, false, fmt.Errorf("truncate structured item %d: %w", i, err)
		}
		if !itemChanged {
			continue
		}
		out[i] = rewritten
		changed = true
	}

	if !changed {
		return items, false, nil
	}
	return out, true, nil
}

func truncateStructuredOversizedReadFunctionOutputItems(items []json.RawMessage, threshold int) ([]json.RawMessage, bool, error) {
	out := append([]json.RawMessage(nil), items...)
	changed := false

	for i, rawItem := range items {
		rewritten, itemChanged, err := truncateStructuredOversizedReadFunctionOutputItem(rawItem, threshold)
		if err != nil {
			return nil, false, fmt.Errorf("truncate read-style function output item %d: %w", i, err)
		}
		if !itemChanged {
			continue
		}
		out[i] = rewritten
		changed = true
	}

	if !changed {
		return items, false, nil
	}
	return out, true, nil
}

type structuredReadOutputRecord struct {
	Index        int
	Output       string
	Body         string
	MinBodyBytes int
}

func budgetStructuredHistoricalReadFunctionOutputItems(items []json.RawMessage, threshold, keepRecent int) ([]json.RawMessage, bool, error) {
	if threshold <= 0 {
		return items, false, nil
	}

	var records []structuredReadOutputRecord
	for i, rawItem := range items {
		item, err := decodeRawResponsesItem(rawItem)
		if err != nil {
			return nil, false, fmt.Errorf("decode structured item %d: %w", i, err)
		}

		switch rawItemType(item) {
		case "function_call_output", "custom_tool_call_output":
		default:
			continue
		}

		output, err := rawJSONStringValue(item["output"])
		if err != nil || output == "" {
			continue
		}

		command, _, body, ok := parseReadStyleFunctionTranscript(output)
		if !ok || !isReadStyleFunctionCommand(command) {
			continue
		}

		_, minBodyBytes, changed := truncateReadStyleFunctionTranscriptToBodyBudget(output, 1)
		if !changed {
			minBodyBytes = len(body)
		}

		records = append(records, structuredReadOutputRecord{
			Index:        i,
			Output:       output,
			Body:         body,
			MinBodyBytes: minBodyBytes,
		})
	}

	if len(records) <= keepRecent {
		return items, false, nil
	}

	older := records[:len(records)-keepRecent]
	totalOlderBytes := 0
	for _, record := range older {
		totalOlderBytes += len(record.Body)
	}
	if totalOlderBytes <= threshold {
		return items, false, nil
	}

	out := append([]json.RawMessage(nil), items...)
	changed := false
	remainingBudget := threshold
	for i := len(older) - 1; i >= 0; i-- {
		record := older[i]
		reserve := 0
		for j := 0; j < i; j++ {
			reserve += older[j].MinBodyBytes
		}
		allowed := remainingBudget - reserve
		if allowed < record.MinBodyBytes {
			allowed = record.MinBodyBytes
		}
		if len(record.Body) <= allowed {
			remainingBudget -= len(record.Body)
			continue
		}

		rewrittenOutput, rewrittenBodyBytes, itemChanged := truncateReadStyleFunctionTranscriptToBodyBudget(record.Output, allowed)
		if !itemChanged {
			remainingBudget -= len(record.Body)
			continue
		}

		rewrittenItem, err := rewriteStructuredOutputItem(out[record.Index], rewrittenOutput)
		if err != nil {
			return nil, false, fmt.Errorf("rewrite read-style function output item %d: %w", record.Index, err)
		}

		out[record.Index] = rewrittenItem
		remainingBudget -= rewrittenBodyBytes
		changed = true
	}

	if !changed {
		return items, false, nil
	}
	return out, true, nil
}

func compactStructuredOversizedSearchOutputItems(items []json.RawMessage, threshold, maxTools int) ([]json.RawMessage, bool, error) {
	out := append([]json.RawMessage(nil), items...)
	changed := false

	for i, rawItem := range items {
		rewritten, itemChanged, err := compactStructuredOversizedSearchOutputItem(rawItem, threshold, maxTools)
		if err != nil {
			return nil, false, fmt.Errorf("compact search output item %d: %w", i, err)
		}
		if !itemChanged {
			continue
		}
		out[i] = rewritten
		changed = true
	}

	if !changed {
		return items, false, nil
	}
	return out, true, nil
}

func compactStructuredOversizedToolDefinitions(rawTools json.RawMessage, threshold int) (json.RawMessage, bool, error) {
	if threshold <= 0 || len(rawTools) <= threshold {
		return rawTools, false, nil
	}

	var tools []any
	if err := json.Unmarshal(rawTools, &tools); err != nil {
		return rawTools, false, nil
	}

	changed := false
	for i := range tools {
		tool, toolChanged := compactStructuredToolDefinition(tools[i])
		if !toolChanged {
			continue
		}
		tools[i] = tool
		changed = true
	}
	if !changed {
		return rawTools, false, nil
	}

	out, err := json.Marshal(tools)
	if err != nil {
		return nil, false, err
	}
	if len(out) >= len(rawTools) {
		return rawTools, false, nil
	}
	return out, true, nil
}

func compactStructuredOversizedSearchOutputItem(rawItem json.RawMessage, threshold, maxTools int) (json.RawMessage, bool, error) {
	if threshold <= 0 || maxTools <= 0 {
		return rawItem, false, nil
	}

	item, err := decodeRawResponsesItem(rawItem)
	if err != nil {
		return nil, false, err
	}

	if rawItemType(item) != "tool_search_output" {
		return rawItem, false, nil
	}

	rawTools, ok := item["tools"]
	if !ok || len(rawTools) <= threshold {
		return rawItem, false, nil
	}

	var tools []json.RawMessage
	if err := json.Unmarshal(rawTools, &tools); err != nil {
		return rawItem, false, nil
	}
	if len(tools) <= maxTools {
		return rawItem, false, nil
	}

	item["tools"], err = json.Marshal(tools[:maxTools])
	if err != nil {
		return nil, false, err
	}

	out, err := json.Marshal(item)
	if err != nil {
		return nil, false, err
	}
	if len(out) >= len(rawItem) {
		return rawItem, false, nil
	}
	return out, true, nil
}

func compactStructuredToolDefinition(tool any) (any, bool) {
	m, ok := tool.(map[string]any)
	if !ok {
		return tool, false
	}

	changed := compactToolMetadataObject(m, defaultToolDescriptionMax, defaultToolSchemaDescriptionMax)
	if nested, ok := m["function"].(map[string]any); ok {
		if compactToolMetadataObject(nested, defaultToolDescriptionMax, defaultToolSchemaDescriptionMax) {
			m["function"] = nested
			changed = true
		}
	}
	return m, changed
}

func compactToolMetadataObject(obj map[string]any, descriptionMax, schemaDescriptionMax int) bool {
	changed := false

	if description, ok := obj["description"].(string); ok {
		compacted := compactToolDescription(description, descriptionMax)
		if compacted != description {
			obj["description"] = compacted
			changed = true
		}
	}

	if schema, ok := obj["parameters"].(map[string]any); ok {
		if compactStructuredToolSchema(schema, schemaDescriptionMax) {
			obj["parameters"] = schema
			changed = true
		}
	}

	return changed
}

func compactStructuredToolSchema(node any, descriptionMax int) bool {
	switch typed := node.(type) {
	case []any:
		changed := false
		for i := range typed {
			if compactStructuredToolSchema(typed[i], descriptionMax) {
				changed = true
			}
		}
		return changed
	case map[string]any:
		changed := false
		for key, value := range typed {
			switch key {
			case "description":
				description, ok := value.(string)
				if !ok {
					continue
				}
				compacted := compactToolDescription(description, descriptionMax)
				if compacted != description {
					typed[key] = compacted
					changed = true
				}
				continue
			case "title":
				title, ok := value.(string)
				if ok && strings.TrimSpace(title) != "" {
					delete(typed, key)
					changed = true
				}
				continue
			}
			if compactStructuredToolSchema(value, descriptionMax) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func truncateStructuredOversizedReadFunctionOutputItem(rawItem json.RawMessage, threshold int) (json.RawMessage, bool, error) {
	item, err := decodeRawResponsesItem(rawItem)
	if err != nil {
		return nil, false, err
	}

	switch rawItemType(item) {
	case "function_call_output", "custom_tool_call_output":
	default:
		return rawItem, false, nil
	}

	output, err := rawJSONStringValue(item["output"])
	if err != nil || output == "" {
		return rawItem, false, nil
	}

	rewrittenOutput, changed := truncateReadStyleFunctionTranscript(output, threshold)
	if !changed {
		return rawItem, false, nil
	}

	out, err := rewriteStructuredOutputItem(rawItem, rewrittenOutput)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func truncateStructuredOversizedShellOutputItem(rawItem json.RawMessage, threshold int) (json.RawMessage, bool, error) {
	item, err := decodeRawResponsesItem(rawItem)
	if err != nil {
		return nil, false, err
	}

	switch rawItemType(item) {
	case "shell_call_output", "local_shell_call_output":
	default:
		return rawItem, false, nil
	}

	output, err := rawJSONStringValue(item["output"])
	if err != nil || output == "" || len(output) <= threshold {
		return rawItem, false, nil
	}

	item["output"] = marshalRawJSONString(truncateStructuredShellOutput(output, threshold))
	out, err := json.Marshal(item)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func rewriteStructuredOutputItem(rawItem json.RawMessage, output string) (json.RawMessage, error) {
	item, err := decodeRawResponsesItem(rawItem)
	if err != nil {
		return nil, err
	}
	item["output"] = marshalRawJSONString(output)
	out, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func dropStructuredDuplicateCompactionSummaries(items []json.RawMessage) ([]json.RawMessage, bool, error) {
	summaries := make(map[string][]int)

	for i, rawItem := range items {
		item, err := decodeRawResponsesItem(rawItem)
		if err != nil {
			return nil, false, fmt.Errorf("decode structured item %d: %w", i, err)
		}
		if rawItemType(item) != "message" {
			continue
		}

		role, _ := rawJSONStringValue(item["role"])
		if role != "user" {
			continue
		}

		text, ok, err := structuredMessageText(item["content"])
		if err != nil {
			return nil, false, fmt.Errorf("extract summary text for item %d: %w", i, err)
		}
		if !ok || !isCodexCompactionSummaryMessage(text) {
			continue
		}
		summaries[text] = append(summaries[text], i)
	}

	drop := make(map[int]bool)
	for _, indices := range summaries {
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

func structuredShellSignature(item map[string]json.RawMessage) (string, bool, error) {
	switch rawItemType(item) {
	case "local_shell_call", "shell_call":
	default:
		return "", false, nil
	}
	signature, err := canonicalizeRawItemWithoutFields(item, "call_id", "id", "status")
	if err != nil {
		return "", false, err
	}
	if signature == "" {
		return "", false, nil
	}
	return signature, true, nil
}

func structuredShellOutputSignature(item map[string]json.RawMessage) (string, bool, error) {
	switch rawItemType(item) {
	case "shell_call_output", "local_shell_call_output":
	default:
		return "", false, nil
	}
	signature, err := canonicalizeRawItemWithoutFields(item, "call_id", "id")
	if err != nil {
		return "", false, err
	}
	if signature == "" {
		return "", false, nil
	}
	return signature, true, nil
}

func structuredShellOutputOutcome(item map[string]json.RawMessage) (bool, bool, error) {
	exitCode, hasExitCode, err := rawJSONIntValue(item["exit_code"])
	if err != nil {
		return false, false, err
	}
	if !hasExitCode {
		exitCode, hasExitCode, err = rawJSONIntValue(item["exitCode"])
		if err != nil {
			return false, false, err
		}
	}

	status, _ := rawJSONStringValue(item["status"])
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error", "cancelled", "canceled":
		return true, false, nil
	}

	if hasExitCode {
		if exitCode != 0 {
			return true, false, nil
		}
		return false, true, nil
	}

	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "succeeded", "success":
		return false, true, nil
	default:
		return false, false, nil
	}
}

func truncateStructuredShellOutput(output string, threshold int) string {
	if threshold <= 0 || len(output) <= threshold {
		return output
	}

	marker := "\n[truncated by neurorouter — original " + formatBytes(len(output)) + "]\n"
	available := threshold - len(marker)
	if available <= 0 {
		return marker
	}

	headBytes := available * structuredShellOutputHeadShare / structuredShellOutputShareTotal
	tailBytes := available - headBytes
	if headBytes == 0 {
		return marker + output[len(output)-tailBytes:]
	}
	if tailBytes == 0 {
		return output[:headBytes] + marker
	}
	return output[:headBytes] + marker + output[len(output)-tailBytes:]
}

func truncateReadStyleFunctionTranscript(output string, threshold int) (string, bool) {
	command, prefix, body, ok := parseReadStyleFunctionTranscript(output)
	if !ok || len(body) <= threshold || !isReadStyleFunctionCommand(command) {
		return "", false
	}

	rewritten := prefix + truncateStructuredShellOutput(body, threshold)
	if len(rewritten) >= len(output) {
		return "", false
	}
	return rewritten, true
}

func truncateReadStyleFunctionTranscriptToBodyBudget(output string, budget int) (string, int, bool) {
	command, _, body, ok := parseReadStyleFunctionTranscript(output)
	if !ok || !isReadStyleFunctionCommand(command) {
		return "", 0, false
	}
	if len(body) <= budget {
		return output, len(body), false
	}

	lowestOutput, changed := truncateReadStyleFunctionTranscript(output, 1)
	if !changed {
		return "", 0, false
	}
	_, _, lowestBody, ok := parseReadStyleFunctionTranscript(lowestOutput)
	if !ok {
		return "", 0, false
	}
	if budget <= len(lowestBody) {
		return lowestOutput, len(lowestBody), true
	}

	low := 1
	high := len(body) - 1
	bestOutput := lowestOutput
	bestBodyBytes := len(lowestBody)
	for low <= high {
		mid := (low + high) / 2
		candidateOutput, candidateChanged := truncateReadStyleFunctionTranscript(output, mid)
		if !candidateChanged {
			high = mid - 1
			continue
		}

		_, _, candidateBody, ok := parseReadStyleFunctionTranscript(candidateOutput)
		if !ok {
			return "", 0, false
		}

		if len(candidateBody) > budget {
			high = mid - 1
			continue
		}

		bestOutput = candidateOutput
		bestBodyBytes = len(candidateBody)
		low = mid + 1
	}
	return bestOutput, bestBodyBytes, true
}

func compactToolDescription(description string, max int) string {
	normalized := strings.Join(strings.Fields(description), " ")
	if max <= 0 || len(normalized) <= max {
		return normalized
	}
	if max <= 3 {
		return normalized[:max]
	}

	cut := max - 3
	if boundary := strings.LastIndexByte(normalized[:cut], ' '); boundary > max/2 {
		cut = boundary
	}
	return strings.TrimSpace(normalized[:cut]) + "..."
}

func parseReadStyleFunctionTranscript(output string) (string, string, string, bool) {
	if !strings.HasPrefix(output, "Command: ") {
		return "", "", "", false
	}

	newline := strings.IndexByte(output, '\n')
	if newline == -1 {
		return "", "", "", false
	}
	command := strings.TrimSpace(strings.TrimPrefix(output[:newline], "Command: "))
	if command == "" {
		return "", "", "", false
	}

	const outputMarker = "\nOutput:\n"
	markerIndex := strings.Index(output, outputMarker)
	if markerIndex == -1 {
		return "", "", "", false
	}

	prefixEnd := markerIndex + len(outputMarker)
	body := output[prefixEnd:]
	if body == "" {
		return "", "", "", false
	}
	return command, output[:prefixEnd], body, true
}

func isReadStyleFunctionCommand(command string) bool {
	normalized := strings.ToLower(strings.TrimSpace(command))
	return strings.Contains(normalized, "sed -n") ||
		strings.Contains(normalized, "nl -ba") ||
		strings.Contains(normalized, "cat ") ||
		strings.Contains(normalized, "rg -n") ||
		strings.Contains(normalized, "rg --files") ||
		strings.Contains(normalized, "find ") ||
		strings.Contains(normalized, "wc -l") ||
		strings.Contains(normalized, "ls ") ||
		strings.Contains(normalized, "head ") ||
		strings.Contains(normalized, "tail ") ||
		strings.Contains(normalized, "git show")
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

func rawJSONIntValue(raw json.RawMessage) (int, bool, error) {
	if len(raw) == 0 {
		return 0, false, nil
	}
	var out int
	if err := json.Unmarshal(raw, &out); err == nil {
		return out, true, nil
	}
	var asFloat float64
	if err := json.Unmarshal(raw, &asFloat); err == nil {
		out = int(asFloat)
		if float64(out) == asFloat {
			return out, true, nil
		}
	}
	return 0, false, fmt.Errorf("decode int value")
}

func canonicalizeRawItemWithoutFields(item map[string]json.RawMessage, fields ...string) (string, error) {
	if len(item) == 0 {
		return "", nil
	}
	clone := make(map[string]json.RawMessage, len(item))
	for key, value := range item {
		clone[key] = append(json.RawMessage(nil), value...)
	}
	for _, field := range fields {
		delete(clone, field)
	}
	data, err := json.Marshal(clone)
	if err != nil {
		return "", err
	}
	return canonicalizeRawJSON(data)
}

func structuredMessageText(raw json.RawMessage) (string, bool, error) {
	if len(raw) == 0 {
		return "", false, nil
	}
	text, err := extractContent(raw)
	if err != nil {
		return "", false, err
	}
	if text == "" {
		return "", false, nil
	}
	return text, true, nil
}

func isCodexCompactionSummaryMessage(text string) bool {
	return strings.HasPrefix(text, codexCompactionSummaryPrefix+"\n")
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

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
