package neurorouter

import "testing"

func TestDetectAdapter_Claude(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello <system-reminder>be helpful</system-reminder>"},
	}
	a := DetectAdapter(msgs)
	if a.Name() != "claude" {
		t.Errorf("expected claude, got %s", a.Name())
	}
}

func TestDetectAdapter_ClaudeToolUse(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_123","name":"Read"}]`},
	}
	a := DetectAdapter(msgs)
	if a.Name() != "claude" {
		t.Errorf("expected claude, got %s", a.Name())
	}
}

func TestDetectAdapter_OpenAI(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "assistant", Content: `{"tool_calls":[{"id":"call_abc","function":{"name":"get_weather"}}]}`},
	}
	a := DetectAdapter(msgs)
	if a.Name() != "openai" {
		t.Errorf("expected openai, got %s", a.Name())
	}
}

func TestDetectAdapter_Generic(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	a := DetectAdapter(msgs)
	if a.Name() != "generic" {
		t.Errorf("expected generic, got %s", a.Name())
	}
}

func TestSelectFilterAdapter_ProviderWins(t *testing.T) {
	a := SelectFilterAdapter(TargetCapabilities{Provider: "anthropic"}, []ChatMessage{
		{Role: "assistant", Content: `{"tool_calls":[{"id":"call_abc"}]}`},
	})
	if a.Name() != "claude" {
		t.Fatalf("expected claude adapter, got %s", a.Name())
	}
}

func TestSelectFilterAdapter_OpenAICompatibleProviders(t *testing.T) {
	for _, provider := range []string{"openai", "openai-compatible", "deepseek", "groq"} {
		t.Run(provider, func(t *testing.T) {
			a := SelectFilterAdapter(TargetCapabilities{Provider: provider}, []ChatMessage{
				{Role: "user", Content: "hello"},
			})
			if a.Name() != "openai" {
				t.Fatalf("expected openai adapter, got %s", a.Name())
			}
		})
	}
}

func TestSelectFilterAdapter_FallsBackToContentDetection(t *testing.T) {
	a := SelectFilterAdapter(TargetCapabilities{}, []ChatMessage{
		{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_123","name":"Read"}]`},
	})
	if a.Name() != "claude" {
		t.Fatalf("expected claude adapter, got %s", a.Name())
	}
}

func TestSelectFilterAdapter_GenericFallback(t *testing.T) {
	a := SelectFilterAdapter(TargetCapabilities{}, []ChatMessage{
		{Role: "user", Content: "hello"},
	})
	if a.Name() != "generic" {
		t.Fatalf("expected generic adapter, got %s", a.Name())
	}
}

func TestClaudeAdapter_AllFilters(t *testing.T) {
	a := ClaudeAdapter{}
	cfg := FilterConfig{
		StaleReads: true, Thinking: true, OrphanedResults: true,
		FailedRetries: true, SystemReminders: true, OversizedBlocks: true,
	}
	chain := a.Filters(cfg)
	if chain == nil {
		t.Fatal("expected non-nil chain")
	}
	if len(chain.Filters) != 6 {
		t.Errorf("expected 6 filters for Claude, got %d", len(chain.Filters))
	}
}

func TestOpenAIAdapter_Filters(t *testing.T) {
	a := OpenAIAdapter{}
	cfg := FilterConfig{
		StaleReads: true, Thinking: true, OrphanedResults: true,
		FailedRetries: true, SystemReminders: true, OversizedBlocks: true,
	}
	chain := a.Filters(cfg)
	if chain == nil {
		t.Fatal("expected non-nil chain")
	}
	// OpenAI: oversized + system_reminders + stale_reads + orphaned = 4
	// Thinking and FailedRetries are Claude-specific, skipped.
	if len(chain.Filters) != 4 {
		t.Errorf("expected 4 filters for OpenAI, got %d", len(chain.Filters))
	}
}

func TestGenericAdapter_Filters(t *testing.T) {
	a := GenericAdapter{}
	cfg := FilterConfig{
		StaleReads: true, Thinking: true, OrphanedResults: true,
		SystemReminders: true, OversizedBlocks: true,
	}
	chain := a.Filters(cfg)
	if chain == nil {
		t.Fatal("expected non-nil chain")
	}
	// Generic: oversized + stale_reads = 2
	if len(chain.Filters) != 2 {
		t.Errorf("expected 2 filters for generic, got %d", len(chain.Filters))
	}
}

func TestGenericAdapter_NoFilters(t *testing.T) {
	a := GenericAdapter{}
	chain := a.Filters(FilterConfig{})
	if chain != nil {
		t.Error("expected nil chain when no filters enabled")
	}
}

func TestFilterOpenAIOrphanedCalls(t *testing.T) {
	t.Run("removes orphaned tool response", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `{"tool_calls":[{"id":"call_abc","function":{"name":"get_weather"}}]}`},
			{Role: "tool", Content: `{"tool_call_id":"call_abc","content":"sunny"}`},
			{Role: "tool", Content: `{"tool_call_id":"call_missing","content":"orphaned"}`},
		}
		out := FilterOpenAIOrphanedCalls(msgs)
		if len(out) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(out))
		}
	})

	t.Run("keeps all when no orphans", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `{"tool_calls":[{"id":"call_1","function":{"name":"search"}}]}`},
			{Role: "tool", Content: `{"tool_call_id":"call_1","content":"results"}`},
		}
		out := FilterOpenAIOrphanedCalls(msgs)
		if len(out) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(out))
		}
	})
}

func TestFilterDuplicateSystemMessages(t *testing.T) {
	t.Run("deduplicates system messages", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "system", Content: "You are helpful"},
			{Role: "user", Content: "hello"},
			{Role: "system", Content: "You are helpful"},
			{Role: "user", Content: "world"},
		}
		out := FilterDuplicateSystemMessages(msgs)
		if len(out) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(out))
		}
		// First system should be removed, second kept.
		if out[0].Role != "user" {
			t.Errorf("expected first remaining to be user, got %s", out[0].Role)
		}
	})

	t.Run("keeps distinct system messages", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "system", Content: "You are helpful"},
			{Role: "system", Content: "You are concise"},
			{Role: "user", Content: "hello"},
		}
		out := FilterDuplicateSystemMessages(msgs)
		if len(out) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(out))
		}
	})
}
