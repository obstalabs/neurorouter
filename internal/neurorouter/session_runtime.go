package neurorouter

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

const (
	defaultSessionKey = "default"
	sessionHeaderName = "X-Neurorouter-Session"
)

type sessionRuntime struct {
	pipeline *Pipeline
	audit    *auditLog
	dnd      *DND
	alerts   *AlertInjector
}

type sessionRegistry struct {
	mu       sync.Mutex
	cfg      ProxyConfig
	runtimes map[string]*sessionRuntime
}

func newSessionRuntime(cfg ProxyConfig) *sessionRuntime {
	dnd := NewDND()
	return &sessionRuntime{
		pipeline: NewPipeline(PipelineConfig{
			Filters:    cfg.Filters,
			Protection: cfg.Protection,
			Neurocache: cfg.Neurocache,
		}),
		audit:  newAuditLog(100),
		dnd:    dnd,
		alerts: NewAlertInjector(VerbosityDefault, dnd),
	}
}

func newSessionRegistry(cfg ProxyConfig, defaultRuntime *sessionRuntime) *sessionRegistry {
	return &sessionRegistry{
		cfg: cfg,
		runtimes: map[string]*sessionRuntime{
			defaultSessionKey: defaultRuntime,
		},
	}
}

func (s *sessionRegistry) runtime(key string) *sessionRuntime {
	sessionKey := normalizeSessionKey(key)
	if sessionKey == "" {
		sessionKey = defaultSessionKey
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if runtime, ok := s.runtimes[sessionKey]; ok {
		return runtime
	}

	runtime := newSessionRuntime(s.cfg)
	s.runtimes[sessionKey] = runtime
	return runtime
}

func managementSessionKey(r *http.Request) string {
	if key := normalizeSessionKey(r.URL.Query().Get("session")); key != "" {
		return key
	}
	if key := normalizeSessionKey(r.Header.Get(sessionHeaderName)); key != "" {
		return key
	}
	return defaultSessionKey
}

func requestSessionKey(r *http.Request, rawBody []byte) string {
	for _, header := range []string{
		sessionHeaderName,
		"session_id",
		"Session_id",
		"Session-Id",
		"X-Session-Id",
		"X-Client-Request-Id",
	} {
		if key := normalizeSessionKey(r.Header.Get(header)); key != "" {
			return key
		}
	}
	if key := sessionKeyFromMetadata(rawBody); key != "" {
		return key
	}
	return defaultSessionKey
}

func sessionKeyFromMetadata(rawBody []byte) string {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &doc); err != nil {
		return ""
	}

	rawMetadata, ok := doc["metadata"]
	if !ok || len(rawMetadata) == 0 {
		return ""
	}

	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(rawMetadata, &metadata); err != nil {
		return ""
	}

	for _, key := range []string{"session_id", "thread_id", "conversation_id", "trace_id"} {
		rawValue, ok := metadata[key]
		if !ok {
			continue
		}

		var value string
		if err := json.Unmarshal(rawValue, &value); err != nil {
			continue
		}
		if value = normalizeSessionKey(value); value != "" {
			return value
		}
	}

	return ""
}

func normalizeSessionKey(value string) string {
	return strings.TrimSpace(value)
}
