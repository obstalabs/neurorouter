package neurorouter

import "testing"

func TestIntentDetector_TestWriting(t *testing.T) {
	d := NewIntentDetector()

	tests := []string{
		"Write tests for the filter.go file",
		"Add unit tests to cover the edge cases",
		"Can you create test coverage for this function?",
		"write test for this function",
		"increase test coverage for proxy.go",
	}

	for _, content := range tests {
		msgs := []ChatMessage{{Role: "user", Content: content}}
		intent := d.Detect(msgs)
		if intent != IntentTestWriting {
			t.Errorf("expected test_writing for %q, got %s", content, intent)
		}
	}
}

func TestIntentDetector_LintFormat(t *testing.T) {
	d := NewIntentDetector()

	tests := []string{
		"fix lint errors in proxy.go",
		"run gofmt on all files",
		"fix the formatting issues",
		"there's an eslint error here",
	}

	for _, content := range tests {
		msgs := []ChatMessage{{Role: "user", Content: content}}
		intent := d.Detect(msgs)
		if intent != IntentLintFormat {
			t.Errorf("expected lint_format for %q, got %s", content, intent)
		}
	}
}

func TestIntentDetector_Boilerplate(t *testing.T) {
	d := NewIntentDetector()

	tests := []string{
		"create a new file for the auth handler",
		"generate a new endpoint for /users",
		"scaffold the database model",
		"add boilerplate for the CLI command",
	}

	for _, content := range tests {
		msgs := []ChatMessage{{Role: "user", Content: content}}
		intent := d.Detect(msgs)
		if intent != IntentBoilerplate {
			t.Errorf("expected boilerplate for %q, got %s", content, intent)
		}
	}
}

func TestIntentDetector_Refactor(t *testing.T) {
	d := NewIntentDetector()

	tests := []string{
		"refactor this function to be cleaner",
		"extract function from the handler",
		"rename processRequest to handleRequest",
		"clean up the error handling",
	}

	for _, content := range tests {
		msgs := []ChatMessage{{Role: "user", Content: content}}
		intent := d.Detect(msgs)
		if intent != IntentRefactor {
			t.Errorf("expected refactor for %q, got %s", content, intent)
		}
	}
}

func TestIntentDetector_Architecture(t *testing.T) {
	d := NewIntentDetector()

	tests := []string{
		"what's the best approach for handling auth?",
		"design the system for multi-tenancy",
		"should we use Redis or in-memory cache?",
		"plan the implementation for the billing module",
	}

	for _, content := range tests {
		msgs := []ChatMessage{{Role: "user", Content: content}}
		intent := d.Detect(msgs)
		if intent != IntentArchitect {
			t.Errorf("expected architecture for %q, got %s", content, intent)
		}
	}
}

func TestIntentDetector_General(t *testing.T) {
	d := NewIntentDetector()

	tests := []string{
		"hello",
		"explain how the proxy works",
		"what does this error mean?",
		"fix the bug where requests hang",
	}

	for _, content := range tests {
		msgs := []ChatMessage{{Role: "user", Content: content}}
		intent := d.Detect(msgs)
		if intent != IntentGeneral {
			t.Errorf("expected general for %q, got %s", content, intent)
		}
	}
}

func TestIntentDetector_EmptyMessages(t *testing.T) {
	d := NewIntentDetector()

	intent := d.Detect(nil)
	if intent != IntentGeneral {
		t.Errorf("expected general for nil messages, got %s", intent)
	}

	intent = d.Detect([]ChatMessage{})
	if intent != IntentGeneral {
		t.Errorf("expected general for empty messages, got %s", intent)
	}
}

func TestIntentDetector_UsesLastUserMessage(t *testing.T) {
	d := NewIntentDetector()

	msgs := []ChatMessage{
		{Role: "user", Content: "explain the architecture"}, // architecture
		{Role: "assistant", Content: "Here's the design..."},
		{Role: "user", Content: "now write tests for proxy.go"}, // test_writing
	}

	intent := d.Detect(msgs)
	if intent != IntentTestWriting {
		t.Errorf("expected test_writing from last user message, got %s", intent)
	}
}

func TestIntentDetector_Routing(t *testing.T) {
	d := NewIntentDetector()
	d.SetRouting(IntentTestWriting, "qwen-coder")
	d.SetRouting(IntentLintFormat, "deepseek-chat")

	msgs := []ChatMessage{{Role: "user", Content: "write tests for this"}}
	intent, model := d.DetectAndRoute(msgs)

	if intent != IntentTestWriting {
		t.Errorf("expected test_writing, got %s", intent)
	}
	if model != "qwen-coder" {
		t.Errorf("expected qwen-coder, got %q", model)
	}
}

func TestIntentDetector_RoutingNoOverride(t *testing.T) {
	d := NewIntentDetector()

	msgs := []ChatMessage{{Role: "user", Content: "hello"}}
	_, model := d.DetectAndRoute(msgs)

	if model != "" {
		t.Errorf("expected empty model for general intent, got %q", model)
	}
}
