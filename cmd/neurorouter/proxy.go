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

type providerSpec struct {
	envKey  string
	baseURL string
	name    string
}

// Known provider endpoints for auto-detection from environment variables.
var providers = []providerSpec{
	{"ANTHROPIC_API_KEY", "https://api.anthropic.com", "Anthropic"},
	{"OPENAI_API_KEY", "https://api.openai.com", "OpenAI"},
	{"GROQ_API_KEY", "https://api.groq.com/openai", "Groq"},
	{"DEEPSEEK_API_KEY", "https://api.deepseek.com", "DeepSeek"},
}

type autoDetectResult struct {
	Target         string
	APIKey         string
	TargetProvider string
	KeyProvider    string
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
	ClientAuth       bool
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
	f.Bool("client-auth", false, "forward client Authorization header instead of auto-configuring proxy auth")
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
	clientAuth := settings.ClientAuth
	protectPolicy := settings.ProtectPolicy
	noProtect := settings.NoProtect
	noFilter := settings.NoFilter
	noCache := settings.NoCache
	dryRun := settings.DryRun

	// Resolve API key from env: prefix.
	if strings.HasPrefix(apiKey, "env:") {
		apiKey = os.Getenv(strings.TrimPrefix(apiKey, "env:"))
	}

	availableAuthEnvKeys := availableProviderEnvKeys(os.Getenv)
	if err := validateProxyAuthSelection(target, settings.APIKey, clientAuth, availableAuthEnvKeys); err != nil {
		return err
	}

	detected := autoDetectProviderSettings(target, apiKey, !clientAuth, os.Getenv)
	target = detected.Target
	apiKey = detected.APIKey

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

		bytesSaved := e.BytesBefore - e.BytesAfter
		filters := ""
		if len(e.FiltersRun) > 0 {
			filters = "  filters=[" + strings.Join(e.FiltersRun, ",") + "]"
		}
		secrets := ""
		if e.SecretsFound > 0 {
			secrets = fmt.Sprintf("  secrets=%d", e.SecretsFound)
		}
		fmt.Fprintf(os.Stderr, "[req] model=%s  bytes=%d→%d (%s)%s%s\n",
			e.Model, e.BytesBefore, e.BytesAfter, formatRequestDelta(e.BytesBefore, e.BytesAfter), filters, secrets)

		if firstRequest {
			firstRequest = false
			if bytesSaved > 0 {
				tokensSaved := bytesSaved / 4
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
	fmt.Fprintf(os.Stderr, "  target:  %s\n", startupTargetLabel(target, detected.TargetProvider))
	fmt.Fprintf(os.Stderr, "  protect: %v (policy: %s)\n", !noProtect, protectPolicy)
	fmt.Fprintf(os.Stderr, "  filter:  %v\n", !noFilter)
	fmt.Fprintf(os.Stderr, "  cache:   %v\n", !noCache)
	if len(availableAuthEnvKeys) > 0 {
		fmt.Fprintf(os.Stderr, "  auth-env: %s\n", strings.Join(availableAuthEnvKeys, ", "))
	}
	fmt.Fprintf(os.Stderr, "  auth:    %s\n", startupAuthMode(settings.APIKey, apiKey, detected.KeyProvider, clientAuth))
	if choice := startupAuthChoice(settings.Target, settings.APIKey, clientAuth); choice != "" {
		fmt.Fprintf(os.Stderr, "  choose:  %s\n", choice)
	}
	if warning := startupAuthWarning(settings.Target, settings.APIKey, apiKey, availableAuthEnvKeys, clientAuth); warning != "" {
		fmt.Fprintf(os.Stderr, "  warn:    %s\n", warning)
	}
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
	fmt.Fprint(os.Stderr, startupClientHint(addr, target, detected.TargetProvider))
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

func requestSavedPercent(before, after int) int {
	if before <= 0 {
		return 0
	}
	return (before - after) * 100 / before
}

func formatRequestDelta(before, after int) string {
	delta := before - after
	switch {
	case delta > 0:
		return fmt.Sprintf("-%d bytes, %d%% saved", delta, requestSavedPercent(before, after))
	case delta < 0:
		return fmt.Sprintf("+%d bytes", -delta)
	default:
		return "0 bytes"
	}
}

func autoDetectProviderSettings(target, apiKey string, allowAutoKey bool, getenv func(string) string) autoDetectResult {
	result := autoDetectResult{Target: target, APIKey: apiKey}

	if result.Target == "" {
		for _, provider := range providers {
			key := getenv(provider.envKey)
			if key == "" {
				continue
			}

			result.Target = provider.baseURL
			result.TargetProvider = provider.name
			if allowAutoKey && result.APIKey == "" {
				result.APIKey = key
				result.KeyProvider = provider.name
			}
			return result
		}
		return result
	}

	if result.APIKey != "" {
		return result
	}
	if !allowAutoKey {
		return result
	}

	provider, ok := providerForTarget(result.Target)
	if !ok {
		return result
	}

	if key := getenv(provider.envKey); key != "" {
		result.APIKey = key
		result.KeyProvider = provider.name
	}

	return result
}

func providerForTarget(target string) (providerSpec, bool) {
	normalizedTarget := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(target)), "/")
	for _, provider := range providers {
		normalizedBase := strings.TrimSuffix(strings.ToLower(provider.baseURL), "/")
		if normalizedTarget == normalizedBase || strings.HasPrefix(normalizedTarget, normalizedBase+"/") {
			return provider, true
		}
	}
	return providerSpec{}, false
}

func availableProviderEnvKeys(getenv func(string) string) []string {
	keys := make([]string, 0, len(providers))
	for _, provider := range providers {
		if getenv(provider.envKey) != "" {
			keys = append(keys, provider.envKey)
		}
	}
	return keys
}

func startupTargetLabel(target, targetProvider string) string {
	if targetProvider == "" {
		return target
	}
	return fmt.Sprintf("%s (auto-detected from %s)", target, targetProvider)
}

func startupAuthChoice(rawTargetSetting, rawAPIKeySetting string, clientAuth bool) string {
	if rawTargetSetting == "" {
		return ""
	}

	provider, ok := providerForTarget(rawTargetSetting)
	if !ok {
		return ""
	}

	if clientAuth {
		return fmt.Sprintf("use --api-key env:%s to pin proxy auth instead", provider.envKey)
	}
	if rawAPIKeySetting != "" {
		return "use --client-auth to forward client Authorization instead"
	}

	return fmt.Sprintf("use --api-key env:%s for proxy auth, or --client-auth for client Authorization", provider.envKey)
}

func startupAuthWarning(rawTargetSetting, rawAPIKeySetting, resolvedAPIKey string, availableAuthEnvKeys []string, clientAuth bool) string {
	if clientAuth || rawAPIKeySetting != "" || rawTargetSetting == "" || resolvedAPIKey != "" || len(availableAuthEnvKeys) == 0 {
		return ""
	}

	provider, ok := providerForTarget(rawTargetSetting)
	if !ok {
		return "explicit target has no matching exported provider key; using pass-through client auth"
	}

	return fmt.Sprintf("explicit target has no exported %s; using pass-through client auth", provider.envKey)
}

func startupAuthMode(rawAPIKeySetting, resolvedAPIKey, keyProvider string, clientAuth bool) string {
	if clientAuth {
		return "forward client Authorization header (API key or OAuth token)"
	}

	if resolvedAPIKey == "" {
		return "forward client Authorization header (API key or OAuth token)"
	}

	if strings.HasPrefix(rawAPIKeySetting, "env:") {
		return fmt.Sprintf("configured on proxy from %s", strings.TrimPrefix(rawAPIKeySetting, "env:"))
	}

	if rawAPIKeySetting != "" {
		return "configured on proxy via flag or config"
	}

	if keyProvider != "" {
		return fmt.Sprintf("configured on proxy (auto-detected from %s credentials)", keyProvider)
	}

	return "configured on proxy"
}

func startupClientHint(addr, target, detectedProvider string) string {
	family := startupClientFamily(target, detectedProvider)

	switch family {
	case "anthropic":
		return fmt.Sprintf("\nTo use with Claude Code:\n  export ANTHROPIC_BASE_URL=http://%s\n\n", addr)
	default:
		return fmt.Sprintf("\nTo use with Codex or another Responses-compatible client:\n  export OPENAI_BASE_URL=http://%s\n  # for Codex, prefer a provider profile with wire_api=responses\n\n", addr)
	}
}

func startupClientFamily(target, detectedProvider string) string {
	lowerTarget := strings.ToLower(target)
	lowerProvider := strings.ToLower(detectedProvider)

	if strings.Contains(lowerProvider, "anthropic") || strings.Contains(lowerTarget, "anthropic") {
		return "anthropic"
	}

	return "responses"
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
		ClientAuth:       flagBool(cmd, "client-auth"),
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

func validateProxyAuthSelection(target, rawAPIKeySetting string, clientAuth bool, availableAuthEnvKeys []string) error {
	if !clientAuth {
		return nil
	}
	if rawAPIKeySetting != "" {
		return fmt.Errorf("--client-auth cannot be used with --api-key")
	}
	if target == "" && len(availableAuthEnvKeys) > 1 {
		return fmt.Errorf("--client-auth requires --target when multiple provider keys are exported")
	}
	return nil
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
