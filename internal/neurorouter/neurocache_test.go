package neurorouter

import (
	"strings"
	"sync"
	"testing"
)

func findSuggestion(suggestions []Suggestion, typ string) *Suggestion {
	for i := range suggestions {
		if suggestions[i].Type == typ {
			return &suggestions[i]
		}
	}
	return nil
}

func TestNeurocache_RepeatedReads(t *testing.T) {
	nc := NewNeurocache()

	for i := 0; i < 5; i++ {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `{"name":"Read","id":"t1","input":{"file_path":"/src/proxy.go"}}`},
			{Role: "user", Content: "file contents..."},
		}
		nc.Record(msgs, &PipelineResult{BytesBefore: 100, BytesAfter: 100})
	}

	suggestions := nc.Suggestions()
	s := findSuggestion(suggestions, "stale_reads")
	if s == nil {
		t.Fatal("expected stale_reads suggestion after 5 reads of same file")
	}
	if s.Category != CategoryHook {
		t.Errorf("expected category hook, got %s", s.Category)
	}
	if s.TokensWasted <= 0 {
		t.Error("expected positive TokensWasted")
	}
	if s.InstallAction == "" {
		t.Error("expected non-empty InstallAction")
	}
}

func TestNeurocache_ReminderDuplication(t *testing.T) {
	nc := NewNeurocache()

	reminder := "<system-reminder>Remember to be helpful and kind to all users in every response</system-reminder>"
	for i := 0; i < 5; i++ {
		msgs := []ChatMessage{
			{Role: "user", Content: "hello " + reminder},
		}
		nc.Record(msgs, &PipelineResult{BytesBefore: 200, BytesAfter: 200})
	}

	suggestions := nc.Suggestions()
	s := findSuggestion(suggestions, "reminder_spam")
	if s == nil {
		t.Fatal("expected reminder_spam suggestion after 5 identical reminders")
	}
	if s.Category != CategoryFilter {
		t.Errorf("expected category filter, got %s", s.Category)
	}
	if s.CostUSD <= 0 {
		t.Error("expected positive CostUSD")
	}
}

func TestNeurocache_ContextBloat(t *testing.T) {
	nc := NewNeurocache()

	for i := 0; i < 5; i++ {
		nc.Record(nil, &PipelineResult{BytesBefore: 1000, BytesAfter: 500})
	}

	suggestions := nc.Suggestions()
	s := findSuggestion(suggestions, "context_bloat")
	if s == nil {
		t.Fatal("expected context_bloat suggestion with 50% waste ratio")
	}
	if s.Category != CategorySkill {
		t.Errorf("expected category skill, got %s", s.Category)
	}
	if s.Severity != SeverityHigh {
		t.Errorf("expected severity high for 50%% waste, got %s", s.Severity)
	}
	if s.ProjectedImprovement == "" {
		t.Error("expected non-empty ProjectedImprovement")
	}
}

func TestNeurocache_RequestRepeat(t *testing.T) {
	nc := NewNeurocache()

	longContent := "Please analyze this code and tell me what it does. " +
		"I need a detailed explanation of the architecture and design patterns used. " +
		"Focus on the error handling and concurrency model."

	for i := 0; i < 4; i++ {
		msgs := []ChatMessage{
			{Role: "user", Content: longContent},
		}
		nc.Record(msgs, &PipelineResult{BytesBefore: 300, BytesAfter: 300})
	}

	suggestions := nc.Suggestions()
	s := findSuggestion(suggestions, "request_repeat")
	if s == nil {
		t.Fatal("expected request_repeat suggestion after 4 identical prompts")
	}
	if s.Category != CategoryHook {
		t.Errorf("expected category hook, got %s", s.Category)
	}
}

func TestNeurocache_ThinkingBloat(t *testing.T) {
	nc := NewNeurocache()

	// Simulate requests where 30% is thinking blocks.
	thinkingContent := "<thinking>" + strings.Repeat("reasoning here ", 100) + "</thinking>answer"
	for i := 0; i < 5; i++ {
		msgs := []ChatMessage{
			{Role: "assistant", Content: thinkingContent},
			{Role: "user", Content: "ok"},
		}
		// Total bytes = len(thinkingContent) + 2; thinking is ~96% of assistant msg.
		nc.Record(msgs, &PipelineResult{
			BytesBefore: len(thinkingContent) + 2,
			BytesAfter:  len(thinkingContent) + 2,
		})
	}

	suggestions := nc.Suggestions()
	s := findSuggestion(suggestions, "thinking_bloat")
	if s == nil {
		t.Fatal("expected thinking_bloat suggestion")
	}
	if s.Category != CategoryFilter {
		t.Errorf("expected category filter, got %s", s.Category)
	}
	if s.TokensWasted <= 0 {
		t.Error("expected positive TokensWasted")
	}
}

func TestNeurocache_LargeToolOutput(t *testing.T) {
	nc := NewNeurocache()

	// Simulate 6 large tool outputs (>10KB each).
	largeResult := `[{"type":"tool_result","tool_use_id":"toolu_1","content":"` + strings.Repeat("x", 12*1024) + `"}]`
	for i := 0; i < 6; i++ {
		msgs := []ChatMessage{
			{Role: "user", Content: largeResult},
		}
		nc.Record(msgs, &PipelineResult{BytesBefore: len(largeResult), BytesAfter: len(largeResult)})
	}

	suggestions := nc.Suggestions()
	s := findSuggestion(suggestions, "large_tool_output")
	if s == nil {
		t.Fatal("expected large_tool_output suggestion after 6 large outputs")
	}
	if s.Category != CategoryPolicy {
		t.Errorf("expected category policy, got %s", s.Category)
	}
}

func TestNeurocache_NoSuggestions(t *testing.T) {
	nc := NewNeurocache()

	files := []string{"/a.go", "/b.go", "/c.go"}
	for _, f := range files {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `{"name":"Read","id":"t1","input":{"file_path":"` + f + `"}}`},
		}
		nc.Record(msgs, &PipelineResult{BytesBefore: 100, BytesAfter: 95})
	}

	suggestions := nc.Suggestions()
	if len(suggestions) != 0 {
		t.Errorf("expected no suggestions, got %d: %v", len(suggestions), suggestions)
	}
}

func TestNeurocache_Concurrent(t *testing.T) {
	nc := NewNeurocache()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			msgs := []ChatMessage{
				{Role: "assistant", Content: `{"name":"Read","id":"t1","input":{"file_path":"/concurrent.go"}}`},
			}
			nc.Record(msgs, &PipelineResult{BytesBefore: 100, BytesAfter: 80})
			_ = nc.Suggestions()
			_ = nc.Stats()
		}()
	}
	wg.Wait()

	if nc.Stats()["requests"] != 20 {
		t.Errorf("expected 20 requests, got %v", nc.Stats()["requests"])
	}
}

func TestSuggestion_Fields(t *testing.T) {
	nc := NewNeurocache()

	// Generate a stale_reads suggestion to verify all fields are populated.
	for i := 0; i < 5; i++ {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `{"name":"Read","id":"t1","input":{"file_path":"/big/file.go"}}`},
		}
		nc.Record(msgs, &PipelineResult{BytesBefore: 500, BytesAfter: 500})
	}

	suggestions := nc.Suggestions()
	if len(suggestions) == 0 {
		t.Fatal("expected at least one suggestion")
	}

	s := suggestions[0]
	if s.Type == "" {
		t.Error("Type is empty")
	}
	if s.Category == "" {
		t.Error("Category is empty")
	}
	if s.Severity == "" {
		t.Error("Severity is empty")
	}
	if s.Metric == "" {
		t.Error("Metric is empty")
	}
	if s.Action == "" {
		t.Error("Action is empty")
	}
	if s.InstallAction == "" {
		t.Error("InstallAction is empty")
	}
	if s.ProjectedImprovement == "" {
		t.Error("ProjectedImprovement is empty")
	}
}
