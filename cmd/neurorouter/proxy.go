package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/ppiankov/neurorouter/internal/neurorouter"
	"github.com/spf13/cobra"
)

// Known provider endpoints for auto-detection from environment variables.
var providers = []struct {
	envKey  string
	baseURL string
	name    string
}{
	{"ANTHROPIC_API_KEY", "https://api.anthropic.com", "Anthropic"},
	{"OPENAI_API_KEY", "https://api.openai.com", "OpenAI"},
	{"GROQ_API_KEY", "https://api.groq.com/openai", "Groq"},
	{"DEEPSEEK_API_KEY", "https://api.deepseek.com", "DeepSeek"},
}

var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Start the local proxy",
	Long:  "Start the local proxy with filtering, secret protection, and compatibility handling for supported upstreams.",
	RunE:  runProxy,
}

type proxyRuntimeSettings struct {
	Listen           string
	Target           string
	APIKey           string
	ProtectPolicy    string
	PublicBind       bool
	ExposeManagement bool
	NoProtect        bool
	NoFilter         bool
	NoCache          bool
	DryRun           bool
	Debug            bool
}

func addProxyFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.String("listen", neurorouter.DefaultListenAddress, "listen address")
	f.Bool("public", false, "allow binding the proxy to a non-loopback interface")
	f.Bool("expose-management", false, "expose /v1/audit and /v1/suggestions on public binds")
	f.String("target", "", "upstream URL (auto-detected from API key env vars if omitted)")
	f.String("api-key", "", "API key (or env:VAR_NAME; auto-detected if omitted)")
	f.String("protect-policy", "warn", "secret policy: block, redact, warn")
	f.Bool("no-protect", false, "disable secret detection")
	f.Bool("no-filter", false, "disable content filters")
	f.Bool("no-cache", false, "disable neurocache pattern detection")
	f.Bool("dry-run", false, "show filtered vs original without sending upstream")
	f.Bool("debug", false, "enable debug logging")
}

func init() {
	addProxyFlags(proxyCmd)
}

func runProxy(cmd *cobra.Command, _ []string) error {
	loadedConfig, err := neurorouter.LoadConfig("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	settings, err := resolveProxySettings(cmd, loadedConfig)
	if err != nil {
		return err
	}

	debug := settings.Debug
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	listen := settings.Listen
	publicBind := settings.PublicBind
	exposeManagement := settings.ExposeManagement
	target := settings.Target
	apiKey := settings.APIKey
	protectPolicy := settings.ProtectPolicy
	noProtect := settings.NoProtect
	noFilter := settings.NoFilter
	noCache := settings.NoCache
	dryRun := settings.DryRun

	// Resolve API key from env: prefix.
	if strings.HasPrefix(apiKey, "env:") {
		apiKey = os.Getenv(strings.TrimPrefix(apiKey, "env:"))
	}

	// Auto-detect from environment if not specified.
	detectedProvider := ""
	if target == "" || apiKey == "" {
		for _, p := range providers {
			if key := os.Getenv(p.envKey); key != "" {
				if target == "" {
					target = p.baseURL
				}
				if apiKey == "" {
					apiKey = key
				}
				detectedProvider = p.name
				break
			}
		}
	}

	if target == "" {
		fmt.Fprintln(os.Stderr, "error: no upstream target found")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Set --target or export an API key environment variable:")
		fmt.Fprintln(os.Stderr, "  export ANTHROPIC_API_KEY=...")
		fmt.Fprintln(os.Stderr, "  export OPENAI_API_KEY=...")
		fmt.Fprintln(os.Stderr, "  neurorouter proxy --target https://api.openai.com --api-key env:OPENAI_API_KEY")
		return fmt.Errorf("no upstream target")
	}

	cfg := neurorouter.ProxyConfig{
		Listen:            listen,
		AllowPublicListen: publicBind,
		ExposeManagement:  exposeManagement,
		Targets: map[string]neurorouter.Target{
			"default": {BaseURL: target, APIKey: apiKey},
		},
		DryRun: dryRun,
	}

	if !noFilter {
		cfg.Filters = neurorouter.FilterConfig{
			StaleReads: true, Thinking: true, OrphanedResults: true,
			FailedRetries: true, SystemReminders: true, OversizedBlocks: true,
		}
	}

	if !noProtect {
		cfg.Protection = neurorouter.ProtectConfig{
			Enabled: true,
			Policy:  neurorouter.SecretPolicy(protectPolicy),
		}
	}

	if !noCache {
		cfg.Neurocache = neurorouter.NeurocacheConfig{Enabled: true}
	}

	// Session tracking for first-run and shutdown summary.
	var (
		requestCount int
		totalBefore  int
		totalAfter   int
		totalSecrets int
		firstRequest = true
	)

	cfg.OnRequest = func(e neurorouter.RequestEvent) {
		requestCount++
		totalBefore += e.BytesBefore
		totalAfter += e.BytesAfter
		totalSecrets += e.SecretsFound

		saved := 0
		if e.BytesBefore > 0 {
			saved = (e.BytesBefore - e.BytesAfter) * 100 / e.BytesBefore
		}
		filters := ""
		if len(e.FiltersRun) > 0 {
			filters = "  filters=[" + strings.Join(e.FiltersRun, ",") + "]"
		}
		secrets := ""
		if e.SecretsFound > 0 {
			secrets = fmt.Sprintf("  secrets=%d", e.SecretsFound)
		}
		fmt.Fprintf(os.Stderr, "[req] model=%s  bytes=%d→%d (%d%% saved)%s%s\n",
			e.Model, e.BytesBefore, e.BytesAfter, saved, filters, secrets)

		if firstRequest {
			firstRequest = false
			if saved > 0 {
				tokensSaved := (e.BytesBefore - e.BytesAfter) / 4
				cost := float64(tokensSaved) * 3.0 / 1_000_000
				fmt.Fprintf(os.Stderr, "\n      NeuroRouter saved %d tokens ($%.4f) on your first request.\n", tokensSaved, cost)
				fmt.Fprintf(os.Stderr, "      Run 'neurorouter stats' to see cumulative savings.\n\n")
			}
		}
	}

	p := neurorouter.NewProxy(cfg)
	addr, err := p.Start()
	if err != nil {
		return fmt.Errorf("start proxy: %w", err)
	}

	fmt.Fprintf(os.Stderr, "neurorouter listening on %s\n", addr)
	if detectedProvider != "" {
		fmt.Fprintf(os.Stderr, "  target:  %s (auto-detected from %s)\n", target, detectedProvider)
	} else {
		fmt.Fprintf(os.Stderr, "  target:  %s\n", target)
	}
	fmt.Fprintf(os.Stderr, "  protect: %v (policy: %s)\n", !noProtect, protectPolicy)
	fmt.Fprintf(os.Stderr, "  filter:  %v\n", !noFilter)
	fmt.Fprintf(os.Stderr, "  cache:   %v\n", !noCache)
	if publicBind {
		fmt.Fprintf(os.Stderr, "  public:  true\n")
		if exposeManagement {
			fmt.Fprintf(os.Stderr, "  manage:  exposed on public bind\n")
		} else {
			fmt.Fprintf(os.Stderr, "  manage:  disabled on public bind (use --expose-management to opt in)\n")
		}
	} else {
		fmt.Fprintf(os.Stderr, "  public:  false (loopback only)\n")
	}
	if dryRun {
	fmt.Fprintf(os.Stderr, "  dry-run: enabled (requests will NOT be forwarded)\n")
	}
	fmt.Fprintf(os.Stderr, "\nTo use with a Responses-compatible client:\n")
	fmt.Fprintf(os.Stderr, "  export OPENAI_BASE_URL=http://%s\n", addr)
	fmt.Fprintf(os.Stderr, "  # for Codex, prefer a provider profile with wire_api=responses\n\n")
	fmt.Fprintf(os.Stderr, "Waiting for requests...\n")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	fmt.Fprintln(os.Stderr, "\nshutting down...")
	_ = p.Stop()

	// Session summary.
	if requestCount > 0 {
		bytesSaved := totalBefore - totalAfter
		tokensSaved := bytesSaved / 4
		cost := float64(tokensSaved) * 3.0 / 1_000_000
		savedPct := 0
		if totalBefore > 0 {
			savedPct = bytesSaved * 100 / totalBefore
		}
		fmt.Fprintf(os.Stderr, "\nSession summary (%d requests):\n", requestCount)
		fmt.Fprintf(os.Stderr, "  Saved: %dKB / %d tokens / $%.4f (%d%% noise removed)\n",
			bytesSaved/1024, tokensSaved, cost, savedPct)
		if totalSecrets > 0 {
			fmt.Fprintf(os.Stderr, "  Secrets caught: %d\n", totalSecrets)
		}
	}

	return nil
}

func resolveProxySettings(cmd *cobra.Command, cfg *neurorouter.Config) (proxyRuntimeSettings, error) {
	if cfg == nil {
		cfg = neurorouter.DefaultConfig()
	}

	settings := proxyRuntimeSettings{
		Listen:           listenAddressForPort(cfg.ListenPort),
		Target:           cfg.Upstream,
		ProtectPolicy:    cfg.ProtectPolicy,
		APIKey:           flagString(cmd, "api-key"),
		PublicBind:       flagBool(cmd, "public"),
		ExposeManagement: flagBool(cmd, "expose-management"),
		NoProtect:        flagBool(cmd, "no-protect"),
		NoFilter:         flagBool(cmd, "no-filter"),
		NoCache:          flagBool(cmd, "no-cache"),
		DryRun:           flagBool(cmd, "dry-run"),
		Debug:            flagBool(cmd, "debug"),
	}

	if cmd.Flags().Changed("listen") {
		settings.Listen = flagString(cmd, "listen")
	}
	if cmd.Flags().Changed("target") {
		settings.Target = flagString(cmd, "target")
	}
	if cmd.Flags().Changed("protect-policy") {
		settings.ProtectPolicy = flagString(cmd, "protect-policy")
	}

	return settings, nil
}

func listenAddressForPort(port int) string {
	if port <= 0 {
		port = neurorouter.DefaultConfig().ListenPort
	}
	return "127.0.0.1:" + strconv.Itoa(port)
}

func flagString(cmd *cobra.Command, name string) string {
	val, _ := cmd.Flags().GetString(name)
	return val
}

func flagBool(cmd *cobra.Command, name string) bool {
	val, _ := cmd.Flags().GetBool(name)
	return val
}
