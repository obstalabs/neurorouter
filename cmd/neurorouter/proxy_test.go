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
}

func TestResolveProxySettings_ConfigValues(t *testing.T) {
	cfg := neurorouter.DefaultConfig()
	cfg.ListenPort = 5005
	cfg.Upstream = "https://config.example"
	cfg.ProtectPolicy = "block"

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
		got := startupAuthMode("", "", "")
		want := "forward client Authorization header (API key or OAuth token)"
		if got != want {
			t.Fatalf("auth mode: got %q, want %q", got, want)
		}
	})

	t.Run("reports env configured key", func(t *testing.T) {
		got := startupAuthMode("env:OPENAI_API_KEY", "sk-test", "")
		want := "configured on proxy from OPENAI_API_KEY"
		if got != want {
			t.Fatalf("auth mode: got %q, want %q", got, want)
		}
	})

	t.Run("reports auto detected provider key", func(t *testing.T) {
		got := startupAuthMode("", "sk-test", "OpenAI")
		want := "configured on proxy (auto-detected from OpenAI credentials)"
		if got != want {
			t.Fatalf("auth mode: got %q, want %q", got, want)
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
