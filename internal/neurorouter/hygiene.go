package neurorouter

import (
	"regexp"
	"strings"
	"sync"
)

// HygieneCheck is a detected prompt quality issue.
type HygieneCheck struct {
	Issue      string `json:"issue"`      // "vague_directive", "parasite_words", "missing_context", "scope_ambiguity"
	Original   string `json:"original"`   // the problematic part of the prompt
	Suggestion string `json:"suggestion"` // reworded version or advice
}

// HygieneDetector analyzes user prompts for vagueness and noise.
// Pre-send analysis — fires before the request leaves.
type HygieneDetector struct {
	mu sync.Mutex

	// Throttling: max 1 suggestion per session.
	fired bool
}

// NewHygieneDetector creates a prompt hygiene detector.
func NewHygieneDetector() *HygieneDetector {
	return &HygieneDetector{}
}

// Check analyzes the last user message and returns a hygiene issue, if any.
// Returns nil if the prompt is sharp enough or the detector has already fired this session.
func (hd *HygieneDetector) Check(msgs []ChatMessage) *HygieneCheck {
	hd.mu.Lock()
	defer hd.mu.Unlock()

	if hd.fired {
		return nil
	}

	// Find last user message.
	var userMsg string
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" && !strings.Contains(msgs[i].Content, "tool_result") {
			userMsg = msgs[i].Content
			break
		}
	}
	if userMsg == "" {
		return nil
	}

	lower := strings.ToLower(userMsg)

	// Check vague directives first (highest signal).
	if check := checkVagueDirective(lower, userMsg); check != nil {
		hd.fired = true
		return check
	}

	// Check parasite word density.
	if check := checkParasiteWords(lower, userMsg); check != nil {
		hd.fired = true
		return check
	}

	// Check missing context (short prompt for complex ask).
	if check := checkMissingContext(lower, userMsg); check != nil {
		hd.fired = true
		return check
	}

	return nil
}

// Reset allows the detector to fire again (for testing or new session).
func (hd *HygieneDetector) Reset() {
	hd.mu.Lock()
	hd.fired = false
	hd.mu.Unlock()
}

// --- Detectors ---

var vagueDirectives = []struct {
	pattern *regexp.Regexp
	example string
}{
	{regexp.MustCompile(`(?i)^(clean\s+up|cleanup)\b`), "specify which file and what to change: 'in auth.go, extract the validation into a separate function'"},
	{regexp.MustCompile(`(?i)^fix\s+(everything|all|it|this|the\s+code)\b`), "name the specific bug or behavior: 'fix the nil pointer in handleAuth when user is not logged in'"},
	{regexp.MustCompile(`(?i)^(make\s+it\s+better|improve\s+(this|it|the\s+code))\b`), "define what 'better' means: 'reduce handleAuth from 80 lines to under 40 by extracting helpers'"},
	{regexp.MustCompile(`(?i)^refactor\b.*\.\s*$`), "specify the target: 'refactor handleAuth to use early returns instead of nested ifs'"},
	{regexp.MustCompile(`(?i)^(update|change)\s+(the\s+)?(docs?|code|tests?)\s*$`), "specify what to update: 'update README.md usage section with the new --dry-run flag'"},
}

func checkVagueDirective(lower, original string) *HygieneCheck {
	trimmed := strings.TrimSpace(lower)
	for _, vd := range vagueDirectives {
		if vd.pattern.MatchString(trimmed) {
			matched := vd.pattern.FindString(trimmed)
			return &HygieneCheck{
				Issue:      "vague_directive",
				Original:   matched,
				Suggestion: vd.example,
			}
		}
	}
	return nil
}

var parasiteWords = []string{
	"just ", "maybe ", "sort of ", "basically ", "i think ",
	"kind of ", "like ", " stuff", "probably ", "i guess ",
}

func checkParasiteWords(lower, original string) *HygieneCheck {
	if len(original) < 30 {
		return nil // too short to judge
	}

	count := 0
	found := ""
	for _, pw := range parasiteWords {
		if strings.Contains(lower, pw) {
			count++
			if found == "" {
				found = strings.TrimSpace(pw)
			}
		}
	}

	// Only flag if 3+ parasite words in one prompt — clearly rushed.
	if count >= 3 {
		return &HygieneCheck{
			Issue:      "parasite_words",
			Original:   found + " (+" + itoa(count-1) + " more)",
			Suggestion: "Remove filler words for sharper results. State what you want directly.",
		}
	}
	return nil
}

func checkMissingContext(lower, original string) *HygieneCheck {
	// Short prompt (<20 chars) that looks like a complex ask.
	if len(strings.TrimSpace(original)) > 20 {
		return nil
	}

	complexSignals := []string{"refactor", "fix", "add", "implement", "create", "build", "update", "change"}
	for _, sig := range complexSignals {
		if strings.Contains(lower, sig) {
			return &HygieneCheck{
				Issue:      "missing_context",
				Original:   strings.TrimSpace(original),
				Suggestion: "Add specifics: which file, what behavior, acceptance criteria. Short prompts for complex tasks burn tokens on guessing.",
			}
		}
	}
	return nil
}

// FormatHygieneCheck returns the alert-ready message for a hygiene check.
func FormatHygieneCheck(check *HygieneCheck) string {
	if check == nil {
		return ""
	}
	return "Prompt may waste tokens. '" + check.Original + "' → " + check.Suggestion
}

func containsStr(s, substr string) bool {
	return strings.Contains(s, substr)
}
