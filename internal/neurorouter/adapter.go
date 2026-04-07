package neurorouter

import (
	"regexp"
	"strings"
)

// FilterAdapter maps generic filter operations to provider-specific content structures.
type FilterAdapter interface {
	// Name returns the adapter identifier.
	Name() string

	// Filters returns the filter chain appropriate for this provider.
	Filters(cfg FilterConfig) *FilterChain
}

// DetectAdapter auto-detects the provider from message content patterns.
// Returns the appropriate adapter. Falls back to GenericAdapter if unknown.
func DetectAdapter(msgs []ChatMessage) FilterAdapter {
	for _, m := range msgs {
		// Claude: <system-reminder>, tool_use/tool_result with toolu_ IDs, <thinking>.
		if strings.Contains(m.Content, "<system-reminder>") ||
			strings.Contains(m.Content, "toolu_") ||
			strings.Contains(m.Content, "<thinking>") {
			return ClaudeAdapter{}
		}

		// OpenAI: function_call, tool_calls with call_ IDs.
		if strings.Contains(m.Content, `"function_call"`) ||
			strings.Contains(m.Content, `"tool_calls"`) ||
			strings.Contains(m.Content, `call_`) {
			return OpenAIAdapter{}
		}
	}

	return GenericAdapter{}
}

// SelectFilterAdapter chooses the runtime adapter for one request.
// Provider/capability signals win because they are explicit; content detection is fallback only.
func SelectFilterAdapter(cap TargetCapabilities, msgs []ChatMessage) FilterAdapter {
	switch strings.ToLower(strings.TrimSpace(cap.Provider)) {
	case "anthropic", "claude":
		return ClaudeAdapter{}
	case "openai", "openai-compatible", "deepseek", "groq":
		return OpenAIAdapter{}
	}

	if detected := DetectAdapter(msgs); detected != nil {
		return detected
	}
	return GenericAdapter{}
}

// --- Claude Adapter ---

// ClaudeAdapter handles Claude Code's content patterns.
// All 6 existing filters work with Claude's format.
type ClaudeAdapter struct{}

func (ClaudeAdapter) Name() string { return "claude" }

func (ClaudeAdapter) Filters(cfg FilterConfig) *FilterChain {
	cfg.OrphanedResults = false
	return NewFilterChain(cfg)
}

// --- OpenAI Adapter ---

// OpenAIAdapter handles OpenAI-style chat-completions content patterns.
// Uses universal filters plus OpenAI-compatible duplicate-system/tool-call cleanup.
type OpenAIAdapter struct{}

func (OpenAIAdapter) Name() string { return "openai" }

func (OpenAIAdapter) Filters(cfg FilterConfig) *FilterChain {
	var filters []NamedFilter

	// Universal filters work across all providers.
	if cfg.OversizedBlocks {
		threshold := cfg.MaxBlockBytes
		if threshold <= 0 {
			threshold = defaultMaxBlockBytes
		}
		filters = append(filters, NamedFilter{Name: "oversized_blocks", Apply: FilterOversizedBlocks(threshold)})
	}
	if cfg.SystemReminders {
		// OpenAI doesn't use <system-reminder> but may have repeated system messages.
		filters = append(filters, NamedFilter{Name: "system_reminders", Apply: FilterDuplicateSystemMessages})
	}
	if cfg.StaleReads {
		filters = append(filters, NamedFilter{Name: "stale_reads", Apply: FilterStaleReads})
	}
	if cfg.OrphanedResults {
		// OpenAI uses function_call/tool_calls format instead of tool_use/tool_result.
		filters = append(filters, NamedFilter{Name: "orphaned_results", Apply: FilterOpenAIOrphanedCalls})
	}

	// Thinking and FailedRetries are Claude-specific — skip for OpenAI.

	if len(filters) == 0 {
		return nil
	}
	return &FilterChain{Filters: filters}
}

// --- Generic Adapter ---

// GenericAdapter applies only universal filters that work with any Chat Completions API.
type GenericAdapter struct{}

func (GenericAdapter) Name() string { return "generic" }

func (GenericAdapter) Filters(cfg FilterConfig) *FilterChain {
	var filters []NamedFilter

	if cfg.OversizedBlocks {
		threshold := cfg.MaxBlockBytes
		if threshold <= 0 {
			threshold = defaultMaxBlockBytes
		}
		filters = append(filters, NamedFilter{Name: "oversized_blocks", Apply: FilterOversizedBlocks(threshold)})
	}
	if cfg.StaleReads {
		filters = append(filters, NamedFilter{Name: "stale_reads", Apply: FilterStaleReads})
	}

	if len(filters) == 0 {
		return nil
	}
	return &FilterChain{Filters: filters}
}

// --- OpenAI-specific filters ---

// openaiToolCallIDRe extracts tool call IDs from OpenAI tool_calls format.
var openaiToolCallIDRe = regexp.MustCompile(`"id"\s*:\s*"(call_[^"]+)"`)

// openaiToolCallRefRe extracts tool_call_id references from tool role messages.
var openaiToolCallRefRe = regexp.MustCompile(`"tool_call_id"\s*:\s*"(call_[^"]+)"`)

// FilterOpenAIOrphanedCalls removes messages with tool_call_id references
// to call IDs that don't exist in any assistant message's tool_calls array.
func FilterOpenAIOrphanedCalls(msgs []ChatMessage) []ChatMessage {
	// First pass: collect all tool call IDs from assistant messages.
	callIDs := make(map[string]bool)
	for _, m := range msgs {
		if m.Role == "assistant" {
			for _, match := range openaiToolCallIDRe.FindAllStringSubmatch(m.Content, -1) {
				callIDs[match[1]] = true
			}
		}
	}

	if len(callIDs) == 0 {
		return msgs
	}

	// Second pass: drop tool-role messages referencing missing call IDs.
	out := make([]ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "tool" {
			refs := openaiToolCallRefRe.FindAllStringSubmatch(m.Content, -1)
			if len(refs) > 0 {
				allOrphaned := true
				for _, ref := range refs {
					if callIDs[ref[1]] {
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

// FilterDuplicateSystemMessages deduplicates system messages with identical content.
// Keeps the last occurrence of each unique system message.
func FilterDuplicateSystemMessages(msgs []ChatMessage) []ChatMessage {
	// Track system message content → last index.
	lastSeen := make(map[string]int)
	for i, m := range msgs {
		if m.Role == "system" {
			lastSeen[m.Content] = i
		}
	}

	if len(lastSeen) == 0 {
		return msgs
	}

	// Build set of indices to keep.
	keepIndices := make(map[int]bool)
	for _, idx := range lastSeen {
		keepIndices[idx] = true
	}

	out := make([]ChatMessage, 0, len(msgs))
	for i, m := range msgs {
		if m.Role == "system" {
			if !keepIndices[i] {
				continue // skip duplicate
			}
		}
		out = append(out, m)
	}
	return out
}
