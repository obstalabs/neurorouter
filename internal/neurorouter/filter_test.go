package neurorouter

import (
	"strings"
	"testing"
)

func TestFilterOversizedBlocks(t *testing.T) {
	f := FilterOversizedBlocks(100)

	t.Run("no truncation needed", func(t *testing.T) {
		msgs := []ChatMessage{{Role: "user", Content: "short message"}}
		out := f(msgs)
		if out[0].Content != "short message" {
			t.Errorf("unexpected content: %q", out[0].Content)
		}
	})

	t.Run("truncates large message", func(t *testing.T) {
		large := strings.Repeat("x", 200)
		msgs := []ChatMessage{{Role: "user", Content: large}}
		out := f(msgs)
		if len(out[0].Content) >= 200 {
			t.Errorf("expected truncation, got len %d", len(out[0].Content))
		}
		if !strings.Contains(out[0].Content, "[truncated by neurorouter") {
			t.Error("missing truncation marker")
		}
	})

	t.Run("preserves other messages", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "system", Content: "be helpful"},
			{Role: "user", Content: strings.Repeat("y", 200)},
			{Role: "assistant", Content: "ok"},
		}
		out := f(msgs)
		if out[0].Content != "be helpful" {
			t.Error("system message changed")
		}
		if out[2].Content != "ok" {
			t.Error("assistant message changed")
		}
		if !strings.Contains(out[1].Content, "[truncated") {
			t.Error("user message not truncated")
		}
	})
}

func TestFilterThinking(t *testing.T) {
	t.Run("removes thinking blocks", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: "<thinking>internal reasoning</thinking>Here is my answer."},
		}
		out := FilterThinking(msgs)
		if len(out) != 1 {
			t.Fatalf("expected 1 message, got %d", len(out))
		}
		if strings.Contains(out[0].Content, "thinking") {
			t.Errorf("thinking block not removed: %q", out[0].Content)
		}
		if out[0].Content != "Here is my answer." {
			t.Errorf("unexpected content: %q", out[0].Content)
		}
	})

	t.Run("drops empty messages", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: "<thinking>only thinking</thinking>"},
			{Role: "user", Content: "hello"},
		}
		out := FilterThinking(msgs)
		if len(out) != 1 {
			t.Fatalf("expected 1 message, got %d", len(out))
		}
		if out[0].Role != "user" {
			t.Errorf("wrong message kept: %q", out[0].Role)
		}
	})
}

func TestFilterSystemReminders(t *testing.T) {
	reminder := "<system-reminder>Remember to be helpful</system-reminder>"

	t.Run("deduplicates reminders", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "user", Content: "hello " + reminder},
			{Role: "user", Content: "world " + reminder},
			{Role: "user", Content: "last " + reminder},
		}
		out := FilterSystemReminders(msgs)
		if len(out) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(out))
		}
		// First two should have reminder removed.
		if strings.Contains(out[0].Content, "system-reminder") {
			t.Error("first message still has reminder")
		}
		if strings.Contains(out[1].Content, "system-reminder") {
			t.Error("second message still has reminder")
		}
		// Last should keep it.
		if !strings.Contains(out[2].Content, "system-reminder") {
			t.Error("last message should keep reminder")
		}
	})

	t.Run("no-op without duplicates", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "user", Content: "hello " + reminder},
		}
		out := FilterSystemReminders(msgs)
		if len(out) != 1 || out[0].Content != msgs[0].Content {
			t.Error("single reminder should not be removed")
		}
	})
}

func TestFilterStaleReads(t *testing.T) {
	t.Run("removes stale reads", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `{"name":"Read","id":"t1","input":{"file_path":"/foo/bar.go"}}`},
			{Role: "user", Content: "file contents here"},
			{Role: "assistant", Content: `some other work`},
			{Role: "assistant", Content: `{"name":"Read","id":"t2","input":{"file_path":"/foo/bar.go"}}`},
			{Role: "user", Content: "file contents again"},
		}
		out := FilterStaleReads(msgs)
		// First read (index 0) is stale — same file read again at index 3 with no write between.
		if len(out) != 4 {
			t.Fatalf("expected 4 messages (1 stale read dropped), got %d", len(out))
		}
		// The kept messages should be: user, other work, second read, second result.
		if strings.Contains(out[0].Content, `"name":"Read"`) {
			t.Error("first read should have been dropped")
		}
	})

	t.Run("keeps reads with intervening write", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `{"name":"Read","id":"t1","input":{"file_path":"/foo/bar.go"}}`},
			{Role: "assistant", Content: `{"name":"Write","id":"t2","input":{"file_path":"/foo/bar.go"}}`},
			{Role: "assistant", Content: `{"name":"Read","id":"t3","input":{"file_path":"/foo/bar.go"}}`},
		}
		out := FilterStaleReads(msgs)
		if len(out) != 3 {
			t.Fatalf("expected 3 messages (write intervened), got %d", len(out))
		}
	})
}

func TestFilterOrphanedResults(t *testing.T) {
	t.Run("removes orphaned tool_result", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_123","name":"Read"}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_123","content":"ok"}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_999","content":"orphaned"}]`},
		}
		out := FilterOrphanedResults(msgs)
		if len(out) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(out))
		}
	})

	t.Run("keeps all when no orphans", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_1","name":"Bash"}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_1","content":"done"}]`},
		}
		out := FilterOrphanedResults(msgs)
		if len(out) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(out))
		}
	})
}

func TestFilterFailedRetries(t *testing.T) {
	t.Run("removes failed+error pair when retried", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/x"}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_1","content":"error: no such file"}]`},
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_2","name":"Read","input":{"file_path":"/y"}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_2","content":"file contents"}]`},
		}
		out := FilterFailedRetries(msgs)
		if len(out) != 2 {
			t.Fatalf("expected 2 messages (failed pair dropped), got %d", len(out))
		}
	})

	t.Run("keeps non-retried errors", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_1","content":"error: command not found"}]`},
			{Role: "assistant", Content: "I see the command failed. Let me try a different approach."},
		}
		out := FilterFailedRetries(msgs)
		// No retry with same tool name, so nothing dropped.
		if len(out) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(out))
		}
	})
}

func TestFilterChain_Composition(t *testing.T) {
	cfg := FilterConfig{
		OversizedBlocks: true,
		Thinking:        true,
		MaxBlockBytes:   50,
	}
	chain := NewFilterChain(cfg)
	if chain == nil {
		t.Fatal("expected non-nil chain")
	}

	msgs := []ChatMessage{
		{Role: "assistant", Content: "<thinking>hmm</thinking>answer"},
		{Role: "user", Content: strings.Repeat("x", 100)},
	}

	out, result := chain.Run(msgs)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	if strings.Contains(out[0].Content, "thinking") {
		t.Error("thinking not removed")
	}
	if !strings.Contains(out[1].Content, "[truncated") {
		t.Error("oversized not truncated")
	}
	if result.BytesBefore <= result.BytesAfter {
		t.Error("expected bytes reduction")
	}
	if len(result.Applied) == 0 {
		t.Error("expected filters to be listed as applied")
	}
}

func TestFilterChain_Nil(t *testing.T) {
	chain := NewFilterChain(FilterConfig{})
	if chain != nil {
		t.Error("expected nil chain when no filters enabled")
	}
}
