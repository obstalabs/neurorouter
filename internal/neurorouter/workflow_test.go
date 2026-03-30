package neurorouter

import (
	"testing"
)

func TestWorkflowDetector_NoSuggestionsBelowThreshold(t *testing.T) {
	wd := NewWorkflowDetector()

	// Single sequence occurrence — should not trigger.
	msgs := []ChatMessage{
		{Role: "assistant", Content: `[{"type":"tool_use","name":"Read","id":"t1"}]`},
		{Role: "assistant", Content: `[{"type":"tool_use","name":"Edit","id":"t2"}]`},
		{Role: "assistant", Content: `[{"type":"tool_use","name":"Bash","id":"t3"}]`},
	}
	wd.RecordMessages(msgs)

	suggestions := wd.Suggestions()
	if len(suggestions) != 0 {
		t.Errorf("expected no suggestions with 1 occurrence, got %d", len(suggestions))
	}
}

func TestWorkflowDetector_DetectsRecurringSequence(t *testing.T) {
	wd := NewWorkflowDetector()
	wd.minOccur = 2 // lower for test

	// Same 3-step sequence repeated.
	for i := 0; i < 3; i++ {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","name":"Read","id":"t1"}]`},
			{Role: "assistant", Content: `[{"type":"tool_use","name":"Edit","id":"t2"}]`},
			{Role: "assistant", Content: `[{"type":"tool_use","name":"Bash","id":"t3"}]`},
		}
		wd.RecordMessages(msgs)
	}

	suggestions := wd.Suggestions()
	if len(suggestions) == 0 {
		t.Fatal("expected at least one workflow suggestion")
	}

	s := suggestions[0]
	if s.SkillName == "" {
		t.Error("expected non-empty skill name")
	}
	if s.SkillBody == "" {
		t.Error("expected non-empty skill body")
	}
	if len(s.Steps) < 3 {
		t.Errorf("expected at least 3 steps, got %d", len(s.Steps))
	}
}

func TestWorkflowDetector_SkillNameGeneration(t *testing.T) {
	name := generateSkillName([]string{"Read", "Edit", "Bash"})
	if name != "read-edit-bash" {
		t.Errorf("expected 'read-edit-bash', got %q", name)
	}
}

func TestWorkflowDetector_SkillNameDedup(t *testing.T) {
	// Duplicate tools in sequence should be deduped in name.
	name := generateSkillName([]string{"Read", "Edit", "Read", "Bash"})
	if name != "read-edit-bash" {
		t.Errorf("expected 'read-edit-bash', got %q", name)
	}
}

func TestWorkflowDetector_MarkSuggested(t *testing.T) {
	wd := NewWorkflowDetector()
	wd.minOccur = 1 // immediate for test

	msgs := []ChatMessage{
		{Role: "assistant", Content: `[{"type":"tool_use","name":"Read","id":"t1"}]`},
		{Role: "assistant", Content: `[{"type":"tool_use","name":"Edit","id":"t2"}]`},
		{Role: "assistant", Content: `[{"type":"tool_use","name":"Bash","id":"t3"}]`},
	}
	wd.RecordMessages(msgs)

	suggestions := wd.Suggestions()
	if len(suggestions) == 0 {
		t.Fatal("expected suggestion")
	}

	// Mark as suggested.
	wd.MarkSuggested(suggestions[0].Hash)

	// Should not suggest again.
	after := wd.Suggestions()
	if len(after) != 0 {
		t.Errorf("expected 0 suggestions after marking, got %d", len(after))
	}
}

func TestWorkflowDetector_IgnoresUserMessages(t *testing.T) {
	wd := NewWorkflowDetector()

	msgs := []ChatMessage{
		{Role: "user", Content: `[{"type":"tool_result","name":"Read","id":"t1"}]`},
		{Role: "user", Content: `some user text`},
	}
	wd.RecordMessages(msgs)

	// Should have no sequence tracked.
	if len(wd.currentSeq) != 0 {
		t.Errorf("expected empty sequence from user messages, got %d", len(wd.currentSeq))
	}
}

func TestWorkflowDetector_SkillBodyFormat(t *testing.T) {
	body := generateSkillBody("read-edit-bash", []string{"Read", "Edit", "Bash"})
	if body == "" {
		t.Fatal("expected non-empty body")
	}
	if !contains(body, "Step 1: Read") {
		t.Error("expected Step 1 in body")
	}
	if !contains(body, "Suggested workflow name: read-edit-bash") {
		t.Error("expected workflow naming guidance in body")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
