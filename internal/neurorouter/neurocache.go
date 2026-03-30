package neurorouter

import (
	"crypto/sha256"
	"fmt"
	"sync"
)

// SuggestionCategory classifies what kind of fix is needed.
type SuggestionCategory string

const (
	CategoryHook   SuggestionCategory = "hook"   // behavior change (read-cache, auto-compact)
	CategorySkill  SuggestionCategory = "skill"  // agent capability (/compact at threshold)
	CategoryFilter SuggestionCategory = "filter" // automatic fix (dedup reminders, strip thinking)
	CategoryPolicy SuggestionCategory = "policy" // team rule (max context before cleanup)
)

// SuggestionSeverity indicates urgency.
type SuggestionSeverity string

const (
	SeverityHigh   SuggestionSeverity = "high"   // >$5/session waste or >30% noise
	SeverityMedium SuggestionSeverity = "medium" // $1-5/session waste or 15-30% noise
	SeverityLow    SuggestionSeverity = "low"    // <$1/session waste or <15% noise
)

// Suggestion is a structured recommendation emitted by the neurocache.
// Each suggestion maps a detected pattern to a concrete fix with quantified impact.
type Suggestion struct {
	Type                 string             `json:"type"`                  // "stale_reads", "context_bloat", "reminder_spam", "request_repeat", "thinking_bloat", "large_tool_output"
	Category             SuggestionCategory `json:"category"`              // hook, skill, filter, policy
	Severity             SuggestionSeverity `json:"severity"`              // high, medium, low
	Metric               string             `json:"metric"`                // human-readable: "proxy.go read 7 times (28KB waste)"
	TokensWasted         int                `json:"tokens_wasted"`         // estimated tokens wasted
	CostUSD              float64            `json:"cost_usd"`              // estimated dollar cost at $3/M input tokens
	Action               string             `json:"action"`                // what to do: "install read-cache hook"
	InstallAction        string             `json:"install_action"`        // concrete install path or command
	ProjectedImprovement string             `json:"projected_improvement"` // "OPS 45% → 72%, ~$12/session saved"
}

// costPerToken is the assumed input token cost for suggestions ($3/M tokens ≈ Claude Sonnet).
const costPerToken = 3.0 / 1_000_000

// tokensFromBytes estimates tokens from byte count (1 token ≈ 4 bytes).
func tokensFromBytes(bytes int64) int {
	return int(bytes / 4)
}

// NeurocacheConfig controls the neurocache.
type NeurocacheConfig struct {
	Enabled bool
}

// Neurocache tracks request patterns across a session to detect waste.
// All stats are session-scoped, never user-scoped.
type Neurocache struct {
	mu sync.Mutex

	// File read tracking.
	fileReads map[string]int // path → read count this session

	// Reminder deduplication tracking.
	remindersSeen  map[[32]byte]int // reminder hash → occurrence count
	reminderBytes  int64            // total reminder bytes (including duplicates)
	uniqueRemBytes int64            // unique reminder bytes

	// Context bloat tracking.
	totalBytesBefore int64 // sum of all pipeline BytesBefore
	totalBytesAfter  int64 // sum of all pipeline BytesAfter
	requestCount     int   // total requests processed

	// Request repeat tracking.
	recentHashes map[[32]byte]int // content hash → count

	// Thinking block tracking.
	thinkingBytes int64 // total bytes in thinking blocks across session

	// Large tool output tracking.
	largeOutputCount int   // tool outputs > 10KB
	largeOutputBytes int64 // total bytes in large outputs
}

// NewNeurocache creates a new session-scoped tracker.
func NewNeurocache() *Neurocache {
	return &Neurocache{
		fileReads:     make(map[string]int),
		remindersSeen: make(map[[32]byte]int),
		recentHashes:  make(map[[32]byte]int),
	}
}

// Record processes a request's messages and pipeline result to update counters.
func (nc *Neurocache) Record(msgs []ChatMessage, result *PipelineResult) {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	nc.requestCount++

	if result != nil {
		nc.totalBytesBefore += int64(result.BytesBefore)
		nc.totalBytesAfter += int64(result.BytesAfter)
	}

	// Track file reads.
	for _, m := range msgs {
		for _, match := range readToolRe.FindAllStringSubmatch(m.Content, -1) {
			nc.fileReads[match[1]]++
		}
	}

	// Track reminder duplication.
	for _, m := range msgs {
		matches := systemReminderRe.FindAllString(m.Content, -1)
		for _, reminder := range matches {
			hash := sha256.Sum256([]byte(reminder))
			nc.reminderBytes += int64(len(reminder))
			if nc.remindersSeen[hash] == 0 {
				nc.uniqueRemBytes += int64(len(reminder))
			}
			nc.remindersSeen[hash]++
		}
	}

	// Track content repeats.
	var contentHash [32]byte
	for _, m := range msgs {
		if m.Role == "user" && len(m.Content) > 100 {
			contentHash = sha256.Sum256([]byte(m.Content))
			nc.recentHashes[contentHash]++
		}
	}

	// Track thinking blocks.
	for _, m := range msgs {
		if m.Role == "assistant" {
			for _, match := range thinkingBlockRe.FindAllString(m.Content, -1) {
				nc.thinkingBytes += int64(len(match))
			}
		}
	}

	// Track large tool outputs (>10KB).
	for _, m := range msgs {
		if m.Role == "user" {
			results := toolResultIDRe.FindAllStringSubmatch(m.Content, -1)
			if len(results) > 0 && len(m.Content) > 10*1024 {
				nc.largeOutputCount++
				nc.largeOutputBytes += int64(len(m.Content))
			}
		}
	}
}

// Suggestions returns all accumulated suggestions based on observed patterns.
// Each suggestion is a concrete, actionable fix with quantified impact.
func (nc *Neurocache) Suggestions() []Suggestion {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	var suggestions []Suggestion

	// Repeated file reads: flag files read 3+ times.
	for path, count := range nc.fileReads {
		if count >= 3 {
			wasteBytes := int64(count-1) * 4096 // estimate ~4KB per redundant read
			tokens := tokensFromBytes(wasteBytes)
			cost := float64(tokens) * costPerToken
			severity := SeverityLow
			if cost > 5.0 {
				severity = SeverityHigh
			} else if cost > 1.0 {
				severity = SeverityMedium
			}
				suggestions = append(suggestions, Suggestion{
					Type:                 "stale_reads",
					Category:             CategoryHook,
					Severity:             severity,
					Metric:               fmt.Sprintf("%s read %d times (%dKB waste)", path, count, wasteBytes/1024),
					TokensWasted:         tokens,
					CostUSD:              cost,
					Action:               "install read-cache hook to avoid redundant file reads",
					InstallAction:        "review repeated reads with neurorouter audit or stats and cache file contents in the client workflow",
					ProjectedImprovement: fmt.Sprintf("%d fewer reads, ~$%.2f/session saved", count-1, cost),
				})
		}
	}

	// Reminder duplication: if total reminder bytes > 2x unique.
	if nc.uniqueRemBytes > 0 && nc.reminderBytes > nc.uniqueRemBytes*2 {
		wasteBytes := nc.reminderBytes - nc.uniqueRemBytes
		tokens := tokensFromBytes(wasteBytes)
		cost := float64(tokens) * costPerToken
		severity := SeverityMedium
		if cost > 5.0 {
			severity = SeverityHigh
		}
		ratio := float64(nc.reminderBytes) / float64(nc.uniqueRemBytes)
			suggestions = append(suggestions, Suggestion{
				Type:                 "reminder_spam",
				Category:             CategoryFilter,
				Severity:             severity,
			Metric:               fmt.Sprintf("reminders duplicated %.0fx (%dKB total, %dKB unique)", ratio, nc.reminderBytes/1024, nc.uniqueRemBytes/1024),
				TokensWasted:         tokens,
				CostUSD:              cost,
				Action:               "enable system_reminders filter to deduplicate automatically",
				InstallAction:        "keep NeuroRouter filters enabled (default) so reminder cleanup stays active",
				ProjectedImprovement: fmt.Sprintf("%dKB saved per session, ~$%.2f", wasteBytes/1024, cost),
			})
	}

	// Context bloat: if average waste > 25%.
	if nc.totalBytesBefore > 0 && nc.requestCount >= 3 {
		wasteRatio := 1.0 - float64(nc.totalBytesAfter)/float64(nc.totalBytesBefore)
		if wasteRatio > 0.25 {
			wasteBytes := nc.totalBytesBefore - nc.totalBytesAfter
			tokens := tokensFromBytes(wasteBytes)
			cost := float64(tokens) * costPerToken
			severity := SeverityHigh
			if wasteRatio < 0.30 {
				severity = SeverityMedium
			}
				suggestions = append(suggestions, Suggestion{
					Type:                 "context_bloat",
					Category:             CategorySkill,
				Severity:             severity,
				Metric:               fmt.Sprintf("%.0f%% of context is noise (%dKB wasted across %d requests)", wasteRatio*100, wasteBytes/1024, nc.requestCount),
					TokensWasted:         tokens,
					CostUSD:              cost,
					Action:               "trigger /compact at 35K token threshold",
					InstallAction:        "compact earlier in the client session and keep NeuroRouter filters enabled",
					ProjectedImprovement: fmt.Sprintf("%.0f%% noise reduction, ~$%.2f/session saved", wasteRatio*100, cost),
				})
		}
	}

	// Request repeats: same content sent 3+ times.
	for _, count := range nc.recentHashes {
		if count >= 3 {
				suggestions = append(suggestions, Suggestion{
					Type:                 "request_repeat",
					Category:             CategoryHook,
				Severity:             SeverityMedium,
				Metric:               fmt.Sprintf("identical prompt sent %d times", count),
					TokensWasted:         0, // unknown without content size
					CostUSD:              0,
					Action:               "cache this response or use a deterministic approach",
					InstallAction:        "inspect repeated requests with neurorouter stats or audit and fix the retry path in the client workflow",
					ProjectedImprovement: fmt.Sprintf("%d duplicate requests eliminated", count-1),
				})
		}
	}

	// Thinking block waste: if thinking bytes > 10% of total.
	if nc.totalBytesBefore > 0 && nc.thinkingBytes > 0 {
		thinkRatio := float64(nc.thinkingBytes) / float64(nc.totalBytesBefore)
		if thinkRatio > 0.10 {
			tokens := tokensFromBytes(nc.thinkingBytes)
			cost := float64(tokens) * costPerToken
			severity := SeverityMedium
			if thinkRatio > 0.25 {
				severity = SeverityHigh
			}
				suggestions = append(suggestions, Suggestion{
					Type:                 "thinking_bloat",
					Category:             CategoryFilter,
				Severity:             severity,
				Metric:               fmt.Sprintf("%.0f%% of token spend is thinking blocks (%dKB)", thinkRatio*100, nc.thinkingBytes/1024),
					TokensWasted:         tokens,
					CostUSD:              cost,
					Action:               "enable thinking filter to strip thinking blocks",
					InstallAction:        "keep NeuroRouter filters enabled (default) so thinking cleanup stays active",
					ProjectedImprovement: fmt.Sprintf("%.0f%% cost reduction on thinking-heavy sessions", thinkRatio*100),
				})
		}
	}

	// Large tool outputs: if 5+ large outputs seen.
	if nc.largeOutputCount >= 5 {
		tokens := tokensFromBytes(nc.largeOutputBytes)
		cost := float64(tokens) * costPerToken
		severity := SeverityMedium
		if cost > 5.0 {
			severity = SeverityHigh
		}
			suggestions = append(suggestions, Suggestion{
				Type:                 "large_tool_output",
				Category:             CategoryPolicy,
			Severity:             severity,
			Metric:               fmt.Sprintf("%d tool outputs >10KB (%dKB total)", nc.largeOutputCount, nc.largeOutputBytes/1024),
				TokensWasted:         tokens,
				CostUSD:              cost,
				Action:               "set max tool output size policy to truncate large results",
				InstallAction:        "trim file reads, command output, or diffs in the client workflow before they reach the model",
				ProjectedImprovement: fmt.Sprintf("%dKB saved, ~$%.2f/session", nc.largeOutputBytes/1024, cost),
			})
	}

	return suggestions
}

// Stats returns a snapshot of current counters.
func (nc *Neurocache) Stats() map[string]any {
	suggestions := nc.Suggestions()

	nc.mu.Lock()
	defer nc.mu.Unlock()

	return map[string]any{
		"requests":         nc.requestCount,
		"unique_reads":     len(nc.fileReads),
		"total_reads":      sumInts(nc.fileReads),
		"bytes_before":     nc.totalBytesBefore,
		"bytes_after":      nc.totalBytesAfter,
		"unique_reminders": len(nc.remindersSeen),
		"suggestions":      len(suggestions),
	}
}

func sumInts(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}
