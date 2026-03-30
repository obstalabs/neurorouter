package neurorouter

import (
	"strings"
	"testing"
)

func TestAlertInjector_CriticalAlwaysShown(t *testing.T) {
	ai := NewAlertInjector(VerbosityDefault, nil)

	alerts := ai.Generate(&PipelineResult{SecretsFound: 2, SecretPolicy: PolicyRedact}, nil)

	found := false
	for _, a := range alerts {
		if a.Tier == TierCritical {
			found = true
			if !strings.Contains(a.Message, "2 secret(s)") {
				t.Errorf("expected secret count in message, got %q", a.Message)
			}
			if !strings.Contains(a.Message, "redacted") {
				t.Errorf("expected redacted policy in message, got %q", a.Message)
			}
		}
	}
	if !found {
		t.Error("expected critical alert for secrets")
	}
}

func TestAlertInjector_ImportantOnSignificantWaste(t *testing.T) {
	ai := NewAlertInjector(VerbosityDefault, nil)

	alerts := ai.Generate(&PipelineResult{
		BytesBefore: 10000,
		BytesAfter:  5000,
		FiltersRun:  []string{"thinking"},
	}, nil)

	found := false
	for _, a := range alerts {
		if a.Tier == TierImportant {
			found = true
		}
	}
	if !found {
		t.Error("expected important alert for >1KB saved")
	}
}

func TestAlertInjector_NoAlertForSmallSavings(t *testing.T) {
	ai := NewAlertInjector(VerbosityDefault, nil)

	alerts := ai.Generate(&PipelineResult{
		BytesBefore: 1000,
		BytesAfter:  900,
		FiltersRun:  []string{"thinking"},
	}, nil)

	for _, a := range alerts {
		if a.Tier == TierImportant {
			t.Error("should not emit important alert for <1KB savings")
		}
	}
}

func TestAlertInjector_SuggestionMaxOnePerSession(t *testing.T) {
	ai := NewAlertInjector(VerbosityVerbose, nil)

	suggestions := []Suggestion{{Type: "stale_reads", Severity: SeverityMedium}}

	// First request with suggestion.
	alerts1 := ai.Generate(&PipelineResult{BytesBefore: 100, BytesAfter: 100}, suggestions)
	hasSuggestion := false
	for _, a := range alerts1 {
		if a.Tier == TierSuggestion {
			hasSuggestion = true
		}
	}
	if !hasSuggestion {
		t.Error("expected suggestion on first occurrence")
	}

	// Second request — suggestion should be suppressed.
	alerts2 := ai.Generate(&PipelineResult{BytesBefore: 100, BytesAfter: 100}, suggestions)
	for _, a := range alerts2 {
		if a.Tier == TierSuggestion {
			t.Error("suggestion should be limited to 1 per session")
		}
	}
}

func TestAlertInjector_VerbositySilent(t *testing.T) {
	ai := NewAlertInjector(VerbositySilent, nil)

	alerts := ai.Generate(&PipelineResult{
		SecretsFound: 1,
		SecretPolicy: PolicyWarn,
		BytesBefore:  10000,
		BytesAfter:   5000,
		FiltersRun:   []string{"thinking"},
	}, nil)

	if len(alerts) != 0 {
		t.Errorf("silent mode should suppress all alerts, got %d", len(alerts))
	}
}

func TestAlertInjector_VerbosityMinimal(t *testing.T) {
	ai := NewAlertInjector(VerbosityMinimal, nil)

	alerts := ai.Generate(&PipelineResult{
		SecretsFound: 1,
		SecretPolicy: PolicyBlock,
		BytesBefore:  10000,
		BytesAfter:   5000,
		FiltersRun:   []string{"thinking"},
	}, nil)

	for _, a := range alerts {
		if a.Tier != TierCritical {
			t.Errorf("minimal mode should only show critical, got tier %d", a.Tier)
		}
	}
}

func TestAlertInjector_DNDOverridesVerbosity(t *testing.T) {
	dnd := NewDND()
	dnd.SetManual(true)
	ai := NewAlertInjector(VerbosityVerbose, dnd)

	alerts := ai.Generate(&PipelineResult{
		BytesBefore: 10000,
		BytesAfter:  5000,
		FiltersRun:  []string{"thinking"},
	}, []Suggestion{{Type: "stale_reads", Severity: SeverityMedium}})

	// DND should suppress everything except critical.
	for _, a := range alerts {
		if a.Tier != TierCritical {
			t.Errorf("DND should suppress non-critical, got tier %d: %q", a.Tier, a.Message)
		}
	}
}

func TestAlertInjector_DNDAllowsCritical(t *testing.T) {
	dnd := NewDND()
	dnd.SetManual(true)
	ai := NewAlertInjector(VerbosityDefault, dnd)

	alerts := ai.Generate(&PipelineResult{SecretsFound: 3, SecretPolicy: PolicyBlock}, nil)

	found := false
	for _, a := range alerts {
		if a.Tier == TierCritical {
			found = true
		}
	}
	if !found {
		t.Error("DND should allow critical alerts through")
	}
}

func TestAlertInjector_Throttling(t *testing.T) {
	ai := NewAlertInjector(VerbosityDefault, nil)
	ai.throttleWindow = 3

	result := &PipelineResult{SecretsFound: 1, SecretPolicy: PolicyWarn}

	// First: should show.
	alerts1 := ai.Generate(result, nil)
	if len(alerts1) == 0 {
		t.Fatal("expected alert on first request")
	}

	// Second: same message, should be throttled.
	alerts2 := ai.Generate(result, nil)
	hasCritical := false
	for _, a := range alerts2 {
		if a.Tier == TierCritical {
			hasCritical = true
		}
	}
	if hasCritical {
		t.Error("repeated alert should be throttled within window")
	}
}

func TestFormatAlerts(t *testing.T) {
	alerts := []Alert{
		{Tier: TierCritical, Message: "AWS key redacted"},
		{Tier: TierImportant, Message: "Removed 5K tokens"},
	}

	formatted := FormatAlerts(alerts)

	if !strings.Contains(formatted, "[NEUROROUTER] AWS key redacted") {
		t.Error("expected NEUROROUTER prefix")
	}
	if !strings.Contains(formatted, "[NEUROROUTER] Removed 5K tokens") {
		t.Error("expected second alert")
	}
	if !strings.HasSuffix(formatted, "\n\n") {
		t.Error("expected trailing blank line separator")
	}
}

func TestFormatAlerts_Empty(t *testing.T) {
	if FormatAlerts(nil) != "" {
		t.Error("nil alerts should return empty string")
	}
	if FormatAlerts([]Alert{}) != "" {
		t.Error("empty alerts should return empty string")
	}
}

func TestParseVerbosity(t *testing.T) {
	tests := []struct {
		input string
		want  Verbosity
	}{
		{"silent", VerbositySilent},
		{"minimal", VerbosityMinimal},
		{"default", VerbosityDefault},
		{"verbose", VerbosityVerbose},
		{"SILENT", VerbositySilent},
		{"unknown", VerbosityDefault},
		{"", VerbosityDefault},
	}
	for _, tt := range tests {
		got := ParseVerbosity(tt.input)
		if got != tt.want {
			t.Errorf("ParseVerbosity(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}
