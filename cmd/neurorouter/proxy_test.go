package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ppiankov/neurorouter/internal/neurorouter"
	"github.com/spf13/cobra"
)

func TestProxyFlagDefaults(t *testing.T) {
	cmd := newProxyTestCommand()

	if got := cmd.Flags().Lookup("listen").DefValue; got != neurorouter.DefaultListenAddress {
		t.Fatalf("listen default: got %q, want %q", got, neurorouter.DefaultListenAddress)
	}
	if got := cmd.Flags().Lookup("public").DefValue; got != "false" {
		t.Fatalf("public default: got %q, want false", got)
	}
	if got := cmd.Flags().Lookup("expose-management").DefValue; got != "false" {
		t.Fatalf("expose-management default: got %q, want false", got)
	}
	if got := cmd.Flags().Lookup("client-auth").DefValue; got != "false" {
		t.Fatalf("client-auth default: got %q, want false", got)
	}
	if got := cmd.Flags().Lookup("shell-max-output-bytes").DefValue; got != "0" {
		t.Fatalf("shell-max-output-bytes default: got %q, want 0", got)
	}
	if got := cmd.Flags().Lookup("input-price-per-million-usd").DefValue; got != "3" {
		t.Fatalf("input-price-per-million-usd default: got %q, want 3", got)
	}
	if got := cmd.Flags().Lookup("secret-report").DefValue; got != "off" {
		t.Fatalf("secret-report default: got %q, want off", got)
	}
}

func TestResolveProxySettings_Defaults(t *testing.T) {
	settings, err := resolveProxySettings(newProxyTestCommand(), neurorouter.DefaultConfig())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if settings.Listen != neurorouter.DefaultListenAddress {
		t.Fatalf("listen: got %q, want %q", settings.Listen, neurorouter.DefaultListenAddress)
	}
	if settings.ProtectPolicy != "warn" {
		t.Fatalf("protect policy: got %q, want warn", settings.ProtectPolicy)
	}
	if settings.Target != "" {
		t.Fatalf("target: got %q, want empty", settings.Target)
	}
	if settings.ShellMaxBytes != 0 {
		t.Fatalf("shell max bytes: got %d, want 0", settings.ShellMaxBytes)
	}
	if settings.InputPricePerMillionUSD != neurorouter.DefaultInputPricePerMillionUSD {
		t.Fatalf("input price: got %.1f, want %.1f", settings.InputPricePerMillionUSD, neurorouter.DefaultInputPricePerMillionUSD)
	}
	if settings.SecretReport != secretReportOff {
		t.Fatalf("secret report: got %q, want off", settings.SecretReport)
	}
}

func TestResolveProxySettings_ConfigValues(t *testing.T) {
	cfg := neurorouter.DefaultConfig()
	cfg.ListenPort = 5005
	cfg.Upstream = "https://config.example"
	cfg.ProtectPolicy = "block"
	cfg.InputPricePerMillionUSD = 4.25

	settings, err := resolveProxySettings(newProxyTestCommand(), cfg)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if settings.Listen != "127.0.0.1:5005" {
		t.Fatalf("listen: got %q, want 127.0.0.1:5005", settings.Listen)
	}
	if settings.Target != "https://config.example" {
		t.Fatalf("target: got %q", settings.Target)
	}
	if settings.ProtectPolicy != "block" {
		t.Fatalf("protect policy: got %q", settings.ProtectPolicy)
	}
	if settings.InputPricePerMillionUSD != 4.25 {
		t.Fatalf("input price: got %.2f", settings.InputPricePerMillionUSD)
	}
}

func TestResolveProxySettings_EnvOverridesConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`
listen_port = 5000
upstream = "https://config.example"
protect_policy = "block"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("NEUROROUTER_LISTEN_PORT", "6000")
	t.Setenv("NEUROROUTER_UPSTREAM", "https://env.example")
	t.Setenv("NEUROROUTER_PROTECT_POLICY", "redact")

	cfg, err := neurorouter.LoadConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	settings, err := resolveProxySettings(newProxyTestCommand(), cfg)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if settings.Listen != "127.0.0.1:6000" {
		t.Fatalf("listen: got %q, want 127.0.0.1:6000", settings.Listen)
	}
	if settings.Target != "https://env.example" {
		t.Fatalf("target: got %q", settings.Target)
	}
	if settings.ProtectPolicy != "redact" {
		t.Fatalf("protect policy: got %q", settings.ProtectPolicy)
	}
}

func TestResolveProxySettings_FlagsOverrideLoadedConfig(t *testing.T) {
	cfg := neurorouter.DefaultConfig()
	cfg.ListenPort = 5000
	cfg.Upstream = "https://config.example"
	cfg.ProtectPolicy = "block"

	cmd := newProxyTestCommand()
	mustSetFlag(t, cmd, "listen", "localhost:7000")
	mustSetFlag(t, cmd, "target", "https://flag.example")
	mustSetFlag(t, cmd, "protect-policy", "warn")
	mustSetFlag(t, cmd, "secret-report", "redacted")
	mustSetFlag(t, cmd, "shell-max-output-bytes", "32768")
	mustSetFlag(t, cmd, "input-price-per-million-usd", "5.5")

	settings, err := resolveProxySettings(cmd, cfg)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if settings.Listen != "localhost:7000" {
		t.Fatalf("listen: got %q", settings.Listen)
	}
	if settings.Target != "https://flag.example" {
		t.Fatalf("target: got %q", settings.Target)
	}
	if settings.ProtectPolicy != "warn" {
		t.Fatalf("protect policy: got %q", settings.ProtectPolicy)
	}
	if settings.ShellMaxBytes != 32768 {
		t.Fatalf("shell max bytes: got %d, want 32768", settings.ShellMaxBytes)
	}
	if settings.InputPricePerMillionUSD != 5.5 {
		t.Fatalf("input price: got %.1f, want 5.5", settings.InputPricePerMillionUSD)
	}
	if settings.SecretReport != secretReportRedacted {
		t.Fatalf("secret report: got %q, want redacted", settings.SecretReport)
	}
}

func TestResolveProxySettings_RejectsInvalidSecretReport(t *testing.T) {
	cmd := newProxyTestCommand()
	mustSetFlag(t, cmd, "secret-report", "full")

	if _, err := resolveProxySettings(cmd, neurorouter.DefaultConfig()); err == nil {
		t.Fatal("expected invalid secret-report to fail")
	}
}

func TestConfigDerivedSettingsDriveRunningProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var req neurorouter.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		if req.Model != "test-model" {
			t.Fatalf("model: got %q", req.Model)
		}

		resp := neurorouter.ChatCompletionResponse{
			ID:      "chatcmpl-test",
			Model:   "test-model",
			Choices: []neurorouter.Choice{{Message: neurorouter.ChatMessage{Role: "assistant", Content: "ok"}}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	cfg := neurorouter.DefaultConfig()
	cfg.ListenPort = freePort(t)
	cfg.Upstream = upstream.URL

	settings, err := resolveProxySettings(newProxyTestCommand(), cfg)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	proxy := neurorouter.NewProxy(neurorouter.ProxyConfig{
		Listen: settings.Listen,
		Targets: map[string]neurorouter.Target{
			"default": {BaseURL: settings.Target},
		},
	})
	addr, err := proxy.Start()
	if err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer func() { _ = proxy.Stop() }()

	resp, err := http.Post("http://"+addr+"/v1/responses", "application/json", strings.NewReader(`{"model":"test-model","input":[{"type":"message","role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(body))
	}
}

func TestStartupAuthMode(t *testing.T) {
	t.Run("pass through when proxy has no key", func(t *testing.T) {
		got := startupAuthMode("", "", "", false)
		want := "forward client Authorization header (API key or OAuth token)"
		if got != want {
			t.Fatalf("auth mode: got %q, want %q", got, want)
		}
	})

	t.Run("reports env configured key", func(t *testing.T) {
		got := startupAuthMode("env:OPENAI_API_KEY", "sk-test", "", false)
		want := "configured on proxy from OPENAI_API_KEY"
		if got != want {
			t.Fatalf("auth mode: got %q, want %q", got, want)
		}
	})

	t.Run("reports auto detected provider key", func(t *testing.T) {
		got := startupAuthMode("", "sk-test", "OpenAI", false)
		want := "configured on proxy (auto-detected from OpenAI credentials)"
		if got != want {
			t.Fatalf("auth mode: got %q, want %q", got, want)
		}
	})

	t.Run("reports forced client auth", func(t *testing.T) {
		got := startupAuthMode("", "sk-test", "OpenAI", true)
		want := "forward client Authorization header (API key or OAuth token)"
		if got != want {
			t.Fatalf("auth mode: got %q, want %q", got, want)
		}
	})
}

func TestFormatRequestDelta(t *testing.T) {
	t.Run("shows small positive savings even when percent rounds to zero", func(t *testing.T) {
		got := formatRequestDelta(71042, 70952)
		want := "-90 bytes, 0% saved"
		if got != want {
			t.Fatalf("format delta: got %q, want %q", got, want)
		}
	})

	t.Run("shows zero delta cleanly", func(t *testing.T) {
		got := formatRequestDelta(41806, 41806)
		want := "0 bytes"
		if got != want {
			t.Fatalf("format delta: got %q, want %q", got, want)
		}
	})

	t.Run("shows slight growth cleanly", func(t *testing.T) {
		got := formatRequestDelta(41653, 41655)
		want := "+2 bytes"
		if got != want {
			t.Fatalf("format delta: got %q, want %q", got, want)
		}
	})
}

func TestFormatRequestSummary(t *testing.T) {
	t.Run("adds tokens and usd for savings", func(t *testing.T) {
		got := formatRequestSummary(72628, 64030, 3.0)
		want := "-8598 bytes, 11% saved; 2149 tokens; $0.0064"
		if got != want {
			t.Fatalf("format summary: got %q, want %q", got, want)
		}
	})

	t.Run("keeps growth concise", func(t *testing.T) {
		got := formatRequestSummary(41653, 41655, 3.0)
		want := "+2 bytes"
		if got != want {
			t.Fatalf("format summary: got %q, want %q", got, want)
		}
	})
}

func TestTrackRecurringFingerprint(t *testing.T) {
	counts := make(map[string]int)

	if got := trackRecurringFingerprint(counts, 72628, 64030, []string{"oversized_blocks"}); got != "" {
		t.Fatalf("first occurrence: got %q, want empty", got)
	}

	got := trackRecurringFingerprint(counts, 72628, 64030, []string{"oversized_blocks"})
	want := "fixed-delta oversized_blocks/8598B x2"
	if got != want {
		t.Fatalf("second occurrence: got %q, want %q", got, want)
	}
}

func TestAutoDetectProviderSettings(t *testing.T) {
	getenv := func(values map[string]string) func(string) string {
		return func(key string) string {
			return values[key]
		}
	}

	t.Run("explicit openai target does not borrow groq key", func(t *testing.T) {
		got := autoDetectProviderSettings("https://api.openai.com", "", true, getenv(map[string]string{
			"GROQ_API_KEY": "groq-key",
		}))

		if got.Target != "https://api.openai.com" {
			t.Fatalf("target: got %q", got.Target)
		}
		if got.APIKey != "" {
			t.Fatalf("api key: got %q, want empty", got.APIKey)
		}
		if got.TargetProvider != "" {
			t.Fatalf("target provider: got %q, want empty", got.TargetProvider)
		}
		if got.KeyProvider != "" {
			t.Fatalf("key provider: got %q, want empty", got.KeyProvider)
		}
	})

	t.Run("explicit openai target picks matching openai key", func(t *testing.T) {
		got := autoDetectProviderSettings("https://api.openai.com", "", true, getenv(map[string]string{
			"OPENAI_API_KEY": "openai-key",
			"GROQ_API_KEY":   "groq-key",
		}))

		if got.APIKey != "openai-key" {
			t.Fatalf("api key: got %q, want openai-key", got.APIKey)
		}
		if got.KeyProvider != "OpenAI" {
			t.Fatalf("key provider: got %q, want OpenAI", got.KeyProvider)
		}
		if got.TargetProvider != "" {
			t.Fatalf("target provider: got %q, want empty", got.TargetProvider)
		}
	})

	t.Run("missing target auto-detects both target and key from same provider", func(t *testing.T) {
		got := autoDetectProviderSettings("", "", true, getenv(map[string]string{
			"GROQ_API_KEY": "groq-key",
		}))

		if got.Target != "https://api.groq.com/openai" {
			t.Fatalf("target: got %q", got.Target)
		}
		if got.APIKey != "groq-key" {
			t.Fatalf("api key: got %q", got.APIKey)
		}
		if got.TargetProvider != "Groq" {
			t.Fatalf("target provider: got %q", got.TargetProvider)
		}
		if got.KeyProvider != "Groq" {
			t.Fatalf("key provider: got %q", got.KeyProvider)
		}
	})

	t.Run("client auth mode keeps target but skips matching key", func(t *testing.T) {
		got := autoDetectProviderSettings("https://api.openai.com", "", false, getenv(map[string]string{
			"OPENAI_API_KEY": "openai-key",
		}))

		if got.Target != "https://api.openai.com" {
			t.Fatalf("target: got %q", got.Target)
		}
		if got.APIKey != "" {
			t.Fatalf("api key: got %q, want empty", got.APIKey)
		}
		if got.KeyProvider != "" {
			t.Fatalf("key provider: got %q, want empty", got.KeyProvider)
		}
	})

	t.Run("client auth mode can still infer target from one exported key", func(t *testing.T) {
		got := autoDetectProviderSettings("", "", false, getenv(map[string]string{
			"OPENAI_API_KEY": "openai-key",
		}))

		if got.Target != "https://api.openai.com" {
			t.Fatalf("target: got %q, want https://api.openai.com", got.Target)
		}
		if got.APIKey != "" {
			t.Fatalf("api key: got %q, want empty", got.APIKey)
		}
		if got.TargetProvider != "OpenAI" {
			t.Fatalf("target provider: got %q, want OpenAI", got.TargetProvider)
		}
	})
}

func TestStartupClientHint(t *testing.T) {
	t.Run("responses hint for openai", func(t *testing.T) {
		got := startupClientHint("127.0.0.1:4010", "https://api.openai.com", "OpenAI")
		if !strings.Contains(got, "Codex or another Responses-compatible client") {
			t.Fatalf("missing responses hint: %q", got)
		}
		if !strings.Contains(got, "OPENAI_BASE_URL=http://127.0.0.1:4010") {
			t.Fatalf("missing OPENAI_BASE_URL hint: %q", got)
		}
	})

	t.Run("claude hint for anthropic", func(t *testing.T) {
		got := startupClientHint("127.0.0.1:4010", "https://api.anthropic.com", "Anthropic")
		if !strings.Contains(got, "To use with Claude Code") {
			t.Fatalf("missing Claude hint: %q", got)
		}
		if !strings.Contains(got, "ANTHROPIC_BASE_URL=http://127.0.0.1:4010") {
			t.Fatalf("missing ANTHROPIC_BASE_URL hint: %q", got)
		}
	})
}

func TestStartupTargetLabel(t *testing.T) {
	if got := startupTargetLabel("https://api.openai.com", ""); got != "https://api.openai.com" {
		t.Fatalf("target label: got %q", got)
	}

	want := "https://api.groq.com/openai (auto-detected from Groq)"
	if got := startupTargetLabel("https://api.groq.com/openai", "Groq"); got != want {
		t.Fatalf("target label: got %q, want %q", got, want)
	}
}

func TestAvailableProviderEnvKeys(t *testing.T) {
	got := availableProviderEnvKeys(func(key string) string {
		switch key {
		case "OPENAI_API_KEY":
			return "openai-key"
		case "GROQ_API_KEY":
			return "groq-key"
		default:
			return ""
		}
	})

	want := []string{"OPENAI_API_KEY", "GROQ_API_KEY"}
	if len(got) != len(want) {
		t.Fatalf("keys length: got %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("key %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestStartupAuthWarning(t *testing.T) {
	t.Run("warns when explicit target has no matching exported key", func(t *testing.T) {
		got := startupAuthWarning("https://api.openai.com", "", "", []string{"GROQ_API_KEY"}, false)
		want := "explicit target has no exported OPENAI_API_KEY; using pass-through client auth"
		if got != want {
			t.Fatalf("warning: got %q, want %q", got, want)
		}
	})

	t.Run("silent when proxy key is configured", func(t *testing.T) {
		got := startupAuthWarning("https://api.openai.com", "env:OPENAI_API_KEY", "openai-key", []string{"OPENAI_API_KEY"}, false)
		if got != "" {
			t.Fatalf("warning: got %q, want empty", got)
		}
	})

	t.Run("silent in client auth mode", func(t *testing.T) {
		got := startupAuthWarning("https://api.openai.com", "", "", []string{"OPENAI_API_KEY"}, true)
		if got != "" {
			t.Fatalf("warning: got %q, want empty", got)
		}
	})
}

func TestStartupAuthChoice(t *testing.T) {
	t.Run("offers both choices for explicit target", func(t *testing.T) {
		got := startupAuthChoice("https://api.openai.com", "", false)
		want := "use --api-key env:OPENAI_API_KEY for proxy auth, or --client-auth for client Authorization"
		if got != want {
			t.Fatalf("choice: got %q, want %q", got, want)
		}
	})

	t.Run("shows reverse hint in client auth mode", func(t *testing.T) {
		got := startupAuthChoice("https://api.openai.com", "", true)
		want := "use --api-key env:OPENAI_API_KEY to pin proxy auth instead"
		if got != want {
			t.Fatalf("choice: got %q, want %q", got, want)
		}
	})
}

func TestValidateProxyAuthSelection(t *testing.T) {
	t.Run("rejects client auth with explicit api key", func(t *testing.T) {
		err := validateProxyAuthSelection("https://api.openai.com", "env:OPENAI_API_KEY", true, []string{"OPENAI_API_KEY"})
		if err == nil || err.Error() != "--client-auth cannot be used with --api-key" {
			t.Fatalf("error: got %v", err)
		}
	})

	t.Run("rejects ambiguous target inference for client auth", func(t *testing.T) {
		err := validateProxyAuthSelection("", "", true, []string{"OPENAI_API_KEY", "GROQ_API_KEY"})
		if err == nil || err.Error() != "--client-auth requires --target when multiple provider keys are exported" {
			t.Fatalf("error: got %v", err)
		}
	})

	t.Run("allows explicit target in client auth mode", func(t *testing.T) {
		if err := validateProxyAuthSelection("https://api.openai.com", "", true, []string{"OPENAI_API_KEY", "GROQ_API_KEY"}); err != nil {
			t.Fatalf("error: got %v", err)
		}
	})
}

func newProxyTestCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "proxy"}
	addProxyFlags(cmd)
	return cmd
}

func mustSetFlag(t *testing.T, cmd *cobra.Command, name, value string) {
	t.Helper()
	if err := cmd.Flags().Set(name, value); err != nil {
		t.Fatalf("set flag %s: %v", name, err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	defer func() { _ = ln.Close() }()

	return ln.Addr().(*net.TCPAddr).Port
}
