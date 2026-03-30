package neurorouter

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// FilterGuard detects, traces, and prevents content filter false positives.
type FilterGuard struct {
	mu sync.Mutex

	// Known trigger patterns that cause false positives.
	triggers []filterTrigger

	// History of blocks for tracing.
	blocks []FilterBlock
}

type filterTrigger struct {
	Name    string
	Pattern *regexp.Regexp
	Desc    string
	Advice  string
}

// FilterBlock records a content filter hit.
type FilterBlock struct {
	Timestamp   time.Time `json:"timestamp"`
	TriggerName string    `json:"trigger_name"`
	Preview     string    `json:"preview"`     // truncated content that matched
	FullLength  int       `json:"full_length"` // original content length
	Advice      string    `json:"advice"`
}

// NewFilterGuard creates a guard with the built-in trigger pattern library.
func NewFilterGuard() *FilterGuard {
	return &FilterGuard{
		triggers: builtinTriggers,
	}
}

var builtinTriggers = []filterTrigger{
	{
		Name:    "credential_pattern",
		Pattern: regexp.MustCompile(`(?i)(password|secret|auth_token|api_key)\s*[=:]\s*["']?[^\s"']{8,}`),
		Desc:    "Credential-like key=value patterns in content",
		Advice:  "Wrap credentials in code blocks or use placeholder values",
	},
	{
		Name:    "ssh_key_block",
		Pattern: regexp.MustCompile(`-----BEGIN\s+(RSA |EC |OPENSSH )?PRIVATE KEY-----`),
		Desc:    "SSH/PGP private key headers",
		Advice:  "Remove private key content, reference by path instead",
	},
	{
		Name:    "connection_string",
		Pattern: regexp.MustCompile(`(postgres|mysql|mongodb|redis)://[^\s"']+@[^\s"']+`),
		Desc:    "Database connection strings with credentials",
		Advice:  "Use placeholder values instead of real connection credentials",
	},
	{
		Name:    "security_research",
		Pattern: regexp.MustCompile(`(?i)(exploit|payload|injection|xss|sqli|rce|reverse.?shell|bind.?shell|shellcode|buffer.?overflow)`),
		Desc:    "Security research terminology",
		Advice:  "Add explicit context: 'I'm doing authorized security research on my own code'",
	},
	{
		Name:    "weaponization_terms",
		Pattern: regexp.MustCompile(`(?i)(how\s+to\s+(hack|crack|bypass|exploit)|malware|ransomware|keylogger)`),
		Desc:    "Terms that may trigger safety filters",
		Advice:  "Rephrase with defensive framing: 'How to detect and prevent X' instead of 'How to X'",
	},
	{
		Name:    "bulk_credentials",
		Pattern: regexp.MustCompile(`(?i)(AKIA[0-9A-Z]{16}|sk-[a-zA-Z0-9]{20,}|ghp_[a-zA-Z0-9]{36})`),
		Desc:    "Real API key patterns in content",
		Advice:  "Use synthetic/example keys (AKIAEXAMPLE...) instead of real patterns",
	},
	{
		Name:    "encoded_content",
		Pattern: regexp.MustCompile(`[A-Za-z0-9+/]{100,}={0,2}`),
		Desc:    "Long base64-encoded content blocks",
		Advice:  "Truncate or summarize large encoded blocks, reference files by path",
	},
}

// PreCheck scans messages before sending and returns warnings for trigger matches.
func (fg *FilterGuard) PreCheck(msgs []ChatMessage) []FilterBlock {
	fg.mu.Lock()
	defer fg.mu.Unlock()

	var warnings []FilterBlock
	seen := make(map[string]bool) // dedupe by trigger name per check

	for _, m := range msgs {
		for _, t := range fg.triggers {
			if seen[t.Name] {
				continue
			}
			loc := t.Pattern.FindStringIndex(m.Content)
			if loc == nil {
				continue
			}

			seen[t.Name] = true
			preview := m.Content[loc[0]:loc[1]]
			if len(preview) > 60 {
				preview = preview[:60] + "..."
			}

			warnings = append(warnings, FilterBlock{
				Timestamp:   time.Now(),
				TriggerName: t.Name,
				Preview:     preview,
				FullLength:  len(m.Content),
				Advice:      t.Advice,
			})
		}
	}

	return warnings
}

// RecordBlock logs a content filter block from an API error response.
func (fg *FilterGuard) RecordBlock(msgs []ChatMessage, errorBody string) {
	fg.mu.Lock()
	defer fg.mu.Unlock()

	// Try to identify which trigger caused it.
	triggerName := "unknown"
	advice := "Try splitting the request or rephrasing sensitive content"

	for _, m := range msgs {
		for _, t := range fg.triggers {
			if t.Pattern.MatchString(m.Content) {
				triggerName = t.Name
				advice = t.Advice
				break
			}
		}
		if triggerName != "unknown" {
			break
		}
	}

	block := FilterBlock{
		Timestamp:   time.Now(),
		TriggerName: triggerName,
		Advice:      advice,
	}

	// Preview from the likely triggering content.
	for _, m := range msgs {
		if m.Role == "user" && len(m.Content) > 20 {
			block.Preview = m.Content
			if len(block.Preview) > 200 {
				block.Preview = block.Preview[:200] + "..."
			}
			block.FullLength = len(m.Content)
			break
		}
	}

	fg.blocks = append(fg.blocks, block)
}

// Blocks returns all recorded filter blocks.
func (fg *FilterGuard) Blocks() []FilterBlock {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	out := make([]FilterBlock, len(fg.blocks))
	copy(out, fg.blocks)
	return out
}

// FormatTrace returns a human-readable trace of filter blocks.
func (fg *FilterGuard) FormatTrace() string {
	blocks := fg.Blocks()
	if len(blocks) == 0 {
		return "No content filter blocks recorded this session."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Content filter blocks (%d):\n", len(blocks))
	for i, block := range blocks {
		fmt.Fprintf(&b, "\n  Block %d (%s):\n", i+1, block.Timestamp.Format("15:04:05"))
		fmt.Fprintf(&b, "    Trigger: %s\n", block.TriggerName)
		if block.Preview != "" {
			fmt.Fprintf(&b, "    Content: %s\n", block.Preview)
		}
		fmt.Fprintf(&b, "    Fix: %s\n", block.Advice)
	}
	return b.String()
}

// IsFilterError checks if an HTTP error response is a content filter block.
func IsFilterError(statusCode int, body string) bool {
	if statusCode != 400 {
		return false
	}
	lower := strings.ToLower(body)
	return strings.Contains(lower, "content filter") ||
		strings.Contains(lower, "content_policy") ||
		strings.Contains(lower, "content moderation") ||
		strings.Contains(lower, "safety") ||
		strings.Contains(lower, "blocked by") ||
		strings.Contains(lower, "output blocked")
}
