package neurorouter

import "testing"

func TestParseProtocolMode(t *testing.T) {
	t.Run("defaults empty to auto", func(t *testing.T) {
		got, err := ParseProtocolMode("")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got != ProtocolModeAuto {
			t.Fatalf("mode: got %q, want %q", got, ProtocolModeAuto)
		}
	})

	t.Run("rejects unknown value", func(t *testing.T) {
		if _, err := ParseProtocolMode("bedrock"); err == nil {
			t.Fatal("expected invalid protocol to fail")
		}
	})
}

func TestResolveProtocolMode(t *testing.T) {
	t.Run("detects anthropic target", func(t *testing.T) {
		got, err := ResolveProtocolMode(ProxyConfig{
			Targets: map[string]Target{
				"default": {BaseURL: "https://api.anthropic.com"},
			},
		})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got != ProtocolModeAnthropic {
			t.Fatalf("mode: got %q, want %q", got, ProtocolModeAnthropic)
		}
	})

	t.Run("respects explicit mode when targets are local", func(t *testing.T) {
		got, err := ResolveProtocolMode(ProxyConfig{
			ProtocolMode: ProtocolModeAnthropic,
			Targets: map[string]Target{
				"default": {BaseURL: "http://127.0.0.1:9999"},
			},
		})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got != ProtocolModeAnthropic {
			t.Fatalf("mode: got %q, want %q", got, ProtocolModeAnthropic)
		}
	})

	t.Run("rejects explicit protocol mismatch", func(t *testing.T) {
		_, err := ResolveProtocolMode(ProxyConfig{
			ProtocolMode: ProtocolModeAnthropic,
			Targets: map[string]Target{
				"default": {BaseURL: "https://api.openai.com"},
			},
		})
		if err == nil {
			t.Fatal("expected explicit protocol mismatch to fail")
		}
	})
}
