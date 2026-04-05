package neurorouter

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Filter transforms a message slice, removing or modifying noise.
type Filter func([]ChatMessage) []ChatMessage

// NamedFilter pairs a filter function with its name for metrics.
type NamedFilter struct {
	Name  string
	Apply Filter
}

// FilterChain runs filters in sequence.
type FilterChain struct {
	Filters []NamedFilter
}

// FilterResult holds metrics from running a filter chain.
type FilterResult struct {
	BytesBefore int
	BytesAfter  int
	Applied     []string // names of filters that modified output
}

// FilterConfig controls which filters are enabled.
type FilterConfig struct {
	StaleReads              bool
	Thinking                bool
	OrphanedResults         bool
	FailedRetries           bool
	SystemReminders         bool
	OversizedBlocks         bool
	MaxBlockBytes           int // 0 = default 100KB
	StructuredShellMaxBytes int // 0 = disabled for native shell outputs
}

const defaultMaxBlockBytes = 100 * 1024

// NewFilterChain creates a chain from config.
func NewFilterChain(cfg FilterConfig) *FilterChain {
	var filters []NamedFilter

	if cfg.OversizedBlocks {
		threshold := cfg.MaxBlockBytes
		if threshold <= 0 {
			threshold = defaultMaxBlockBytes
		}
		filters = append(filters, NamedFilter{Name: "oversized_blocks", Apply: FilterOversizedBlocks(threshold)})
	}
	if cfg.Thinking {
		filters = append(filters, NamedFilter{Name: "thinking", Apply: FilterThinking})
	}
	if cfg.SystemReminders {
		filters = append(filters, NamedFilter{Name: "system_reminders", Apply: FilterSystemReminders})
	}
	if cfg.StaleReads {
		filters = append(filters, NamedFilter{Name: "stale_reads", Apply: FilterStaleReads})
	}
	if cfg.OrphanedResults {
		filters = append(filters, NamedFilter{Name: "orphaned_results", Apply: FilterOrphanedResults})
	}
	if cfg.FailedRetries {
		filters = append(filters, NamedFilter{Name: "failed_retries", Apply: FilterFailedRetries})
	}

	if len(filters) == 0 {
		return nil
	}
	return &FilterChain{Filters: filters}
}

// Run executes all filters in sequence and returns metrics.
func (fc *FilterChain) Run(msgs []ChatMessage) ([]ChatMessage, *FilterResult) {
	result := &FilterResult{BytesBefore: messageBytes(msgs)}

	for _, f := range fc.Filters {
		before := len(msgs)
		beforeBytes := messageBytes(msgs)
		msgs = f.Apply(msgs)
		if len(msgs) != before || messageBytes(msgs) != beforeBytes {
			result.Applied = append(result.Applied, f.Name)
		}
	}

	result.BytesAfter = messageBytes(msgs)
	return msgs, result
}

func messageBytes(msgs []ChatMessage) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Role) + len(m.Content)
	}
	return n
}

const defaultClaudeReadToolResultMaxBytes = 4 * 1024

// FilterOversizedBlocks truncates messages with Content exceeding threshold.
func FilterOversizedBlocks(threshold int) Filter {
	return func(msgs []ChatMessage) []ChatMessage {
		shellTools := claudeShellToolUses(msgs)
		readTools := claudeReadToolUses(msgs)
		out := make([]ChatMessage, len(msgs))
		for i, m := range msgs {
			if shaped, changed := shapeClaudeToolResultMessage(m.Content, threshold, shellTools, readTools); changed {
				out[i] = ChatMessage{
					Role:    m.Role,
					Content: shaped,
					Source:  m.Source,
				}
				continue
			}
			if len(m.Content) > threshold {
				out[i] = ChatMessage{
					Role:    m.Role,
					Content: m.Content[:threshold] + "\n[truncated by neurorouter — original " + formatBytes(len(m.Content)) + "]",
					Source:  m.Source,
				}
			} else {
				out[i] = m
			}
		}
		return out
	}
}

type claudeShellTextField struct {
	name string
	text string
}

func claudeShellToolUses(msgs []ChatMessage) map[string]struct{} {
	return claudeToolUsesWhere(msgs, isClaudeShellToolName)
}

func claudeReadToolUses(msgs []ChatMessage) map[string]struct{} {
	return claudeToolUsesWhere(msgs, isClaudeReadToolName)
}

func claudeToolUsesWhere(msgs []ChatMessage, keep func(string) bool) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, m := range msgs {
		if m.Role != "assistant" {
			continue
		}

		var blocks []map[string]json.RawMessage
		if err := json.Unmarshal([]byte(m.Content), &blocks); err != nil {
			continue
		}
		for _, block := range blocks {
			blockType, err := rawJSONStringValue(block["type"])
			if err != nil || blockType != "tool_use" {
				continue
			}
			name, err := rawJSONStringValue(block["name"])
			if err != nil || !keep(name) {
				continue
			}
			id, err := rawJSONStringValue(block["id"])
			if err != nil || id == "" {
				continue
			}
			ids[id] = struct{}{}
		}
	}
	return ids
}

func isClaudeShellToolName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "bash", "powershell":
		return true
	default:
		return false
	}
}

func shapeClaudeToolResultMessage(content string, threshold int, shellTools, readTools map[string]struct{}) (string, bool) {
	if len(shellTools) == 0 && len(readTools) == 0 {
		return "", false
	}

	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &blocks); err != nil {
		return "", false
	}

	changed := false
	for _, block := range blocks {
		blockType, err := rawJSONStringValue(block["type"])
		if err != nil || blockType != "tool_result" {
			continue
		}
		toolUseID, err := rawJSONStringValue(block["tool_use_id"])
		if err != nil {
			continue
		}
		switch {
		case hasClaudeToolUse(shellTools, toolUseID):
			blockChanged, err := shapeClaudeShellToolResultBlock(block, threshold)
			if err != nil {
				continue
			}
			changed = changed || blockChanged
		case hasClaudeToolUse(readTools, toolUseID):
			blockChanged, err := shapeClaudeReadToolResultBlock(block, claudeReadToolResultBudget(threshold))
			if err != nil {
				continue
			}
			changed = changed || blockChanged
		}
	}

	if !changed {
		return "", false
	}

	rewritten, err := json.Marshal(blocks)
	if err != nil || len(rewritten) >= len(content) {
		return "", false
	}
	return string(rewritten), true
}

func hasClaudeToolUse(ids map[string]struct{}, toolUseID string) bool {
	if len(ids) == 0 {
		return false
	}
	_, ok := ids[toolUseID]
	return ok
}

func shapeClaudeShellToolResultBlock(block map[string]json.RawMessage, threshold int) (bool, error) {
	rawContent, ok := block["content"]
	if !ok || len(rawContent) == 0 {
		return false, nil
	}

	var textValue string
	if err := json.Unmarshal(rawContent, &textValue); err == nil {
		shaped, changed := shapeClaudeShellJSONText(textValue, threshold)
		if !changed {
			return false, nil
		}
		block["content"] = marshalRawJSONString(shaped)
		return true, nil
	}

	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(rawContent, &parts); err != nil {
		return false, nil
	}

	changed := false
	for _, part := range parts {
		partType, err := rawJSONStringValue(part["type"])
		if err != nil {
			continue
		}
		switch partType {
		case "text":
			text, err := rawJSONStringValue(part["text"])
			if err != nil || text == "" {
				continue
			}
			shaped, partChanged := shapeClaudeShellJSONText(text, threshold)
			if !partChanged {
				continue
			}
			part["text"] = marshalRawJSONString(shaped)
			changed = true
		case "json":
			rawValue, ok := part["value"]
			if !ok || len(rawValue) == 0 {
				continue
			}
			var value map[string]json.RawMessage
			if err := json.Unmarshal(rawValue, &value); err != nil {
				continue
			}
			partChanged, err := shapeClaudeShellJSONFields(value, threshold)
			if err != nil || !partChanged {
				continue
			}
			part["value"] = marshalRawValue(value)
			changed = true
		}
	}

	if !changed {
		return false, nil
	}
	block["content"] = marshalRawValue(parts)
	return true, nil
}

func claudeReadToolResultBudget(threshold int) int {
	budget := defaultClaudeReadToolResultMaxBytes
	if threshold > 0 && threshold/4 > 0 && threshold/4 < budget {
		budget = threshold / 4
	}
	return budget
}

func shapeClaudeReadToolResultBlock(block map[string]json.RawMessage, budget int) (bool, error) {
	if budget <= 0 {
		return false, nil
	}

	rawContent, ok := block["content"]
	if !ok || len(rawContent) == 0 {
		return false, nil
	}

	var textValue string
	if err := json.Unmarshal(rawContent, &textValue); err == nil {
		if len(textValue) <= budget {
			return false, nil
		}
		block["content"] = marshalRawJSONString(truncateStructuredShellOutput(textValue, budget))
		return true, nil
	}

	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(rawContent, &parts); err != nil {
		return false, nil
	}

	changed := false
	for _, part := range parts {
		partType, err := rawJSONStringValue(part["type"])
		if err != nil || partType != "text" {
			continue
		}
		text, err := rawJSONStringValue(part["text"])
		if err != nil || len(text) <= budget {
			continue
		}
		part["text"] = marshalRawJSONString(truncateStructuredShellOutput(text, budget))
		changed = true
	}
	if !changed {
		return false, nil
	}
	block["content"] = marshalRawValue(parts)
	return true, nil
}

func shapeClaudeShellJSONText(text string, threshold int) (string, bool) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return "", false
	}
	changed, err := shapeClaudeShellJSONFields(value, threshold)
	if err != nil || !changed {
		return "", false
	}
	rewritten, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return string(rewritten), true
}

func shapeClaudeShellJSONFields(obj map[string]json.RawMessage, threshold int) (bool, error) {
	var fields []claudeShellTextField
	for _, name := range []string{"stderr", "stdout"} {
		text, err := rawJSONStringValue(obj[name])
		if err != nil || text == "" {
			continue
		}
		fields = append(fields, claudeShellTextField{name: name, text: text})
	}

	if len(fields) == 0 {
		return false, nil
	}

	total := 0
	for _, field := range fields {
		total += len(field.text)
	}
	if total <= threshold {
		return false, nil
	}

	budgets := claudeShellFieldBudgets(fields, threshold)
	changed := false
	for _, field := range fields {
		budget := budgets[field.name]
		if budget <= 0 || len(field.text) <= budget {
			continue
		}
		obj[field.name] = marshalRawJSONString(truncateStructuredShellOutput(field.text, budget))
		changed = true
	}
	return changed, nil
}

func claudeShellFieldBudgets(fields []claudeShellTextField, threshold int) map[string]int {
	budgets := make(map[string]int, len(fields))
	if len(fields) == 0 {
		return budgets
	}
	if len(fields) == 1 {
		budgets[fields[0].name] = threshold
		return budgets
	}

	remaining := threshold
	remainingFields := len(fields)
	for _, field := range fields {
		fairShare := remaining / remainingFields
		if field.name == "stderr" && fairShare < threshold/2 {
			fairShare = threshold / 2
		}
		if fairShare > remaining {
			fairShare = remaining
		}
		if fairShare > len(field.text) {
			fairShare = len(field.text)
		}
		budgets[field.name] = fairShare
		remaining -= fairShare
		remainingFields--
	}

	if remaining > 0 {
		for _, preferred := range []string{"stdout", "stderr"} {
			for _, field := range fields {
				if field.name != preferred {
					continue
				}
				budgets[field.name] += remaining
				return budgets
			}
		}
	}

	return budgets
}

func formatBytes(n int) string {
	if n >= 1024*1024 {
		mb := float64(n) / (1024 * 1024)
		text := strconv.FormatFloat(mb, 'f', 1, 64)
		return strings.TrimRight(strings.TrimRight(text, "0"), ".") + "MB"
	}
	kb := n / 1024
	return itoa(kb) + "KB"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// thinkingBlockRe matches <thinking>...</thinking> blocks.
var thinkingBlockRe = regexp.MustCompile(`(?s)<thinking>.*?</thinking>`)

// thinkingJSONRe matches "type":"thinking" JSON content blocks.
var thinkingJSONRe = regexp.MustCompile(`(?s)\{[^}]*"type"\s*:\s*"thinking"[^}]*\}`)

// FilterThinking strips thinking blocks from message content.
func FilterThinking(msgs []ChatMessage) []ChatMessage {
	var out []ChatMessage
	for _, m := range msgs {
		if rewritten, changed, removeMessage := stripClaudeThinkingBlocks(m.Content); changed {
			if removeMessage {
				continue
			}
			out = append(out, ChatMessage{Role: m.Role, Content: rewritten, Source: m.Source})
			continue
		}

		content := thinkingBlockRe.ReplaceAllString(m.Content, "")
		content = thinkingJSONRe.ReplaceAllString(content, "")
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		out = append(out, ChatMessage{Role: m.Role, Content: content, Source: m.Source})
	}
	return out
}

func stripClaudeThinkingBlocks(content string) (string, bool, bool) {
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &blocks); err != nil {
		return "", false, false
	}

	rewritten := make([]map[string]json.RawMessage, 0, len(blocks))
	changed := false
	for _, block := range blocks {
		blockType, err := rawJSONStringValue(block["type"])
		if err != nil {
			rewritten = append(rewritten, block)
			continue
		}
		if blockType == "thinking" || blockType == "redacted_thinking" {
			changed = true
			continue
		}
		rewritten = append(rewritten, block)
	}

	if !changed {
		return "", false, false
	}
	if len(rewritten) == 0 {
		return "", true, true
	}

	encoded, err := json.Marshal(rewritten)
	if err != nil {
		return "", false, false
	}
	return string(encoded), true, false
}

// systemReminderRe matches <system-reminder>...</system-reminder> blocks.
var systemReminderRe = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

// FilterSystemReminders deduplicates system-reminder blocks, keeping the last occurrence.
func FilterSystemReminders(msgs []ChatMessage) []ChatMessage {
	out := msgs

	// First pass: find all reminders and track last occurrence.
	type loc struct {
		msgIdx int
		start  int
		end    int
	}
	reminderLocs := make(map[[32]byte][]loc)
	var allHashes [][32]byte

	for i, m := range msgs {
		matches := systemReminderRe.FindAllStringIndex(m.Content, -1)
		for _, match := range matches {
			text := m.Content[match[0]:match[1]]
			hash := sha256.Sum256([]byte(text))
			if _, exists := reminderLocs[hash]; !exists {
				allHashes = append(allHashes, hash)
			}
			reminderLocs[hash] = append(reminderLocs[hash], loc{msgIdx: i, start: match[0], end: match[1]})
		}
	}

	// Build set of (msgIdx, start, end) to remove — all but last occurrence of each.
	type removal struct {
		msgIdx int
		start  int
		end    int
	}
	var removals []removal
	for _, hash := range allHashes {
		locs := reminderLocs[hash]
		if len(locs) <= 1 {
			continue
		}
		// Remove all but last.
		for _, l := range locs[:len(locs)-1] {
			removals = append(removals, removal(l))
		}
	}

	if len(removals) > 0 {
		// Group removals by message index.
		perMsg := make(map[int][]removal)
		for _, r := range removals {
			perMsg[r.msgIdx] = append(perMsg[r.msgIdx], r)
		}

		rewritten := make([]ChatMessage, 0, len(msgs))
		for i, m := range msgs {
			rems, hasRemovals := perMsg[i]
			if !hasRemovals {
				rewritten = append(rewritten, m)
				continue
			}

			// Sort removals by start position descending for safe string surgery.
			for j := 0; j < len(rems)-1; j++ {
				for k := j + 1; k < len(rems); k++ {
					if rems[k].start > rems[j].start {
						rems[j], rems[k] = rems[k], rems[j]
					}
				}
			}

			content := m.Content
			for _, r := range rems {
				content = content[:r.start] + content[r.end:]
			}
			content = strings.TrimSpace(content)
			if content == "" {
				continue
			}
			rewritten = append(rewritten, ChatMessage{Role: m.Role, Content: content, Source: m.Source})
		}
		out = rewritten
	}
	return dedupeClaudeContinuationSummaries(out)
}

func dedupeClaudeContinuationSummaries(msgs []ChatMessage) []ChatMessage {
	summaryLocs := make(map[[32]byte][]int)
	var seenHashes [][32]byte

	for i, m := range msgs {
		text, ok := claudeContinuationSummaryText(m.Content)
		if !ok {
			continue
		}
		hash := sha256.Sum256([]byte(text))
		if _, exists := summaryLocs[hash]; !exists {
			seenHashes = append(seenHashes, hash)
		}
		summaryLocs[hash] = append(summaryLocs[hash], i)
	}

	if len(summaryLocs) == 0 {
		return msgs
	}

	drop := make(map[int]bool)
	for _, hash := range seenHashes {
		locs := summaryLocs[hash]
		if len(locs) <= 1 {
			continue
		}
		for _, idx := range locs[:len(locs)-1] {
			drop[idx] = true
		}
	}

	if len(drop) == 0 {
		return msgs
	}

	out := make([]ChatMessage, 0, len(msgs)-len(drop))
	for i, m := range msgs {
		if drop[i] {
			continue
		}
		out = append(out, m)
	}
	return out
}

func claudeContinuationSummaryText(content string) (string, bool) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "", false
	}
	if text, ok := anthropicTextOnlyBlocksFilterString(json.RawMessage(content)); ok {
		trimmed = strings.TrimSpace(text)
	}
	if !looksLikeClaudeContinuationSummary(trimmed) {
		return "", false
	}
	return trimmed, true
}

func looksLikeClaudeContinuationSummary(text string) bool {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) < 128 {
		return false
	}

	lower := strings.ToLower(trimmed)
	for _, marker := range []string{
		"[coalesced]",
		"continuation summary",
		"summary of the conversation so far",
		"summary of the conversation up to this point",
		"previous conversation summary",
		"this session is being continued from a previous conversation",
		"this conversation is continued from a previous context window",
		"compacted summary",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// readToolRe matches Read/read_file tool_use patterns and extracts file paths.
var readToolRe = regexp.MustCompile(`"name"\s*:\s*"(?:Read|read_file|View|ReadFile)"[^}]*"(?:file_path|path)"\s*:\s*"([^"]+)"`)

// writeToolRe matches Write/Edit/write_file tool_use patterns.
var writeToolRe = regexp.MustCompile(`"name"\s*:\s*"(?:Write|Edit|write_file|WriteFile|NotebookEdit)"[^}]*"(?:file_path|path)"\s*:\s*"([^"]+)"`)

// FilterStaleReads removes duplicate file reads while preserving the latest
// distinct read context for a file within each write-delimited segment.
type staleReadEvent struct {
	msgIdx    int
	signature string
	toolUseID string
}

type claudeToolUseEvent struct {
	id        string
	path      string
	signature string
	isRead    bool
	isWrite   bool
}

func FilterStaleReads(msgs []ChatMessage) []ChatMessage {
	lastRead := make(map[string][]staleReadEvent) // path → list of read events

	// First pass: collect all read and write events.
	writes := make(map[string][]int) // path → write indices
	for i, m := range msgs {
		if events, parsed := claudeToolUseEvents(m.Content); parsed {
			for _, event := range events {
				if event.path == "" {
					continue
				}
				if event.isWrite {
					writes[event.path] = append(writes[event.path], i)
				}
				if event.isRead {
					lastRead[event.path] = append(lastRead[event.path], staleReadEvent{
						msgIdx:    i,
						signature: event.signature,
						toolUseID: event.id,
					})
				}
			}
			continue
		}
		for _, match := range writeToolRe.FindAllStringSubmatch(m.Content, -1) {
			writes[match[1]] = append(writes[match[1]], i)
		}
		for _, match := range readToolRe.FindAllStringSubmatch(m.Content, -1) {
			lastRead[match[1]] = append(lastRead[match[1]], staleReadEvent{
				msgIdx:    i,
				signature: staleReadFallbackSignature(m.Content, match[1]),
			})
		}
	}

	// Determine which message indices to drop.
	drop := make(map[int]bool)
	staleToolUseIDs := make(map[string]struct{})
	for path, events := range lastRead {
		for _, event := range staleReadEventsToDrop(events, writes[path]) {
			if event.toolUseID != "" {
				staleToolUseIDs[event.toolUseID] = struct{}{}
				continue
			}
			drop[event.msgIdx] = true
		}
	}

	for id := range claudeDuplicateShellTranscriptToolUseIDs(msgs) {
		staleToolUseIDs[id] = struct{}{}
	}

	if len(drop) == 0 && len(staleToolUseIDs) == 0 {
		return msgs
	}

	out := make([]ChatMessage, 0, len(msgs))
	for i, m := range msgs {
		if drop[i] {
			continue
		}

		content := m.Content
		if len(staleToolUseIDs) > 0 {
			rewritten, changed, removeMessage := stripClaudeToolChainBlocks(m.Role, content, staleToolUseIDs)
			if removeMessage {
				continue
			}
			if changed {
				content = rewritten
			}
		}
		out = append(out, ChatMessage{Role: m.Role, Content: content, Source: m.Source})
	}
	return out
}

func staleReadEventsToDrop(events []staleReadEvent, writeIndices []int) []staleReadEvent {
	if len(events) <= 1 {
		return nil
	}

	stale := make([]staleReadEvent, 0, len(events)-1)
	readPos := 0
	for _, writeIdx := range writeIndices {
		segmentStart := readPos
		for readPos < len(events) && events[readPos].msgIdx < writeIdx {
			readPos++
		}
		stale = append(stale, staleReadSegmentDuplicates(events[segmentStart:readPos])...)
	}
	stale = append(stale, staleReadSegmentDuplicates(events[readPos:])...)
	return stale
}

func staleReadSegmentDuplicates(events []staleReadEvent) []staleReadEvent {
	if len(events) <= 1 {
		return nil
	}

	bySignature := make(map[string][]staleReadEvent)
	order := make([]string, 0, len(events))
	for _, event := range events {
		signature := strings.TrimSpace(event.signature)
		if signature == "" {
			signature = fmt.Sprintf("unique:%d", event.msgIdx)
		}
		if _, exists := bySignature[signature]; !exists {
			order = append(order, signature)
		}
		bySignature[signature] = append(bySignature[signature], event)
	}

	stale := make([]staleReadEvent, 0, len(events)-1)
	for _, signature := range order {
		duplicates := bySignature[signature]
		if len(duplicates) <= 1 {
			continue
		}
		stale = append(stale, duplicates[:len(duplicates)-1]...)
	}
	return stale
}

func claudeToolUseEvents(content string) ([]claudeToolUseEvent, bool) {
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &blocks); err != nil {
		return nil, false
	}

	events := make([]claudeToolUseEvent, 0, len(blocks))
	for _, block := range blocks {
		blockType, err := rawJSONStringValue(block["type"])
		if err != nil || blockType != "tool_use" {
			continue
		}
		name, err := rawJSONStringValue(block["name"])
		if err != nil {
			continue
		}

		isRead := isClaudeReadToolName(name)
		isWrite := isClaudeWriteToolName(name)
		if !isRead && !isWrite {
			continue
		}

		id, _ := rawJSONStringValue(block["id"])
		path := claudeToolPath(block["input"])
		if path == "" {
			continue
		}

		events = append(events, claudeToolUseEvent{
			id:        id,
			path:      path,
			signature: claudeReadToolSignature(block["input"], path),
			isRead:    isRead,
			isWrite:   isWrite,
		})
	}
	return events, true
}

func claudeReadToolSignature(rawInput json.RawMessage, fallbackPath string) string {
	canonical, err := canonicalizeRawJSON(rawInput)
	if err == nil && canonical != "" {
		return canonical
	}
	return fallbackPath
}

func staleReadFallbackSignature(content string, fallbackPath string) string {
	var block map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &block); err == nil {
		if rawInput, ok := block["input"]; ok {
			return claudeReadToolSignature(rawInput, fallbackPath)
		}
	}
	return fallbackPath
}

func claudeToolPath(rawInput json.RawMessage) string {
	if len(rawInput) == 0 {
		return ""
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return ""
	}
	if path, err := rawJSONStringValue(input["file_path"]); err == nil && path != "" {
		return path
	}
	if path, err := rawJSONStringValue(input["path"]); err == nil && path != "" {
		return path
	}
	return ""
}

func isClaudeReadToolName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read", "read_file", "view", "readfile":
		return true
	default:
		return false
	}
}

func isClaudeWriteToolName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "write", "edit", "write_file", "writefile", "notebookedit":
		return true
	default:
		return false
	}
}

func stripClaudeToolChainBlocks(role, content string, toolUseIDs map[string]struct{}) (string, bool, bool) {
	if role != "assistant" && role != "user" {
		return "", false, false
	}

	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &blocks); err != nil {
		return "", false, false
	}

	rewritten := make([]map[string]json.RawMessage, 0, len(blocks))
	changed := false
	for _, block := range blocks {
		blockType, err := rawJSONStringValue(block["type"])
		if err != nil {
			rewritten = append(rewritten, block)
			continue
		}

		switch {
		case role == "assistant" && blockType == "tool_use":
			id, err := rawJSONStringValue(block["id"])
			if err == nil {
				if _, ok := toolUseIDs[id]; ok {
					changed = true
					continue
				}
			}
		case role == "user" && blockType == "tool_result":
			id, err := rawJSONStringValue(block["tool_use_id"])
			if err == nil {
				if _, ok := toolUseIDs[id]; ok {
					changed = true
					continue
				}
			}
		}

		rewritten = append(rewritten, block)
	}

	if !changed {
		return "", false, false
	}
	if len(rewritten) == 0 {
		return "", true, true
	}

	encoded, err := json.Marshal(rewritten)
	if err != nil {
		return "", false, false
	}
	return string(encoded), true, false
}

type claudeShellToolUse struct {
	msgIdx    int
	toolUseID string
	signature string
	shellName string
}

type claudeShellToolResult struct {
	msgIdx          int
	toolUseID       string
	outputSignature string
	success         bool
}

func claudeDuplicateShellTranscriptToolUseIDs(msgs []ChatMessage) map[string]struct{} {
	toolUses, results := claudeShellHistory(msgs)
	if len(toolUses) == 0 || len(results) == 0 {
		return nil
	}

	latest := make(map[string]string)
	drop := make(map[string]struct{})
	for _, result := range results {
		if !result.success {
			continue
		}
		toolUse, ok := toolUses[result.toolUseID]
		if !ok {
			continue
		}
		key := toolUse.signature + "\n" + result.outputSignature
		if prevID, exists := latest[key]; exists {
			drop[prevID] = struct{}{}
		}
		latest[key] = result.toolUseID
	}

	if len(drop) == 0 {
		return nil
	}
	return drop
}

func claudeFailedRetryToolUseIDs(msgs []ChatMessage) map[string]struct{} {
	toolUses, results := claudeShellHistory(msgs)
	if len(toolUses) == 0 || len(results) == 0 {
		return nil
	}

	pendingFailures := make(map[string][]string)
	drop := make(map[string]struct{})
	for _, result := range results {
		toolUse, ok := toolUses[result.toolUseID]
		if !ok {
			continue
		}

		if result.success {
			for _, toolUseID := range pendingFailures[toolUse.signature] {
				drop[toolUseID] = struct{}{}
			}
			delete(pendingFailures, toolUse.signature)
			continue
		}

		pendingFailures[toolUse.signature] = append(pendingFailures[toolUse.signature], result.toolUseID)
	}

	if len(drop) == 0 {
		return nil
	}
	return drop
}

func anthropicBlocks(raw json.RawMessage) []map[string]json.RawMessage {
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}
	return blocks
}

func claudeShellHistory(msgs []ChatMessage) (map[string]claudeShellToolUse, []claudeShellToolResult) {
	toolUses := make(map[string]claudeShellToolUse)
	for i, m := range msgs {
		if m.Role != "assistant" {
			continue
		}
		for _, block := range anthropicBlocks(json.RawMessage(m.Content)) {
			blockType, err := rawJSONStringValue(block["type"])
			if err != nil || blockType != "tool_use" {
				continue
			}
			name, err := rawJSONStringValue(block["name"])
			if err != nil || !isClaudeShellToolName(name) {
				continue
			}
			toolUseID, err := rawJSONStringValue(block["id"])
			if err != nil || strings.TrimSpace(toolUseID) == "" {
				continue
			}
			signature, ok := claudeShellToolSignature(name, block["input"])
			if !ok {
				continue
			}
			toolUses[toolUseID] = claudeShellToolUse{
				msgIdx:    i,
				toolUseID: toolUseID,
				signature: signature,
				shellName: strings.ToLower(strings.TrimSpace(name)),
			}
		}
	}

	if len(toolUses) == 0 {
		return nil, nil
	}

	results := make([]claudeShellToolResult, 0)
	for i, m := range msgs {
		if m.Role != "user" {
			continue
		}
		for _, block := range anthropicBlocks(json.RawMessage(m.Content)) {
			blockType, err := rawJSONStringValue(block["type"])
			if err != nil || blockType != "tool_result" {
				continue
			}
			toolUseID, err := rawJSONStringValue(block["tool_use_id"])
			if err != nil || strings.TrimSpace(toolUseID) == "" {
				continue
			}
			if _, ok := toolUses[toolUseID]; !ok {
				continue
			}

			outputSignature, success, ok := claudeShellToolResultSignature(block)
			if !ok {
				continue
			}
			results = append(results, claudeShellToolResult{
				msgIdx:          i,
				toolUseID:       toolUseID,
				outputSignature: outputSignature,
				success:         success,
			})
		}
	}

	return toolUses, results
}

func claudeShellToolSignature(name string, rawInput json.RawMessage) (string, bool) {
	if !isClaudeShellToolName(name) || len(rawInput) == 0 {
		return "", false
	}
	canonical, err := canonicalizeRawJSON(rawInput)
	if err != nil || canonical == "" {
		return "", false
	}
	return strings.ToLower(strings.TrimSpace(name)) + ":" + canonical, true
}

func claudeShellToolResultSignature(block map[string]json.RawMessage) (string, bool, bool) {
	rawContent, ok := block["content"]
	if !ok || len(rawContent) == 0 {
		return "", false, false
	}

	for _, payload := range claudeStructuredShellPayloads(rawContent) {
		canonical, err := canonicalizeRawJSON(marshalRawValue(payload))
		if err != nil || canonical == "" {
			continue
		}
		return canonical, claudeShellPayloadSucceeded(payload), true
	}

	return "", false, false
}

func claudeStructuredShellPayloads(rawContent json.RawMessage) []map[string]json.RawMessage {
	if len(rawContent) == 0 {
		return nil
	}

	var direct string
	if err := json.Unmarshal(rawContent, &direct); err == nil {
		if payload, ok := decodeClaudeShellPayload(json.RawMessage(direct)); ok {
			return []map[string]json.RawMessage{payload}
		}
		return nil
	}

	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(rawContent, &parts); err != nil {
		return nil
	}

	payloads := make([]map[string]json.RawMessage, 0, len(parts))
	for _, part := range parts {
		partType, err := rawJSONStringValue(part["type"])
		if err != nil {
			continue
		}
		switch partType {
		case "text":
			text, err := rawJSONStringValue(part["text"])
			if err != nil {
				continue
			}
			if payload, ok := decodeClaudeShellPayload(json.RawMessage(text)); ok {
				payloads = append(payloads, payload)
			}
		case "json":
			if payload, ok := decodeClaudeShellPayload(part["value"]); ok {
				payloads = append(payloads, payload)
			}
		}
	}
	return payloads
}

func decodeClaudeShellPayload(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false
	}
	if !looksLikeClaudeShellPayload(payload) {
		return nil, false
	}
	return payload, true
}

func looksLikeClaudeShellPayload(payload map[string]json.RawMessage) bool {
	for _, field := range []string{
		"stdout",
		"stderr",
		"returnCodeInterpretation",
		"exit_code",
		"exitCode",
		"backgroundedByUser",
		"interrupted",
	} {
		if len(payload[field]) > 0 {
			return true
		}
	}
	return false
}

func claudeShellPayloadSucceeded(payload map[string]json.RawMessage) bool {
	if exitCode, ok, err := rawJSONIntValue(payload["exit_code"]); err == nil && ok {
		return exitCode == 0
	}
	if exitCode, ok, err := rawJSONIntValue(payload["exitCode"]); err == nil && ok {
		return exitCode == 0
	}
	if interpretation, err := rawJSONStringValue(payload["returnCodeInterpretation"]); err == nil {
		normalized := strings.ToLower(strings.TrimSpace(interpretation))
		switch {
		case strings.Contains(normalized, "exit_code:0"):
			return true
		case strings.Contains(normalized, "exit_code:"):
			return false
		case normalized == "success", normalized == "ok":
			return true
		}
	}
	if interrupted, ok := rawJSONBoolValue(payload["interrupted"]); ok && interrupted {
		return false
	}
	if stderr, err := rawJSONStringValue(payload["stderr"]); err == nil && strings.TrimSpace(stderr) != "" {
		return false
	}
	if stdout, err := rawJSONStringValue(payload["stdout"]); err == nil && strings.TrimSpace(stdout) != "" {
		return true
	}
	return false
}

func rawJSONBoolValue(raw json.RawMessage) (bool, bool) {
	if len(raw) == 0 {
		return false, false
	}
	var out bool
	if err := json.Unmarshal(raw, &out); err != nil {
		return false, false
	}
	return out, true
}

// toolUseIDRe extracts tool_use IDs from assistant messages.
var toolUseIDRe = regexp.MustCompile(`"type"\s*:\s*"tool_use"[^}]*"id"\s*:\s*"([^"]+)"`)

// toolResultIDRe extracts tool_use_id references from user messages.
var toolResultIDRe = regexp.MustCompile(`"type"\s*:\s*"tool_result"[^}]*"tool_use_id"\s*:\s*"([^"]+)"`)

// FilterOrphanedResults removes user messages containing tool_result blocks
// that reference tool_use IDs not found in any assistant message.
func FilterOrphanedResults(msgs []ChatMessage) []ChatMessage {
	// First pass: collect all tool_use IDs from assistant messages.
	toolUseIDs := make(map[string]bool)
	for _, m := range msgs {
		if m.Role != "assistant" {
			continue
		}
		for _, match := range toolUseIDRe.FindAllStringSubmatch(m.Content, -1) {
			toolUseIDs[match[1]] = true
		}
	}

	if len(toolUseIDs) == 0 {
		return msgs
	}

	// Second pass: check user messages for orphaned tool_results.
	out := make([]ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "user" {
			refs := toolResultIDRe.FindAllStringSubmatch(m.Content, -1)
			if len(refs) > 0 {
				allOrphaned := true
				for _, ref := range refs {
					if toolUseIDs[ref[1]] {
						allOrphaned = false
						break
					}
				}
				if allOrphaned {
					continue
				}
			}
		}
		out = append(out, m)
	}
	return out
}

// errorIndicators marks tool_result content as a failure.
var errorIndicators = []string{
	"error:", "Error:", "ERROR:",
	"permission denied", "Permission denied",
	"no such file", "No such file",
	"command not found",
	"not found",
	"EISDIR",
	"ENOENT",
	"EACCES",
	"exit code",
	"Exit code",
}

// toolSignatureRe extracts tool name for retry matching.
var toolSignatureRe = regexp.MustCompile(`"name"\s*:\s*"([^"]+)"`)

// FilterFailedRetries removes failed tool attempt + error result pairs
// where a subsequent message retries the same operation.
func FilterFailedRetries(msgs []ChatMessage) []ChatMessage {
	if claudeDropIDs := claudeFailedRetryToolUseIDs(msgs); len(claudeDropIDs) > 0 {
		msgs = dropClaudeToolChainPairs(msgs, claudeDropIDs)
	}

	drop := make(map[int]bool)

	for i, m := range msgs {
		if m.Role != "user" {
			continue
		}

		// Check if this user message contains an error tool_result.
		resultRefs := toolResultIDRe.FindAllStringSubmatch(m.Content, -1)
		if len(resultRefs) == 0 {
			continue
		}

		hasError := false
		for _, indicator := range errorIndicators {
			if strings.Contains(m.Content, indicator) {
				hasError = true
				break
			}
		}
		if !hasError {
			continue
		}

		// Find the preceding assistant message with the matching tool_use.
		prevAssistantIdx := -1
		var toolName string
		for j := i - 1; j >= 0; j-- {
			if msgs[j].Role == "assistant" {
				// Check if it contains the referenced tool_use.
				for _, ref := range resultRefs {
					if strings.Contains(msgs[j].Content, ref[1]) {
						prevAssistantIdx = j
						// Extract tool name.
						nameMatch := toolSignatureRe.FindStringSubmatch(msgs[j].Content)
						if nameMatch != nil {
							toolName = nameMatch[1]
						}
						break
					}
				}
				break
			}
		}

		if prevAssistantIdx < 0 || toolName == "" {
			continue
		}

		// Look forward (within 6 messages) for a retry with the same tool name.
		retryFound := false
		limit := i + 6
		if limit > len(msgs) {
			limit = len(msgs)
		}
		for j := i + 1; j < limit; j++ {
			if msgs[j].Role == "assistant" {
				nameMatch := toolSignatureRe.FindStringSubmatch(msgs[j].Content)
				if nameMatch != nil && nameMatch[1] == toolName {
					retryFound = true
					break
				}
			}
		}

		if retryFound {
			drop[prevAssistantIdx] = true
			drop[i] = true
		}
	}

	if len(drop) == 0 {
		return msgs
	}

	out := make([]ChatMessage, 0, len(msgs)-len(drop))
	for i, m := range msgs {
		if !drop[i] {
			out = append(out, m)
		}
	}
	return out
}

func dropClaudeToolChainPairs(msgs []ChatMessage, toolUseIDs map[string]struct{}) []ChatMessage {
	if len(toolUseIDs) == 0 {
		return msgs
	}

	out := make([]ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		rewritten, changed, removeMessage := stripClaudeToolChainBlocks(m.Role, m.Content, toolUseIDs)
		if removeMessage {
			continue
		}
		if changed {
			out = append(out, ChatMessage{Role: m.Role, Content: rewritten, Source: m.Source})
			continue
		}
		out = append(out, m)
	}
	return out
}
