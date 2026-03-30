package neurorouter

import (
	"regexp"
	"strings"
)

// Intent classifies the purpose of a request.
type Intent string

const (
	IntentTestWriting Intent = "test_writing"
	IntentLintFormat  Intent = "lint_format"
	IntentBoilerplate Intent = "boilerplate"
	IntentRefactor    Intent = "refactor"
	IntentArchitect   Intent = "architecture"
	IntentGeneral     Intent = "general" // default / unclear
)

// IntentRule maps detected patterns to an intent.
type IntentRule struct {
	Intent   Intent
	Patterns []*regexp.Regexp
	Keywords []string // case-insensitive substring match
}

// RoutingRule maps an intent to a target model.
type RoutingRule struct {
	Intent Intent
	Model  string // model name to route to (must exist in targets/pool)
}

// IntentDetector classifies request intent from message content.
type IntentDetector struct {
	rules        []IntentRule
	routingRules map[Intent]string // intent → model override
}

// NewIntentDetector creates a detector with built-in heuristic rules.
func NewIntentDetector() *IntentDetector {
	return &IntentDetector{
		rules:        defaultIntentRules,
		routingRules: make(map[Intent]string),
	}
}

// SetRouting configures which model to use for a given intent.
func (d *IntentDetector) SetRouting(intent Intent, model string) {
	d.routingRules[intent] = model
}

// Detect classifies the intent of a ChatRequest from its messages.
// Returns the detected intent. IntentGeneral if no pattern matches.
func (d *IntentDetector) Detect(msgs []ChatMessage) Intent {
	// Check the last user message (most likely to contain the intent).
	var content string
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" || msgs[i].Role == "system" {
			content = msgs[i].Content
			break
		}
	}
	if content == "" {
		return IntentGeneral
	}

	lower := strings.ToLower(content)

	for _, rule := range d.rules {
		// Check keyword matches first (faster).
		for _, kw := range rule.Keywords {
			if strings.Contains(lower, kw) {
				return rule.Intent
			}
		}
		// Check regex patterns.
		for _, re := range rule.Patterns {
			if re.MatchString(content) {
				return rule.Intent
			}
		}
	}

	return IntentGeneral
}

// RouteModel returns the model override for the detected intent.
// Returns empty string if no routing rule is configured.
func (d *IntentDetector) RouteModel(intent Intent) string {
	return d.routingRules[intent]
}

// DetectAndRoute detects intent and returns the model to route to.
// Returns empty string if no routing override applies (use default).
func (d *IntentDetector) DetectAndRoute(msgs []ChatMessage) (Intent, string) {
	intent := d.Detect(msgs)
	model := d.RouteModel(intent)
	return intent, model
}

var defaultIntentRules = []IntentRule{
	{
		Intent: IntentTestWriting,
		Keywords: []string{
			"write test", "write tests", "add test", "add tests",
			"create test", "unit test", "test coverage",
			"test for this", "test this function",
			"_test.go", "test_", "pytest", "jest",
		},
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)write\s+(a\s+)?tests?\s+for`),
			regexp.MustCompile(`(?i)add\s+(unit\s+)?tests?\s+to`),
			regexp.MustCompile(`(?i)increase\s+test\s+coverage`),
		},
	},
	{
		Intent: IntentLintFormat,
		Keywords: []string{
			"fix lint", "lint error", "format code", "gofmt",
			"eslint", "prettier", "black format", "ruff",
			"fix formatting", "code style", "fix import",
			"golangci-lint",
		},
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)fix\s+(the\s+)?(lint|format|style)`),
			regexp.MustCompile(`(?i)run\s+(gofmt|prettier|black|ruff)`),
		},
	},
	{
		Intent: IntentBoilerplate,
		Keywords: []string{
			"boilerplate", "scaffold", "template",
			"create file", "create struct", "generate",
			"stub", "skeleton", "placeholder",
			"add a new endpoint", "add a new handler",
		},
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)create\s+(a\s+)?(new\s+)?(file|struct|type|interface|class)`),
			regexp.MustCompile(`(?i)generate\s+(a\s+)?(new\s+)?(handler|endpoint|route)`),
		},
	},
	{
		Intent: IntentRefactor,
		Keywords: []string{
			"refactor", "rename", "extract function",
			"move to", "split into", "inline",
			"clean up", "simplify",
		},
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)refactor\s+this`),
			regexp.MustCompile(`(?i)extract\s+(a\s+)?(function|method|type)`),
			regexp.MustCompile(`(?i)rename\s+\w+\s+to\s+\w+`),
		},
	},
	{
		Intent: IntentArchitect,
		Keywords: []string{
			"architecture", "design decision", "trade-off",
			"should we use", "which approach",
			"system design", "high-level",
			"plan the implementation", "design pattern",
		},
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)what('s|\s+is)\s+the\s+best\s+(approach|way|pattern)`),
			regexp.MustCompile(`(?i)design\s+(a|the)\s+(system|architecture)`),
		},
	},
}
