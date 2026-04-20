package neurorouter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPrependAlertsToResponsesAPIResponse(t *testing.T) {
	resp := &ResponsesAPIResponse{
		Output: []OutputItem{{
			Type:   "message",
			Role:   "assistant",
			Status: "completed",
			Content: []OutputContent{{
				Type: "output_text",
				Text: "native ok",
			}},
		}},
	}

	prependAlertsToResponsesAPIResponse(resp, []Alert{{
		Tier:    TierImportant,
		Message: "Shaped 2K tokens of context waste (~$0.01 avoided)",
	}})

	text := resp.Output[0].Content[0].Text
	if !strings.HasPrefix(text, "[NEUROROUTER] Shaped 2K tokens") {
		t.Fatalf("expected alert prefix, got %q", text)
	}
	if !strings.Contains(text, "native ok") {
		t.Fatalf("expected original text to remain, got %q", text)
	}
}

func TestInjectAlertsIntoResponsesBody_AddsOutputWhenMissing(t *testing.T) {
	body := []byte(`{"id":"resp-native","object":"response","status":"completed"}`)

	rewritten, err := injectAlertsIntoResponsesBody(body, []Alert{{
		Tier:    TierCritical,
		Message: "2 secret(s) detected — forwarded (policy: warn)",
	}})
	if err != nil {
		t.Fatalf("inject alerts: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(rewritten, &doc); err != nil {
		t.Fatalf("decode rewritten body: %v", err)
	}
	output := doc["output"].([]any)
	first := output[0].(map[string]any)
	content := first["content"].([]any)
	part := content[0].(map[string]any)
	if got := part["text"].(string); !strings.HasPrefix(got, "[NEUROROUTER] 2 secret(s) detected") {
		t.Fatalf("alert text: got %q", got)
	}
}

func TestRewriteResponsesEventPayload_PrependsAlertsOnce(t *testing.T) {
	state := newResponsesAlertStreamState([]Alert{{
		Tier:    TierImportant,
		Message: "Shaped 2K tokens of context waste (~$0.01 avoided)",
	}})

	delta, err := rewriteResponsesEventPayload(
		[]byte(`{"type":"response.output_text.delta","delta":"hello"}`),
		"response.output_text.delta",
		state,
	)
	if err != nil {
		t.Fatalf("rewrite delta: %v", err)
	}
	if !strings.Contains(string(delta), "[NEUROROUTER] Shaped 2K tokens") {
		t.Fatalf("expected alert in first delta: %s", string(delta))
	}

	secondDelta, err := rewriteResponsesEventPayload(
		[]byte(`{"type":"response.output_text.delta","delta":" world"}`),
		"response.output_text.delta",
		state,
	)
	if err != nil {
		t.Fatalf("rewrite second delta: %v", err)
	}
	if strings.Count(string(secondDelta), "[NEUROROUTER]") != 0 {
		t.Fatalf("expected no duplicate alert in second delta: %s", string(secondDelta))
	}

	completed, err := rewriteResponsesEventPayload(
		[]byte(`{"type":"response.completed","response":{"id":"resp-native","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello world"}],"status":"completed"}]}}`),
		"response.completed",
		state,
	)
	if err != nil {
		t.Fatalf("rewrite completed: %v", err)
	}
	if !strings.Contains(string(completed), "[NEUROROUTER] Shaped 2K tokens") {
		t.Fatalf("expected alert in completed payload: %s", string(completed))
	}
}
