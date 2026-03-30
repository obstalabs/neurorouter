package neurorouter

import (
	"strings"
	"testing"
)

func TestFilterGuard_PreCheck_CredentialPattern(t *testing.T) {
	fg := NewFilterGuard()
	msgs := []ChatMessage{
		{Role: "user", Content: buildSecret("password", "=MySecretPass123!")},
	}
	warnings := fg.PreCheck(msgs)
	if len(warnings) == 0 {
		t.Fatal("expected warning for credential pattern")
	}
	if warnings[0].TriggerName != "credential_pattern" {
		t.Errorf("expected credential_pattern, got %s", warnings[0].TriggerName)
	}
}

func TestFilterGuard_PreCheck_SSHKey(t *testing.T) {
	fg := NewFilterGuard()
	msgs := []ChatMessage{
		{Role: "user", Content: buildSecret("-----BEGIN ", "RSA PRIVATE KEY-----")},
	}
	warnings := fg.PreCheck(msgs)
	if len(warnings) == 0 {
		t.Fatal("expected warning for SSH key")
	}
	if warnings[0].TriggerName != "ssh_key_block" {
		t.Errorf("expected ssh_key_block, got %s", warnings[0].TriggerName)
	}
}

func TestFilterGuard_PreCheck_SecurityResearch(t *testing.T) {
	fg := NewFilterGuard()
	msgs := []ChatMessage{
		{Role: "user", Content: "I'm testing for SQL injection vulnerabilities in my auth handler"},
	}
	warnings := fg.PreCheck(msgs)
	found := false
	for _, w := range warnings {
		if w.TriggerName == "security_research" {
			found = true
		}
	}
	if !found {
		t.Error("expected security_research warning")
	}
}

func TestFilterGuard_PreCheck_Clean(t *testing.T) {
	fg := NewFilterGuard()
	msgs := []ChatMessage{
		{Role: "user", Content: "Please add error handling to the handler function"},
	}
	warnings := fg.PreCheck(msgs)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings on clean content, got %d", len(warnings))
	}
}

func TestFilterGuard_PreCheck_Dedup(t *testing.T) {
	fg := NewFilterGuard()
	// Same trigger type in multiple messages — should only warn once.
	msgs := []ChatMessage{
		{Role: "user", Content: buildSecret("password", "=secret1234567!")},
		{Role: "user", Content: buildSecret("secret", ":another_value_here")},
	}
	warnings := fg.PreCheck(msgs)
	credCount := 0
	for _, w := range warnings {
		if w.TriggerName == "credential_pattern" {
			credCount++
		}
	}
	if credCount > 1 {
		t.Errorf("expected dedup to 1 credential warning, got %d", credCount)
	}
}

func TestFilterGuard_RecordBlock(t *testing.T) {
	fg := NewFilterGuard()
	msgs := []ChatMessage{
		{Role: "user", Content: "explain how to detect SQL injection in this code"},
	}
	fg.RecordBlock(msgs, `{"error": "content filter policy violation"}`)

	blocks := fg.Blocks()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].TriggerName == "" {
		t.Error("expected trigger name")
	}
	if blocks[0].Advice == "" {
		t.Error("expected advice")
	}
}

func TestFilterGuard_FormatTrace(t *testing.T) {
	fg := NewFilterGuard()

	// No blocks.
	trace := fg.FormatTrace()
	if !strings.Contains(trace, "No content filter blocks") {
		t.Error("expected empty trace message")
	}

	// Add a block.
	fg.RecordBlock([]ChatMessage{{Role: "user", Content: "test exploit payload"}}, "blocked")
	trace = fg.FormatTrace()
	if !strings.Contains(trace, "Block 1") {
		t.Error("expected block in trace")
	}
	if !strings.Contains(trace, "Fix:") {
		t.Error("expected fix advice in trace")
	}
}

func TestIsFilterError(t *testing.T) {
	tests := []struct {
		code int
		body string
		want bool
	}{
		{400, `{"error": "content filter policy"}`, true},
		{400, `{"error": "content_policy_violation"}`, true},
		{400, `{"error": "output blocked by safety"}`, true},
		{400, `{"error": "invalid request"}`, false},
		{200, `{"result": "ok"}`, false},
		{429, `{"error": "rate limited"}`, false},
		{400, `{"error": "blocked by content moderation"}`, true},
	}

	for _, tt := range tests {
		got := IsFilterError(tt.code, tt.body)
		if got != tt.want {
			t.Errorf("IsFilterError(%d, %q) = %v, want %v", tt.code, tt.body, got, tt.want)
		}
	}
}
