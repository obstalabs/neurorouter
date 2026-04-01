package neurorouter

import (
	"crypto/sha256"
	"encoding/json"
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

// FilterOversizedBlocks truncates messages with Content exceeding threshold.
func FilterOversizedBlocks(threshold int) Filter {
	return func(msgs []ChatMessage) []ChatMessage {
		shellTools := claudeShellToolUses(msgs)
		out := make([]ChatMessage, len(msgs))
		for i, m := range msgs {
			if len(m.Content) > threshold {
				if shaped, changed := shapeClaudeShellToolResultMessage(m.Content, threshold, shellTools); changed {
					out[i] = ChatMessage{
						Role:    m.Role,
						Content: shaped,
						Source:  m.Source,
					}
					continue
				}
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
			if err != nil || !isClaudeShellToolName(name) {
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

func shapeClaudeShellToolResultMessage(content string, threshold int, shellTools map[string]struct{}) (string, bool) {
	if len(shellTools) == 0 {
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
		if _, ok := shellTools[toolUseID]; !ok {
			continue
		}

		blockChanged, err := shapeClaudeToolResultBlock(block, threshold)
		if err != nil {
			continue
		}
		changed = changed || blockChanged
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

func shapeClaudeToolResultBlock(block map[string]json.RawMessage, threshold int) (bool, error) {
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

// systemReminderRe matches <system-reminder>...</system-reminder> blocks.
var systemReminderRe = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

// FilterSystemReminders deduplicates system-reminder blocks, keeping the last occurrence.
func FilterSystemReminders(msgs []ChatMessage) []ChatMessage {
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

	if len(removals) == 0 {
		return msgs
	}

	// Group removals by message index.
	perMsg := make(map[int][]removal)
	for _, r := range removals {
		perMsg[r.msgIdx] = append(perMsg[r.msgIdx], r)
	}

	out := make([]ChatMessage, 0, len(msgs))
	for i, m := range msgs {
		rems, hasRemovals := perMsg[i]
		if !hasRemovals {
			out = append(out, m)
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
		out = append(out, ChatMessage{Role: m.Role, Content: content, Source: m.Source})
	}
	return out
}

// readToolRe matches Read/read_file tool_use patterns and extracts file paths.
var readToolRe = regexp.MustCompile(`"name"\s*:\s*"(?:Read|read_file|View|ReadFile)"[^}]*"(?:file_path|path)"\s*:\s*"([^"]+)"`)

// writeToolRe matches Write/Edit/write_file tool_use patterns.
var writeToolRe = regexp.MustCompile(`"name"\s*:\s*"(?:Write|Edit|write_file|WriteFile|NotebookEdit)"[^}]*"(?:file_path|path)"\s*:\s*"([^"]+)"`)

// FilterStaleReads removes messages containing file reads where the same file
// was read again later without an intervening write.
func FilterStaleReads(msgs []ChatMessage) []ChatMessage {
	lastRead := make(map[string][]int) // path → list of message indices with reads

	// First pass: collect all read and write events.
	writes := make(map[string]int) // path → latest write index
	for i, m := range msgs {
		for _, match := range writeToolRe.FindAllStringSubmatch(m.Content, -1) {
			writes[match[1]] = i
		}
		for _, match := range readToolRe.FindAllStringSubmatch(m.Content, -1) {
			lastRead[match[1]] = append(lastRead[match[1]], i)
		}
	}

	// Determine which message indices to drop.
	drop := make(map[int]bool)
	for path, indices := range lastRead {
		if len(indices) <= 1 {
			continue
		}
		latestWrite, hasWrite := writes[path]
		// Keep only the last read. Drop earlier reads unless a write occurred after them.
		last := indices[len(indices)-1]
		for _, idx := range indices[:len(indices)-1] {
			if hasWrite && latestWrite > idx && latestWrite < last {
				// Write occurred between this read and the last read — keep this read.
				continue
			}
			drop[idx] = true
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
