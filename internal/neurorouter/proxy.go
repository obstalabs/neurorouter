package neurorouter

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
)

// DefaultListenAddress keeps the proxy on loopback by default.
const DefaultListenAddress = "127.0.0.1:4000"

const codexTurnStateHeader = "X-Codex-Turn-State"
const codexTurnMetadataHeader = "X-Codex-Turn-Metadata"

// Target describes an upstream API endpoint.
type Target struct {
	BaseURL string // e.g. "https://api.deepseek.com"
	APIKey  string // resolved API key (not "env:...")
}

// PoolTarget is a Target with optional rate limiting and weight for
// load balancing within a target pool.
type PoolTarget struct {
	Target
	RateLimit *RateLimit // nil = no rate limit
	Weight    int        // relative weight for round-robin; 0 = 1
}

// ProxyConfig holds proxy server configuration.
type ProxyConfig struct {
	Listen                  string // defaults to 127.0.0.1:4000
	AllowPublicListen       bool   // require explicit opt-in for non-loopback binds
	ExposeManagement        bool   // expose audit/suggestions on public binds
	Capabilities            map[string]TargetCapabilities
	Targets                 map[string]Target       // model name → single target (backward compat)
	TargetPool              map[string][]PoolTarget // model name → multiple targets with load balancing
	Filters                 FilterConfig            // content filter configuration
	Protection              ProtectConfig           // secret detection configuration
	Neurocache              NeurocacheConfig        // neurocache configuration
	InputPricePerMillionUSD float64                 // estimated input token price for savings telemetry
	DryRun                  bool                    // if true, show filtered vs original without sending
	OnRequest               func(RequestEvent)      // callback for per-request logging (nil = no logging)
}

// RequestEvent is emitted after each proxied request for CLI output.
type RequestEvent struct {
	Model             string
	BytesBefore       int
	BytesAfter        int
	FiltersRun        []string
	SecretsFound      int
	SecretDiagnostics []DetectedSecret
	Blocked           bool
}

// Proxy is the local request-cleaning and forwarding proxy.
type Proxy struct {
	cfg      ProxyConfig
	srv      *http.Server
	client   *http.Client
	mu       sync.Mutex
	addr     string
	health   *HealthTracker
	limiters map[string]*rateLimiter // key: target BaseURL
	poolIdx  map[string]*uint64      // round-robin counter per model
	pipeline *Pipeline               // default session runtime (kept for test helpers)
	audit    *auditLog               // default session runtime (kept for test helpers)
	dnd      *DND                    // default session runtime (kept for test helpers)
	alerts   *AlertInjector          // default session runtime (kept for test helpers)
	sessions *sessionRegistry        // session-scoped runtime state
	wsBridge *responsesWebsocketRegistry
}

type targetSelection struct {
	Model        string
	Target       Target
	Capabilities TargetCapabilities
}

// NewProxy creates a new proxy server.
func NewProxy(cfg ProxyConfig) *Proxy {
	defaultRuntime := newSessionRuntime(cfg)

	p := &Proxy{
		cfg: cfg,
		client: &http.Client{
			Timeout: 10 * time.Minute,
		},
		health:   NewHealthTracker(0, 0),
		limiters: make(map[string]*rateLimiter),
		poolIdx:  make(map[string]*uint64),
		pipeline: defaultRuntime.pipeline,
		audit:    defaultRuntime.audit,
		dnd:      defaultRuntime.dnd,
		alerts:   defaultRuntime.alerts,
		wsBridge: newResponsesWebsocketRegistry(),
	}

	// Initialize rate limiters for pool targets.
	for model, targets := range cfg.TargetPool {
		idx := uint64(0)
		p.poolIdx[model] = &idx
		for _, pt := range targets {
			if pt.RateLimit != nil {
				p.limiters[pt.BaseURL] = newRateLimiter(*pt.RateLimit)
			}
		}
	}

	p.sessions = newSessionRegistry(cfg, defaultRuntime)

	return p
}

// Start begins listening. Returns the actual address.
func (p *Proxy) Start() (string, error) {
	listenAddr, publicBind, err := normalizeListenAddress(p.cfg.Listen)
	if err != nil {
		return "", err
	}
	if publicBind && !p.cfg.AllowPublicListen {
		return "", fmt.Errorf("public listen %s requires explicit opt-in", listenAddr)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /models", p.handleModels)
	mux.HandleFunc("GET /v1/models", p.handleModels)
	mux.HandleFunc("GET /v1/responses", p.handleResponsesWebsocket)
	mux.HandleFunc("GET /responses", p.handleResponsesWebsocket)
	mux.HandleFunc("POST /v1/responses", p.handleResponses)
	mux.HandleFunc("POST /responses", p.handleResponses)
	mux.HandleFunc("POST /v1/responses/compact", p.handleResponsesCompact)
	mux.HandleFunc("POST /responses/compact", p.handleResponsesCompact)
	mux.HandleFunc("/health", p.handleHealth)
	if !publicBind || p.cfg.ExposeManagement {
		mux.HandleFunc("/v1/suggestions", p.handleSuggestions)
		mux.HandleFunc("/v1/audit", p.handleAudit)
		mux.HandleFunc("GET /v1/dnd", p.handleDNDStatus)
		mux.HandleFunc("POST /v1/dnd", p.handleDNDToggle)
	}

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return "", fmt.Errorf("proxy listen %s: %w", listenAddr, err)
	}

	p.mu.Lock()
	p.addr = ln.Addr().String()
	p.srv = &http.Server{Handler: mux}
	p.mu.Unlock()

	go func() {
		if err := p.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("proxy server error", "error", err)
		}
	}()

	slog.Info("proxy started", "addr", p.addr, "targets", len(p.cfg.Targets))
	return p.addr, nil
}

func normalizeListenAddress(addr string) (string, bool, error) {
	if addr == "" {
		addr = DefaultListenAddress
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", false, fmt.Errorf("parse listen address %q: %w", addr, err)
	}

	if host == "" {
		return net.JoinHostPort("127.0.0.1", port), false, nil
	}

	if strings.EqualFold(host, "localhost") {
		return addr, false, nil
	}

	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return addr, false, nil
	}

	return addr, true, nil
}

// Stop gracefully shuts down the proxy.
func (p *Proxy) Stop() error {
	p.mu.Lock()
	srv := p.srv
	p.mu.Unlock()
	if srv == nil {
		if p.wsBridge != nil {
			p.wsBridge.closeAll()
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := srv.Shutdown(ctx)
	if p.wsBridge != nil {
		p.wsBridge.closeAll()
	}
	return err
}

// Addr returns the listening address after Start.
func (p *Proxy) Addr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.addr
}

func (p *Proxy) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (p *Proxy) handleModels(w http.ResponseWriter, _ *http.Request) {
	type reasoningPreset struct {
		Effort      string `json:"effort"`
		Description string `json:"description"`
	}
	type codexModel struct {
		ID                         string            `json:"id,omitempty"`
		Model                      string            `json:"model,omitempty"`
		Slug                       string            `json:"slug"`
		Name                       string            `json:"name,omitempty"`
		Display                    string            `json:"display_name"`
		Description                string            `json:"description"`
		Provider                   string            `json:"provider,omitempty"`
		WireAPI                    WireAPI           `json:"wire_api,omitempty"`
		Streaming                  bool              `json:"streaming,omitempty"`
		DefaultReasoningLevel      string            `json:"default_reasoning_level,omitempty"`
		Reasoning                  []reasoningPreset `json:"supported_reasoning_levels"`
		ShellType                  string            `json:"shell_type"`
		Visibility                 string            `json:"visibility"`
		SupportedInAPI             bool              `json:"supported_in_api"`
		Priority                   int               `json:"priority"`
		Upgrade                    any               `json:"upgrade,omitempty"`
		BaseInstructions           string            `json:"base_instructions"`
		SupportsReasoningSummary   bool              `json:"supports_reasoning_summaries"`
		SupportsVerbosity          bool              `json:"support_verbosity"`
		TruncationPolicy           map[string]any    `json:"truncation_policy"`
		SupportsParallelToolCalls  bool              `json:"supports_parallel_tool_calls"`
		ExperimentalSupportedTools []string          `json:"experimental_supported_tools"`
	}
	type openAIModel struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}

	modelNames := p.discoveredModels()
	codexModels := make([]codexModel, 0, len(modelNames))
	openAIModels := make([]openAIModel, 0, len(modelNames))

	for _, model := range modelNames {
		target, _ := p.discoveryTargetForModel(model)
		capabilities := p.capabilitiesForModel(model, target)
		codexModels = append(codexModels, codexModel{
			ID:                    model,
			Model:                 model,
			Slug:                  model,
			Name:                  model,
			Display:               model,
			Description:           "NeuroRouter-discovered upstream model",
			Provider:              capabilities.Provider,
			WireAPI:               capabilities.WireAPI,
			Streaming:             capabilities.Streaming,
			DefaultReasoningLevel: "medium",
			Reasoning: []reasoningPreset{
				{Effort: "low", Description: "Low reasoning effort"},
				{Effort: "medium", Description: "Medium reasoning effort"},
				{Effort: "high", Description: "High reasoning effort"},
				{Effort: "xhigh", Description: "Extra high reasoning effort"},
			},
			ShellType:                  "shell_command",
			Visibility:                 "list",
			SupportedInAPI:             true,
			Priority:                   0,
			Upgrade:                    nil,
			BaseInstructions:           "",
			SupportsReasoningSummary:   true,
			SupportsVerbosity:          false,
			TruncationPolicy:           map[string]any{"mode": "bytes", "limit": 10000},
			SupportsParallelToolCalls:  true,
			ExperimentalSupportedTools: []string{},
		})
		openAIModels = append(openAIModels, openAIModel{
			ID:      model,
			Object:  "model",
			Created: 0,
			OwnedBy: capabilities.Provider,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   openAIModels,
		"models": codexModels,
	})
}

func (p *Proxy) handleSuggestions(w http.ResponseWriter, r *http.Request) {
	runtime := p.runtimeForManagement(r)
	w.Header().Set("Content-Type", "application/json")
	if runtime.pipeline == nil {
		_, _ = w.Write([]byte(`{"suggestions":[]}`))
		return
	}
	suggestions := p.filteredSuggestions(runtime)
	data, _ := json.Marshal(map[string]any{"suggestions": suggestions})
	_, _ = w.Write(data)
}

func (p *Proxy) handleAudit(w http.ResponseWriter, r *http.Request) {
	runtime := p.runtimeForManagement(r)
	w.Header().Set("Content-Type", "application/json")
	entries := runtime.audit.Entries()
	if entries == nil {
		entries = []AuditEntry{}
	}
	if strings.ToLower(strings.TrimSpace(r.URL.Query().Get("secret_report"))) != "redacted" {
		entries = stripAuditSecretDiagnostics(entries)
	}
	data, _ := json.Marshal(map[string]any{
		"entries":                     entries,
		"count":                       len(entries),
		"input_price_per_million_usd": NormalizeInputPricePerMillionUSD(p.cfg.InputPricePerMillionUSD),
	})
	_, _ = w.Write(data)
}

func stripAuditSecretDiagnostics(entries []AuditEntry) []AuditEntry {
	if len(entries) == 0 {
		return entries
	}
	out := make([]AuditEntry, len(entries))
	copy(out, entries)
	for i := range out {
		out[i].SecretDiagnostics = nil
	}
	return out
}

func (p *Proxy) handleDNDStatus(w http.ResponseWriter, r *http.Request) {
	runtime := p.runtimeForManagement(r)
	w.Header().Set("Content-Type", "application/json")
	if runtime.dnd == nil {
		_ = json.NewEncoder(w).Encode(DNDSnapshot{Source: DNDSourceOff, Status: "off"})
		return
	}
	_ = json.NewEncoder(w).Encode(runtime.dnd.Snapshot())
}

func (p *Proxy) handleDNDToggle(w http.ResponseWriter, r *http.Request) {
	runtime := p.runtimeForManagement(r)
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid dnd body: "+err.Error())
		return
	}

	if runtime.dnd != nil {
		runtime.dnd.SetManual(req.Enabled)
	}

	w.Header().Set("Content-Type", "application/json")
	if runtime.dnd == nil {
		_ = json.NewEncoder(w).Encode(DNDSnapshot{Source: DNDSourceOff, Status: "off"})
		return
	}
	_ = json.NewEncoder(w).Encode(runtime.dnd.Snapshot())
}

func (p *Proxy) filteredSuggestions(runtime *sessionRuntime) []Suggestion {
	if runtime == nil || runtime.pipeline == nil {
		return []Suggestion{}
	}

	suggestions := runtime.pipeline.Suggestions()
	if len(suggestions) == 0 {
		return []Suggestion{}
	}
	if runtime.dnd == nil {
		return append([]Suggestion(nil), suggestions...)
	}

	filtered := make([]Suggestion, 0, len(suggestions))
	for _, suggestion := range suggestions {
		if !runtime.dnd.ShouldSuppress(suggestion) {
			filtered = append(filtered, suggestion)
		}
	}
	if len(filtered) == 0 {
		return []Suggestion{}
	}
	return filtered
}

func (p *Proxy) handleResponses(w http.ResponseWriter, r *http.Request) {
	p.handleResponsesForUpstream(w, r, "/v1/responses", false)
}

func (p *Proxy) handleResponsesCompact(w http.ResponseWriter, r *http.Request) {
	p.handleResponsesForUpstream(w, r, "/v1/responses/compact", true)
}

func (p *Proxy) handleResponsesForUpstream(w http.ResponseWriter, r *http.Request, nativeUpstreamPath string, requireNativeResponses bool) {
	rawBody, err := readDecodedRequestBody(r)
	if err != nil {
		var decodeErr *requestBodyError
		if errors.As(err, &decodeErr) {
			writeError(w, decodeErr.StatusCode, decodeErr.Message)
			return
		}
		writeError(w, http.StatusBadRequest, "read request body: "+err.Error())
		return
	}

	runtime := p.runtimeForSession(requestSessionKey(r, rawBody))

	var req ResponsesRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if runtime.dnd != nil {
		runtime.dnd.RecordRequest()
	}

	requirements := DeriveRequirements(&req)

	selection, err := p.resolveTarget(req.Model, requirements)
	if err != nil {
		var capabilityErr *CapabilityError
		if errors.As(err, &capabilityErr) {
			writeError(w, http.StatusBadRequest, capabilityErr.Error())
			return
		}
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	}

	requestMsgs, err := ExtractRequestMessages(&req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "extract request messages: "+err.Error())
		return
	}
	filteredMsgs := append([]ChatMessage(nil), requestMsgs...)
	originalMsgs := append([]ChatMessage(nil), requestMsgs...)
	useResponsesWire := selection.Capabilities.WireAPI == WireAPIResponses

	// Pipeline: protect (secrets) → purify (noise).
	var pipeResult *PipelineResult
	var responsesRewrite *ResponsesRewriteResult
	var alerts []Alert
	if runtime.pipeline != nil {
		adapter := SelectFilterAdapter(selection.Capabilities, filteredMsgs)
		var pipeErr error
		filteredMsgs, pipeResult, pipeErr = runtime.pipeline.Process(filteredMsgs, adapter)
		if pipeErr != nil {
			// Record blocked request in audit.
			if runtime.audit != nil && pipeResult != nil {
				runtime.audit.Record(AuditEntry{
					Timestamp:         timeNow(),
					Model:             req.Model,
					BytesBefore:       pipeResult.BytesBefore,
					BytesAfter:        0,
					BytesRemoved:      pipeResult.BytesBefore,
					SecretsFound:      pipeResult.SecretsFound,
					SecretDiagnostics: cloneDetectedSecrets(pipeResult.SecretDiagnostics),
					SecretPolicy:      string(pipeResult.SecretPolicy),
					Blocked:           true,
				})
			}
			if runtime.dnd != nil {
				runtime.dnd.RecordError()
			}
			writeError(w, http.StatusForbidden, "request blocked: "+pipeErr.Error())
			return
		}
		if pipeResult.SecretsFound > 0 && pipeResult.SecretPolicy == PolicyWarn {
			w.Header().Set("X-Neurorouter-Secrets-Detected", fmt.Sprintf("%d", pipeResult.SecretsFound))
		}
		if useResponsesWire {
			responsesRewrite, err = RewriteResponsesRequestWithConfig(rawBody, originalMsgs, filteredMsgs, runtime.pipeline.filters)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "rewrite responses request: "+err.Error())
				return
			}
			pipeResult.BytesBefore = responsesRewrite.BytesBefore
			pipeResult.BytesAfter = responsesRewrite.BytesAfter
			pipeResult.FiltersRun = mergeFilterNames(pipeResult.FiltersRun, responsesRewrite.FiltersRun)
		}
		slog.Debug("pipeline",
			"adapter", adapter.Name(),
			"secrets", pipeResult.SecretsFound,
			"bytes_before", pipeResult.BytesBefore,
			"bytes_after", pipeResult.BytesAfter,
			"filters", pipeResult.FiltersRun)
		suggestions := p.filteredSuggestions(runtime)
		if runtime.alerts != nil {
			alerts = runtime.alerts.Generate(pipeResult, suggestions)
		}
		if len(suggestions) > 0 {
			w.Header().Set("X-Neurorouter-Suggestions", fmt.Sprintf("%d", len(suggestions)))
		}

		// Record transformation in audit log.
		if runtime.audit != nil {
			runtime.audit.Record(AuditEntry{
				Timestamp:         timeNow(),
				Model:             req.Model,
				BytesBefore:       pipeResult.BytesBefore,
				BytesAfter:        pipeResult.BytesAfter,
				BytesRemoved:      pipeResult.BytesBefore - pipeResult.BytesAfter,
				FiltersRun:        pipeResult.FiltersRun,
				SecretsFound:      pipeResult.SecretsFound,
				SecretDiagnostics: cloneDetectedSecrets(pipeResult.SecretDiagnostics),
				SecretPolicy:      string(pipeResult.SecretPolicy),
			})
		}

		// Emit per-request event for CLI output.
		if p.cfg.OnRequest != nil {
			p.cfg.OnRequest(RequestEvent{
				Model:             req.Model,
				BytesBefore:       pipeResult.BytesBefore,
				BytesAfter:        pipeResult.BytesAfter,
				FiltersRun:        pipeResult.FiltersRun,
				SecretsFound:      pipeResult.SecretsFound,
				SecretDiagnostics: cloneDetectedSecrets(pipeResult.SecretDiagnostics),
			})
		}
	}

	if useResponsesWire && responsesRewrite == nil {
		responsesRewrite, err = RewriteResponsesRequestWithConfig(rawBody, originalMsgs, filteredMsgs, FilterConfig{})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "rewrite responses request: "+err.Error())
			return
		}
	}

	// Dry-run mode: return diff without forwarding upstream.
	if p.cfg.DryRun {
		result := DryRunResult{
			Original: originalMsgs,
			Filtered: filteredMsgs,
		}
		if pipeResult != nil {
			result.BytesBefore = pipeResult.BytesBefore
			result.BytesAfter = pipeResult.BytesAfter
			result.BytesRemoved = pipeResult.BytesBefore - pipeResult.BytesAfter
			result.FiltersRun = pipeResult.FiltersRun
			result.SecretsFound = pipeResult.SecretsFound
		}
		w.Header().Set("Content-Type", "application/json")
		data, _ := json.Marshal(result)
		_, _ = w.Write(data)
		return
	}

	body := rawBody
	upstreamPath := nativeUpstreamPath
	if useResponsesWire {
		if responsesRewrite != nil {
			body = responsesRewrite.Body
		}
	} else {
		if requireNativeResponses {
			writeError(w, http.StatusBadRequest, "responses compact requires native Responses upstream support")
			return
		}
		chatReq, err := BuildChatRequest(&req, filteredMsgs)
		if err != nil {
			writeError(w, http.StatusBadRequest, "translate request: "+err.Error())
			return
		}
		body, err = json.Marshal(chatReq)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "marshal request: "+err.Error())
			return
		}
		upstreamPath = "/v1/chat/completions"
	}

	upstreamURL := strings.TrimRight(selection.Target.BaseURL, "/") + upstreamPath
	upReq, err := http.NewRequestWithContext(r.Context(), "POST", upstreamURL, bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create upstream request: "+err.Error())
		return
	}
	upReq.Header.Set("Content-Type", "application/json")
	forwardOpenAICompatibilityHeaders(upReq.Header, r.Header)
	forwardCodexTurnState(upReq.Header, r.Header)

	if selection.Target.APIKey != "" {
		upReq.Header.Set("Authorization", "Bearer "+selection.Target.APIKey)
	} else if auth := r.Header.Get("Authorization"); auth != "" {
		upReq.Header.Set("Authorization", auth)
		forwardClientAuthHeaders(upReq.Header, r.Header)
	}

	slog.Debug("proxy forwarding", "model", req.Model, "provider", selection.Capabilities.Provider, "upstream", upstreamURL, "stream", req.Stream)

	upResp, err := p.client.Do(upReq)
	if err != nil {
		p.health.RecordFailure(selection.Target.BaseURL)
		if runtime.dnd != nil {
			runtime.dnd.RecordError()
		}
		writeError(w, http.StatusBadGateway, "upstream error: "+err.Error())
		return
	}
	defer func() { _ = upResp.Body.Close() }()

	if upResp.StatusCode >= 500 {
		p.health.RecordFailure(selection.Target.BaseURL)
	} else {
		p.health.RecordSuccess(selection.Target.BaseURL)
	}

	// pass through upstream errors
	if upResp.StatusCode >= 400 {
		propagateCodexTurnState(w.Header(), upResp.Header)
		if runtime.dnd != nil {
			runtime.dnd.RecordError()
		}
		body, _ := io.ReadAll(upResp.Body)
		if rewritten, ok := oauthCompatibilityError(selection.Target.APIKey, r.Header, upResp.StatusCode, body); ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"type":    "invalid_request_error",
					"code":    "client_auth_unsupported",
					"message": rewritten,
				},
			})
			return
		}

		if contentType := upResp.Header.Get("Content-Type"); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(upResp.StatusCode)
		_, _ = w.Write(body)
		return
	}

	if useResponsesWire {
		propagateCodexTurnState(w.Header(), upResp.Header)
		if req.Stream {
			p.handlePassthroughStreamingResponse(w, upResp, alerts)
		} else {
			p.handlePassthroughResponse(w, upResp, alerts)
		}
		return
	}

	if req.Stream {
		p.handleStreamingResponse(w, upResp, alerts)
	} else {
		p.handleNonStreamingResponse(w, upResp, alerts)
	}
}

func forwardCodexTurnState(dst, src http.Header) {
	if turnState := src.Get(codexTurnStateHeader); turnState != "" {
		dst.Set(codexTurnStateHeader, turnState)
	}
}

func propagateCodexTurnState(dst, src http.Header) {
	if turnState := src.Get(codexTurnStateHeader); turnState != "" {
		dst.Set(codexTurnStateHeader, turnState)
	}
}

func (p *Proxy) runtimeForManagement(r *http.Request) *sessionRuntime {
	return p.runtimeForSession(managementSessionKey(r))
}

func (p *Proxy) runtimeForSession(sessionKey string) *sessionRuntime {
	if p.sessions == nil {
		return &sessionRuntime{
			pipeline: p.pipeline,
			audit:    p.audit,
			dnd:      p.dnd,
			alerts:   p.alerts,
		}
	}
	return p.sessions.runtime(sessionKey)
}

func (p *Proxy) handleNonStreamingResponse(w http.ResponseWriter, upResp *http.Response, alerts []Alert) {
	var chatResp ChatCompletionResponse
	if err := json.NewDecoder(upResp.Body).Decode(&chatResp); err != nil {
		writeError(w, http.StatusBadGateway, "decode upstream response: "+err.Error())
		return
	}

	resp := TranslateResponse(&chatResp)
	prependAlertsToResponsesAPIResponse(resp, alerts)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (p *Proxy) handleStreamingResponse(w http.ResponseWriter, upResp *http.Response, alerts []Alert) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	translator := NewStreamTranslator()
	alertState := newResponsesAlertStreamState(alerts)
	scanner := bufio.NewScanner(upResp.Body)
	// 256KB buffer for large chunks
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}

		var chunk ChatChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			slog.Debug("proxy: skip unparseable chunk", "error", err)
			continue
		}

		events, done := translator.TranslateChunk(&chunk)
		for _, ev := range events {
			if alertState != nil {
				rewritten, err := rewriteResponsesEventPayload([]byte(ev.Data), ev.Event, alertState)
				if err == nil {
					ev.Data = string(rewritten)
				}
			}
			_, _ = fmt.Fprint(w, ev.Format())
		}
		flusher.Flush()

		if done {
			break
		}
	}
}

func (p *Proxy) handlePassthroughResponse(w http.ResponseWriter, upResp *http.Response, alerts []Alert) {
	if contentType := upResp.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(upResp.StatusCode)

	if len(alerts) == 0 {
		_, _ = io.Copy(w, upResp.Body)
		return
	}

	body, err := io.ReadAll(upResp.Body)
	if err != nil {
		slog.Debug("proxy: read native response for alert injection", "error", err)
		return
	}
	rewritten, err := injectAlertsIntoResponsesBody(body, alerts)
	if err != nil {
		slog.Debug("proxy: inject alerts into native response", "error", err)
		_, _ = w.Write(body)
		return
	}
	_, _ = w.Write(rewritten)
}

func (p *Proxy) handlePassthroughStreamingResponse(w http.ResponseWriter, upResp *http.Response, alerts []Alert) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	if contentType := upResp.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "text/event-stream")
	}
	if cacheControl := upResp.Header.Get("Cache-Control"); cacheControl != "" {
		w.Header().Set("Cache-Control", cacheControl)
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if connection := upResp.Header.Get("Connection"); connection != "" {
		w.Header().Set("Connection", connection)
	} else {
		w.Header().Set("Connection", "keep-alive")
	}

	w.WriteHeader(upResp.StatusCode)

	if len(alerts) == 0 {
		buf := make([]byte, 32*1024)
		for {
			n, err := upResp.Body.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
				flusher.Flush()
			}
			if err != nil {
				if err != io.EOF {
					slog.Debug("proxy: native stream ended with error", "error", err)
				}
				break
			}
		}
		return
	}

	alertState := newResponsesAlertStreamState(alerts)
	scanner := bufio.NewScanner(upResp.Body)
	scanner.Buffer(make([]byte, 0, 256*1024), 2*1024*1024)

	currentEvent := ""
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			currentEvent = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			payload := strings.TrimPrefix(line, "data: ")
			if payload != "" && payload != "[DONE]" {
				rewritten, err := rewriteResponsesEventPayload([]byte(payload), currentEvent, alertState)
				if err == nil {
					line = "data: " + string(rewritten)
				}
			}
		case line == "":
			currentEvent = ""
		}

		_, _ = fmt.Fprint(w, line+"\n")
		if line == "" {
			flusher.Flush()
		}
	}
	if err := scanner.Err(); err != nil {
		slog.Debug("proxy: native stream ended with error", "error", err)
	}
}

func (p *Proxy) resolveTarget(model string, req RequestRequirements) (targetSelection, error) {
	var (
		allReasons        []string
		hadConfigured     bool
		hadCompatiblePath bool
	)

	// Try pool targets first (multi-target with load balancing).
	if sel, compatible, reasons, ok := p.resolveFromPool(model, req); ok {
		return sel, nil
	} else if compatible {
		hadConfigured = true
		hadCompatiblePath = true
	} else if len(reasons) > 0 {
		hadConfigured = true
		allReasons = append(allReasons, reasons...)
	}
	if sel, compatible, reasons, ok := p.resolveFromPool("default", req); ok {
		return sel, nil
	} else if compatible {
		hadConfigured = true
		hadCompatiblePath = true
	} else if len(reasons) > 0 {
		hadConfigured = true
		allReasons = append(allReasons, reasons...)
	}

	// Fall back to single targets (backward compat).
	if sel, compatible, reasons, ok := p.resolveSingleTarget(model, req); ok {
		return sel, nil
	} else if compatible {
		hadConfigured = true
		hadCompatiblePath = true
	} else if len(reasons) > 0 {
		hadConfigured = true
		allReasons = append(allReasons, reasons...)
	}
	if sel, compatible, reasons, ok := p.resolveSingleTarget("default", req); ok {
		return sel, nil
	} else if compatible {
		hadConfigured = true
		hadCompatiblePath = true
	} else if len(reasons) > 0 {
		hadConfigured = true
		allReasons = append(allReasons, reasons...)
	}

	if hadConfigured && !hadCompatiblePath {
		return targetSelection{}, &CapabilityError{
			Model:   model,
			Reasons: uniqueStrings(allReasons),
		}
	}

	return targetSelection{}, fmt.Errorf("no available target for model %q (all exhausted or unhealthy)", model)
}

type requestBodyError struct {
	StatusCode int
	Message    string
}

func (e *requestBodyError) Error() string {
	return e.Message
}

func readDecodedRequestBody(r *http.Request) ([]byte, error) {
	encoding := normalizedContentEncoding(r.Header.Get("Content-Encoding"))

	switch encoding {
	case "", "identity":
		defer func() { _ = r.Body.Close() }()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		return body, nil
	case "gzip":
		defer func() { _ = r.Body.Close() }()
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, &requestBodyError{
				StatusCode: http.StatusBadRequest,
				Message:    "decode gzip request body: " + err.Error(),
			}
		}
		defer func() { _ = reader.Close() }()
		body, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("read gzip request body: %w", err)
		}
		return body, nil
	case "zstd":
		defer func() { _ = r.Body.Close() }()
		reader, err := zstd.NewReader(r.Body)
		if err != nil {
			return nil, &requestBodyError{
				StatusCode: http.StatusBadRequest,
				Message:    "decode zstd request body: " + err.Error(),
			}
		}
		defer reader.Close()
		body, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("read zstd request body: %w", err)
		}
		return body, nil
	default:
		return nil, &requestBodyError{
			StatusCode: http.StatusUnsupportedMediaType,
			Message:    "unsupported content encoding: " + encoding,
		}
	}
}

func normalizedContentEncoding(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(strings.Split(value, ",")[0]))
}

func oauthCompatibilityError(proxyAPIKey string, headers http.Header, statusCode int, body []byte) (string, bool) {
	if proxyAPIKey != "" || statusCode != http.StatusUnauthorized {
		return "", false
	}
	if headers.Get("Chatgpt-Account-Id") == "" {
		return "", false
	}
	if !bytes.Contains(body, []byte("api.responses.write")) {
		return "", false
	}

	return "Codex account-auth pass-through was rejected by the upstream for missing api.responses.write. NeuroRouter community edition supports Codex through an OpenAI API key today; set OPENAI_API_KEY for the client or run the proxy with --api-key env:OPENAI_API_KEY.", true
}

func forwardClientAuthHeaders(dst, src http.Header) {
	for _, header := range []string{
		"Chatgpt-Account-Id",
		"OpenAI-Organization",
		"OpenAI-Project",
	} {
		for _, value := range src.Values(header) {
			dst.Add(header, value)
		}
	}
}

func forwardOpenAICompatibilityHeaders(dst, src http.Header) {
	for _, header := range []string{
		"session_id",
		"X-Client-Request-Id",
		"X-OpenAI-Subagent",
		"OpenAI-Beta",
		"X-Codex-Beta-Features",
		codexTurnStateHeader,
		codexTurnMetadataHeader,
		"Traceparent",
		"Tracestate",
		"Originator",
		"Version",
		"X-Oai-Web-Search-Eligible",
	} {
		for _, value := range src.Values(header) {
			dst.Add(header, value)
		}
	}
}

func (p *Proxy) discoveredModels() []string {
	modelSet := make(map[string]struct{})

	for model := range p.cfg.Targets {
		modelSet[model] = struct{}{}
	}
	for model := range p.cfg.TargetPool {
		modelSet[model] = struct{}{}
	}
	for model := range p.cfg.Capabilities {
		modelSet[model] = struct{}{}
	}

	models := make([]string, 0, len(modelSet))
	for model := range modelSet {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}

func (p *Proxy) discoveryTargetForModel(model string) (Target, bool) {
	if target, ok := p.cfg.Targets[model]; ok {
		return target, true
	}
	if pool := p.cfg.TargetPool[model]; len(pool) > 0 {
		return pool[0].Target, true
	}
	return Target{}, false
}

// resolveFromPool selects a target from the pool using round-robin,
// skipping unhealthy or rate-limited targets.
func (p *Proxy) resolveFromPool(model string, req RequestRequirements) (targetSelection, bool, []string, bool) {
	pool, ok := p.cfg.TargetPool[model]
	if !ok || len(pool) == 0 {
		return targetSelection{}, false, nil, false
	}

	capability := p.capabilitiesForModel(model, pool[0].Target)
	result := Compatible(req, capability)
	if !result.OK {
		return targetSelection{}, false, result.Reasons, false
	}

	idx := p.poolIdx[model]
	n := len(pool)

	// Try each target in round-robin order.
	for i := 0; i < n; i++ {
		p.mu.Lock()
		pos := int(*idx % uint64(n))
		*idx++
		p.mu.Unlock()

		pt := pool[pos]

		// Skip unhealthy targets.
		if !p.health.IsHealthy(pt.BaseURL) {
			continue
		}

		// Skip rate-limited targets.
		if rl, exists := p.limiters[pt.BaseURL]; exists {
			if !rl.Allow() {
				continue
			}
		}

		return targetSelection{
			Model:        model,
			Target:       pt.Target,
			Capabilities: capability,
		}, true, nil, true
	}

	return targetSelection{}, true, nil, false
}

func (p *Proxy) resolveSingleTarget(model string, req RequestRequirements) (targetSelection, bool, []string, bool) {
	target, ok := p.cfg.Targets[model]
	if !ok {
		return targetSelection{}, false, nil, false
	}

	capability := p.capabilitiesForModel(model, target)
	result := Compatible(req, capability)
	if !result.OK {
		return targetSelection{}, false, result.Reasons, false
	}

	return targetSelection{
		Model:        model,
		Target:       target,
		Capabilities: capability,
	}, true, nil, true
}

func (p *Proxy) capabilitiesForModel(model string, target Target) TargetCapabilities {
	if p.cfg.Capabilities != nil {
		if cap, ok := p.cfg.Capabilities[model]; ok {
			if cap.Model == "" {
				cap.Model = model
			}
			if cap.Provider == "" {
				cap.Provider = detectProviderName(target.BaseURL)
			}
			if cap.WireAPI == "" {
				cap.WireAPI = WireAPIChatCompletions
			}
			return cap
		}
	}
	return DefaultTargetCapabilities(model, target)
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	resp := map[string]any{"error": map[string]any{"message": msg, "code": code}}
	_ = json.NewEncoder(w).Encode(resp)
}
