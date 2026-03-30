package neurorouter

import (
	"fmt"
	"strings"
)

// WireAPI identifies the upstream wire protocol.
type WireAPI string

const (
	WireAPIChatCompletions WireAPI = "chat_completions"
	WireAPIResponses       WireAPI = "responses"
)

// TargetCapabilities describes what semantics a configured target can preserve.
type TargetCapabilities struct {
	Model             string  `json:"model"`
	Provider          string  `json:"provider"`
	WireAPI           WireAPI `json:"wire_api"`
	Streaming         bool    `json:"streaming"`
	StructuredOutput  bool    `json:"structured_output"`
	Tools             bool    `json:"tools"`
	ToolResults       bool    `json:"tool_results"`
	ResponsesItems    bool    `json:"responses_items"`
	ReasoningItems    bool    `json:"reasoning_items"`
	SystemRole        bool    `json:"system_role"`
	DeveloperRole     bool    `json:"developer_role"`
	SafeForMechanical bool    `json:"safe_for_mechanical"`
	SafeForSensitive  bool    `json:"safe_for_sensitive"`
}

// RequestRequirements captures the semantics the incoming request needs preserved.
type RequestRequirements struct {
	WireAPI               WireAPI `json:"wire_api"`
	Streaming             bool    `json:"streaming"`
	StructuredOutput      bool    `json:"structured_output"`
	Tools                 bool    `json:"tools"`
	ToolResults           bool    `json:"tool_results"`
	ResponsesItems        bool    `json:"responses_items"`
	ReasoningPreservation bool    `json:"reasoning_preservation"`
	Intent                Intent  `json:"intent"`
}

// CompatibilityResult explains whether a target is compatible with a request.
type CompatibilityResult struct {
	OK      bool     `json:"ok"`
	Reasons []string `json:"reasons,omitempty"`
}

// CapabilityError reports that configured targets exist but none can preserve the request semantics.
type CapabilityError struct {
	Model   string
	Reasons []string
}

func (e *CapabilityError) Error() string {
	return fmt.Sprintf("no compatible target for model %q: missing capabilities %s", e.Model, strings.Join(e.Reasons, ", "))
}

// DeriveRequirements inspects the incoming request shape and records required semantics.
func DeriveRequirements(req *ResponsesRequest) RequestRequirements {
	out := RequestRequirements{
		WireAPI:   WireAPIResponses,
		Streaming: req.Stream,
		Intent:    IntentGeneral,
	}

	if req.Text != nil && req.Text.Format != nil {
		switch req.Text.Format.Type {
		case "json_schema", "json_object":
			out.StructuredOutput = true
		}
	}
	if len(req.Tools) > 0 {
		out.Tools = true
	}

	for _, item := range req.Input {
		if item.Type != "message" {
			out.ResponsesItems = true
		}

		switch item.Type {
		case "reasoning":
			out.ReasoningPreservation = true
		case "function_call", "tool_call", "shell_call", "computer_call":
			out.Tools = true
			out.ResponsesItems = true
		case "function_call_output", "tool_result", "shell_call_output", "computer_call_output":
			out.Tools = true
			out.ToolResults = true
			out.ResponsesItems = true
		}
	}

	return out
}

// Compatible applies hard gates between request semantics and target capabilities.
func Compatible(req RequestRequirements, cap TargetCapabilities) CompatibilityResult {
	var reasons []string

	if req.Streaming && !cap.Streaming {
		reasons = append(reasons, "streaming")
	}
	if req.StructuredOutput && !cap.StructuredOutput {
		reasons = append(reasons, "structured_output")
	}
	if req.Tools && !cap.Tools {
		reasons = append(reasons, "tools")
	}
	if req.ToolResults && !cap.ToolResults {
		reasons = append(reasons, "tool_results")
	}
	if req.ResponsesItems && !cap.ResponsesItems {
		reasons = append(reasons, "responses_items")
	}
	if req.ReasoningPreservation && !cap.ReasoningItems {
		reasons = append(reasons, "reasoning_items")
	}

	return CompatibilityResult{
		OK:      len(reasons) == 0,
		Reasons: reasons,
	}
}

// DefaultTargetCapabilities returns conservative capabilities for the current chat-completions upstream path.
func DefaultTargetCapabilities(model string, target Target) TargetCapabilities {
	provider := detectProviderName(target.BaseURL)
	if provider == "openai" {
		return TargetCapabilities{
			Model:             model,
			Provider:          provider,
			WireAPI:           WireAPIResponses,
			Streaming:         true,
			StructuredOutput:  true,
			Tools:             true,
			ToolResults:       true,
			ResponsesItems:    true,
			ReasoningItems:    true,
			SystemRole:        true,
			DeveloperRole:     true,
			SafeForMechanical: false,
			SafeForSensitive:  true,
		}
	}

	return TargetCapabilities{
		Model:             model,
		Provider:          provider,
		WireAPI:           WireAPIChatCompletions,
		Streaming:         true,
		StructuredOutput:  false,
		Tools:             false,
		ToolResults:       false,
		ResponsesItems:    false,
		ReasoningItems:    false,
		SystemRole:        true,
		DeveloperRole:     false,
		SafeForMechanical: false,
		SafeForSensitive:  true,
	}
}

func detectProviderName(baseURL string) string {
	lower := strings.ToLower(baseURL)
	switch {
	case strings.Contains(lower, "anthropic"):
		return "anthropic"
	case strings.Contains(lower, "openai"):
		return "openai"
	case strings.Contains(lower, "groq"):
		return "groq"
	case strings.Contains(lower, "deepseek"):
		return "deepseek"
	default:
		return "openai-compatible"
	}
}
