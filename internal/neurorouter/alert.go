package neurorouter

import (
	"fmt"
	"strings"
	"sync"
)

// AlertTier controls when an alert is shown.
type AlertTier int

const (
	TierCritical   AlertTier = iota // always shown (secrets detected)
	TierImportant                   // shown at default+ verbosity (significant waste)
	TierSuggestion                  // max 1/session, throttled (patterns detected)
)

// Verbosity controls which tiers are displayed.
type Verbosity int

const (
	VerbositySilent  Verbosity = iota // nothing (DND overrides to this for non-critical)
	VerbosityMinimal                  // critical only
	VerbosityDefault                  // critical + important
	VerbosityVerbose                  // all tiers
)

// ParseVerbosity converts a string to Verbosity.
func ParseVerbosity(s string) Verbosity {
	switch strings.ToLower(s) {
	case "silent":
		return VerbositySilent
	case "minimal":
		return VerbosityMinimal
	case "verbose":
		return VerbosityVerbose
	default:
		return VerbosityDefault
	}
}

// Alert is a single inline message to inject into a response.
type Alert struct {
	Tier    AlertTier
	Message string // without prefix, e.g. "AWS key redacted. Rotate: aws iam create-access-key"
}

// AlertInjector manages alert generation, throttling, and aggregation.
type AlertInjector struct {
	mu sync.Mutex

	verbosity               Verbosity
	dnd                     *DND // nil = no DND
	inputPricePerMillionUSD float64

	// Throttling state.
	recentAlerts   []string // last N alert messages for dedup
	throttleWindow int      // no repeats within this many requests (default 10)
	requestsSince  int      // requests since last injection

	// Aggregation state.
	recentInjections int // injections in last 5 requests
	recentWindow     int // window size for aggregation detection
	pendingAggregate []Alert

	// Session limits.
	suggestionsEmitted int // max 1 suggestion per session
	maxSuggestions     int
}

// NewAlertInjector creates an injector with default settings.
func NewAlertInjector(verbosity Verbosity, dnd *DND) *AlertInjector {
	return NewAlertInjectorWithPricing(verbosity, dnd, DefaultInputPricePerMillionUSD)
}

// NewAlertInjectorWithPricing creates an injector with configurable context-cost pricing.
func NewAlertInjectorWithPricing(verbosity Verbosity, dnd *DND, inputPricePerMillionUSD float64) *AlertInjector {
	return &AlertInjector{
		verbosity:               verbosity,
		dnd:                     dnd,
		inputPricePerMillionUSD: NormalizeInputPricePerMillionUSD(inputPricePerMillionUSD),
		throttleWindow:          10,
		recentWindow:            5,
		maxSuggestions:          1,
	}
}

// Generate creates alerts based on pipeline results.
// Called after each request. Returns alerts to inject (may be empty).
func (ai *AlertInjector) Generate(result *PipelineResult, suggestions []Suggestion) []Alert {
	if result == nil {
		return nil
	}

	ai.mu.Lock()
	defer ai.mu.Unlock()

	ai.requestsSince++

	var alerts []Alert

	// Critical: secrets detected.
	if result.SecretsFound > 0 {
		msg := fmt.Sprintf("%d secret(s) detected", result.SecretsFound)
		switch result.SecretPolicy {
		case PolicyBlock:
			msg += " — request blocked"
		case PolicyRedact:
			msg += " — redacted before sending"
		case PolicyWarn:
			msg += " — forwarded (policy: warn)"
		}
		alerts = append(alerts, Alert{Tier: TierCritical, Message: msg})
	}

	// Important: significant context waste removed (>1KB shaped).
	summary := SummarizeSavings(result.BytesBefore, result.BytesAfter, ai.inputPricePerMillionUSD)
	if summary.BytesSaved > 1024 && len(result.FiltersRun) > 0 {
		msg := fmt.Sprintf("Shaped %dK tokens of context waste (~$%.2f avoided)", summary.TokensSaved/1000, summary.MoneySavedUSD)
		alerts = append(alerts, Alert{Tier: TierImportant, Message: msg})
	}

	// Suggestion: pattern detected (max 1/session).
	if ai.suggestionsEmitted < ai.maxSuggestions && len(suggestions) > 0 {
		// Pick highest severity suggestion.
		best := suggestions[0]
		for _, s := range suggestions[1:] {
			if s.Severity == SeverityHigh {
				best = s
				break
			}
		}
		msg := fmt.Sprintf("%s detected. Run: neurorouter explain %s", best.Type, best.Type)
		alerts = append(alerts, Alert{Tier: TierSuggestion, Message: msg})
	}

	return ai.filter(alerts)
}

// filter applies verbosity, DND, throttling, and aggregation.
func (ai *AlertInjector) filter(alerts []Alert) []Alert {
	if len(alerts) == 0 {
		return nil
	}

	var result []Alert

	for _, a := range alerts {
		// DND > verbosity: DND suppresses everything except critical.
		if ai.dnd != nil && ai.dnd.IsActive() {
			if a.Tier != TierCritical {
				continue
			}
		}

		// Verbosity filter.
		switch ai.verbosity {
		case VerbositySilent:
			continue
		case VerbosityMinimal:
			if a.Tier != TierCritical {
				continue
			}
		case VerbosityDefault:
			if a.Tier == TierSuggestion {
				continue
			}
		case VerbosityVerbose:
			// show all
		}

		// Throttle: no repeats within window.
		if ai.isThrottled(a.Message) {
			continue
		}

		// Suggestion limit.
		if a.Tier == TierSuggestion {
			if ai.suggestionsEmitted >= ai.maxSuggestions {
				continue
			}
			ai.suggestionsEmitted++
		}

		result = append(result, a)
	}

	if len(result) == 0 {
		return nil
	}

	// Track for throttling.
	for _, a := range result {
		ai.trackAlert(a.Message)
	}

	// Track injection rate for aggregation.
	ai.recentInjections++

	// Aggregation: if 3+ injections in last 5 requests, batch.
	if ai.recentInjections >= 3 && ai.requestsSince <= ai.recentWindow {
		ai.pendingAggregate = append(ai.pendingAggregate, result...)
		return nil // suppress, will batch later
	}

	// Flush any pending aggregate.
	if len(ai.pendingAggregate) > 0 {
		batched := ai.flushAggregate()
		result = append([]Alert{batched}, result...)
	}

	ai.requestsSince = 0
	return result
}

func (ai *AlertInjector) isThrottled(msg string) bool {
	for _, recent := range ai.recentAlerts {
		if recent == msg {
			return true
		}
	}
	return false
}

func (ai *AlertInjector) trackAlert(msg string) {
	ai.recentAlerts = append(ai.recentAlerts, msg)
	if len(ai.recentAlerts) > ai.throttleWindow {
		ai.recentAlerts = ai.recentAlerts[1:]
	}
}

func (ai *AlertInjector) flushAggregate() Alert {
	count := len(ai.pendingAggregate)
	ai.pendingAggregate = nil
	ai.recentInjections = 0
	return Alert{
		Tier:    TierImportant,
		Message: fmt.Sprintf("Last %d alerts batched. Run: neurorouter stats", count),
	}
}

// FormatAlerts formats alerts for prepending to response content.
// Returns empty string if no alerts.
func FormatAlerts(alerts []Alert) string {
	if len(alerts) == 0 {
		return ""
	}

	var b strings.Builder
	for _, a := range alerts {
		fmt.Fprintf(&b, "[NEUROROUTER] %s\n", a.Message)
	}
	b.WriteString("\n")
	return b.String()
}
