package neurorouter

import (
	"encoding/json"
	"testing"
)

func TestDeriveRequirements(t *testing.T) {
	req := &ResponsesRequest{
		Stream: true,
		Text: &ResponseText{
			Format: &ResponseFormat{Type: "json_schema"},
		},
		Tools: []ResponseTool{{Type: "function"}},
		Input: []InputItem{
			{Type: "message", Role: "user", Content: json.RawMessage(`"hello"`)},
			{Type: "reasoning", Content: json.RawMessage(`"thinking"`)},
			{Type: "shell_call_output", Content: json.RawMessage(`"stdout"`)},
		},
	}

	got := DeriveRequirements(req)

	if got.WireAPI != WireAPIResponses {
		t.Fatalf("wire api: got %q", got.WireAPI)
	}
	if !got.Streaming {
		t.Fatal("expected streaming requirement")
	}
	if !got.StructuredOutput {
		t.Fatal("expected structured output requirement")
	}
	if !got.Tools {
		t.Fatal("expected tools requirement")
	}
	if !got.ToolResults {
		t.Fatal("expected tool results requirement")
	}
	if !got.ResponsesItems {
		t.Fatal("expected responses items requirement")
	}
	if !got.ReasoningPreservation {
		t.Fatal("expected reasoning preservation requirement")
	}
}

func TestCompatibleReportsPreciseReasons(t *testing.T) {
	req := RequestRequirements{
		WireAPI:               WireAPIResponses,
		Streaming:             true,
		StructuredOutput:      true,
		Tools:                 true,
		ToolResults:           true,
		ResponsesItems:        true,
		ReasoningPreservation: true,
	}

	cap := TargetCapabilities{
		Model:     "test",
		Provider:  "openai",
		WireAPI:   WireAPIChatCompletions,
		Streaming: false,
	}

	got := Compatible(req, cap)
	if got.OK {
		t.Fatal("expected incompatibility")
	}

	wantReasons := map[string]bool{
		"streaming":         false,
		"structured_output": false,
		"tools":             false,
		"tool_results":      false,
		"responses_items":   false,
		"reasoning_items":   false,
	}
	for _, reason := range got.Reasons {
		if _, ok := wantReasons[reason]; ok {
			wantReasons[reason] = true
		}
	}
	for reason, seen := range wantReasons {
		if !seen {
			t.Fatalf("missing incompatibility reason %q in %v", reason, got.Reasons)
		}
	}
}

func TestDefaultTargetCapabilities(t *testing.T) {
	got := DefaultTargetCapabilities("test-model", Target{BaseURL: "https://api.openai.com"})
	if got.Model != "test-model" {
		t.Fatalf("model: got %q", got.Model)
	}
	if got.Provider != "openai" {
		t.Fatalf("provider: got %q", got.Provider)
	}
	if got.WireAPI != WireAPIResponses {
		t.Fatalf("wire api: got %q", got.WireAPI)
	}
	if !got.Streaming {
		t.Fatal("expected streaming to be enabled by default")
	}
	if !got.ResponsesItems {
		t.Fatal("responses items should be enabled for official OpenAI targets")
	}
	if !got.Tools || !got.ToolResults || !got.ReasoningItems {
		t.Fatalf("official OpenAI defaults should preserve Responses semantics: %+v", got)
	}
}
