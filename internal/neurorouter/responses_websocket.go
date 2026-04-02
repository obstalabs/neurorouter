package neurorouter

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

const responsesWebsocketReadLimit = 8 << 20
const openAIResponsesWebsocketBetaHeaderValue = "responses_websockets=2026-02-06"

var responsesWebsocketConnectionSeq uint64

var responsesWebsocketUpgrader = websocket.Upgrader{
	ReadBufferSize:  32 * 1024,
	WriteBufferSize: 32 * 1024,
	CheckOrigin: func(_ *http.Request) bool {
		return true
	},
}

type responsesWebsocketBridgeState struct {
	mu              sync.Mutex
	sessionKey      string
	turnState       string
	upstream        *websocket.Conn
	upstreamBaseURL string
}

func (s *responsesWebsocketBridgeState) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetUpstreamLocked()
}

func (s *responsesWebsocketBridgeState) resetUpstreamLocked() {
	if s.upstream != nil {
		_ = s.upstream.Close()
	}
	s.upstream = nil
	s.upstreamBaseURL = ""
}

type responsesWebsocketRegistry struct {
	mu     sync.Mutex
	states map[string]*responsesWebsocketBridgeState
}

func newResponsesWebsocketRegistry() *responsesWebsocketRegistry {
	return &responsesWebsocketRegistry{
		states: make(map[string]*responsesWebsocketBridgeState),
	}
}

func (r *responsesWebsocketRegistry) state(key, initialTurnState string) *responsesWebsocketBridgeState {
	sessionKey := normalizeSessionKey(key)
	if sessionKey == "" {
		sessionKey = defaultSessionKey
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	state, ok := r.states[sessionKey]
	if !ok {
		state = &responsesWebsocketBridgeState{sessionKey: sessionKey}
		r.states[sessionKey] = state
	}
	state.mu.Lock()
	if state.turnState == "" && initialTurnState != "" {
		state.turnState = initialTurnState
	}
	state.mu.Unlock()
	return state
}

func (r *responsesWebsocketRegistry) release(key string) {
	sessionKey := normalizeSessionKey(key)
	if sessionKey == "" {
		return
	}

	r.mu.Lock()
	state, ok := r.states[sessionKey]
	if ok {
		delete(r.states, sessionKey)
	}
	r.mu.Unlock()

	if ok {
		state.close()
	}
}

func (r *responsesWebsocketRegistry) closeAll() {
	r.mu.Lock()
	states := make([]*responsesWebsocketBridgeState, 0, len(r.states))
	for key, state := range r.states {
		delete(r.states, key)
		states = append(states, state)
	}
	r.mu.Unlock()

	for _, state := range states {
		state.close()
	}
}

func nextResponsesWebsocketConnectionKey() string {
	return fmt.Sprintf("ws-%d", atomic.AddUint64(&responsesWebsocketConnectionSeq, 1))
}

func responsesWebsocketSessionKey(r *http.Request, rawBody []byte, connectionKey string) string {
	sessionKey := requestSessionKey(r, rawBody)
	if sessionKey == "" || sessionKey == defaultSessionKey {
		return connectionKey
	}
	return sessionKey
}

func (p *Proxy) handleResponsesWebsocket(w http.ResponseWriter, r *http.Request) {
	conn, err := responsesWebsocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Debug("proxy websocket upgrade failed", "error", err)
		return
	}
	connectionKey := nextResponsesWebsocketConnectionKey()
	defer p.wsBridge.release(connectionKey)
	defer func() { _ = conn.Close() }()

	conn.SetReadLimit(responsesWebsocketReadLimit)

	for {
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}
		if err := p.handleResponsesWebsocketMessage(conn, r, connectionKey, payload); err != nil {
			slog.Debug("proxy websocket request failed", "error", err)
			return
		}
	}
}

func (p *Proxy) handleResponsesWebsocketMessage(conn *websocket.Conn, r *http.Request, connectionKey string, payload []byte) error {
	rawBody, extraHeaders, err := sanitizeResponsesWebsocketRequest(payload)
	if err != nil {
		return writeResponsesWebsocketError(conn, http.StatusBadRequest, "invalid_request_error", "invalid websocket request: "+err.Error())
	}

	if p.cfg.DryRun {
		return writeResponsesWebsocketError(conn, http.StatusBadRequest, "invalid_request_error", "websocket mode is not supported in dry-run")
	}

	sessionKey := responsesWebsocketSessionKey(r, rawBody, connectionKey)
	state := p.wsBridge.state(sessionKey, r.Header.Get(codexTurnStateHeader))
	runtime := p.runtimeForSession(sessionKey)

	var req ResponsesRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		return writeResponsesWebsocketError(conn, http.StatusBadRequest, "invalid_request_error", "invalid request body: "+err.Error())
	}
	if runtime.dnd != nil {
		runtime.dnd.RecordRequest()
	}

	requirements := DeriveRequirements(&req)
	selection, err := p.resolveTarget(req.Model, requirements)
	if err != nil {
		var capabilityErr *CapabilityError
		if errors.As(err, &capabilityErr) {
			return writeResponsesWebsocketError(conn, http.StatusBadRequest, "invalid_request_error", capabilityErr.Error())
		}
		return writeResponsesWebsocketError(conn, http.StatusTooManyRequests, "rate_limit_error", err.Error())
	}

	requestMsgs, err := ExtractRequestMessages(&req)
	if err != nil {
		return writeResponsesWebsocketError(conn, http.StatusBadRequest, "invalid_request_error", "extract request messages: "+err.Error())
	}
	filteredMsgs := append([]ChatMessage(nil), requestMsgs...)
	originalMsgs := append([]ChatMessage(nil), requestMsgs...)
	useResponsesWire := selection.Capabilities.WireAPI == WireAPIResponses

	var pipeResult *PipelineResult
	var rawRewrite *ResponsesRewriteResult
	var websocketRewrite *ResponsesRewriteResult
	var alerts []Alert
	if runtime.pipeline != nil {
		adapter := SelectFilterAdapter(selection.Capabilities, filteredMsgs)
		var pipeErr error
		filteredMsgs, pipeResult, pipeErr = runtime.pipeline.Process(filteredMsgs, adapter)
		if pipeErr != nil {
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
			return writeResponsesWebsocketError(conn, http.StatusForbidden, "invalid_request_error", "request blocked: "+pipeErr.Error())
		}
		if useResponsesWire {
			rawRewrite, err = RewriteResponsesRequestWithConfig(rawBody, originalMsgs, filteredMsgs, runtime.pipeline.filters)
			if err != nil {
				return writeResponsesWebsocketError(conn, http.StatusInternalServerError, "server_error", "rewrite responses request: "+err.Error())
			}
			websocketRewrite, err = RewriteResponsesRequestWithConfig(payload, originalMsgs, filteredMsgs, runtime.pipeline.filters)
			if err != nil {
				return writeResponsesWebsocketError(conn, http.StatusInternalServerError, "server_error", "rewrite websocket request: "+err.Error())
			}
			pipeResult.BytesBefore = rawRewrite.BytesBefore
			pipeResult.BytesAfter = rawRewrite.BytesAfter
			pipeResult.FiltersRun = mergeFilterNames(pipeResult.FiltersRun, rawRewrite.FiltersRun)
		}
		if runtime.alerts != nil {
			alerts = runtime.alerts.Generate(pipeResult, p.filteredSuggestions(runtime))
		}

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

	if !useResponsesWire {
		return writeResponsesWebsocketError(conn, http.StatusBadRequest, "invalid_request_error", "websocket mode requires a Responses-compatible upstream")
	}

	if rawRewrite == nil {
		rawRewrite, err = RewriteResponsesRequestWithConfig(rawBody, originalMsgs, filteredMsgs, FilterConfig{})
		if err != nil {
			return writeResponsesWebsocketError(conn, http.StatusInternalServerError, "server_error", "rewrite responses request: "+err.Error())
		}
		websocketRewrite, err = RewriteResponsesRequestWithConfig(payload, originalMsgs, filteredMsgs, FilterConfig{})
		if err != nil {
			return writeResponsesWebsocketError(conn, http.StatusInternalServerError, "server_error", "rewrite websocket request: "+err.Error())
		}
	}

	websocketBody := payload
	body := rawBody
	if rawRewrite != nil {
		body = rawRewrite.Body
	}
	if websocketRewrite != nil {
		websocketBody = websocketRewrite.Body
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	if err := p.relayResponsesWebsocketUpstream(conn, r, state, selection, websocketBody, alerts); err == nil {
		return nil
	} else if !errors.Is(err, errResponsesWebsocketFallback) {
		return writeResponsesWebsocketError(conn, http.StatusBadGateway, "server_error", "upstream websocket error: "+err.Error())
	}

	upstreamURL := strings.TrimRight(selection.Target.BaseURL, "/") + "/v1/responses"
	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstreamURL, bytes.NewReader(body))
	if err != nil {
		return writeResponsesWebsocketError(conn, http.StatusInternalServerError, "server_error", "create upstream request: "+err.Error())
	}
	upReq.Header.Set("Content-Type", "application/json")
	upReq.Header.Set("Accept", "text/event-stream")
	forwardOpenAICompatibilityHeaders(upReq.Header, r.Header)
	for header, values := range extraHeaders {
		for _, value := range values {
			upReq.Header.Add(header, value)
		}
	}
	if state != nil && state.turnState != "" {
		upReq.Header.Set(codexTurnStateHeader, state.turnState)
	}

	if selection.Target.APIKey != "" {
		upReq.Header.Set("Authorization", "Bearer "+selection.Target.APIKey)
	} else if auth := r.Header.Get("Authorization"); auth != "" {
		upReq.Header.Set("Authorization", auth)
		forwardClientAuthHeaders(upReq.Header, r.Header)
	}

	upResp, err := p.client.Do(upReq)
	if err != nil {
		p.health.RecordFailure(selection.Target.BaseURL)
		if runtime.dnd != nil {
			runtime.dnd.RecordError()
		}
		return writeResponsesWebsocketError(conn, http.StatusBadGateway, "server_error", "upstream error: "+err.Error())
	}
	defer func() { _ = upResp.Body.Close() }()
	if state != nil {
		if turnState := upResp.Header.Get(codexTurnStateHeader); turnState != "" {
			state.turnState = turnState
		}
	}

	if upResp.StatusCode >= 500 {
		p.health.RecordFailure(selection.Target.BaseURL)
	} else {
		p.health.RecordSuccess(selection.Target.BaseURL)
	}

	if upResp.StatusCode >= 400 {
		if runtime.dnd != nil {
			runtime.dnd.RecordError()
		}
		errorBody, _ := io.ReadAll(upResp.Body)
		if rewritten, ok := oauthCompatibilityError(selection.Target.APIKey, r.Header, upResp.StatusCode, errorBody); ok {
			return writeResponsesWebsocketError(conn, http.StatusUnauthorized, "client_auth_unsupported", rewritten)
		}
		return writeResponsesWebsocketError(conn, upResp.StatusCode, "upstream_error", decodeUpstreamErrorMessage(upResp.StatusCode, errorBody))
	}

	return relayResponsesSSEToWebsocket(conn, upResp.Body, alerts)
}

func sanitizeResponsesWebsocketRequest(payload []byte) ([]byte, http.Header, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(payload, &doc); err != nil {
		return nil, nil, err
	}

	rawType, ok := doc["type"]
	if !ok {
		return nil, nil, fmt.Errorf("missing type")
	}

	var reqType string
	if err := json.Unmarshal(rawType, &reqType); err != nil {
		return nil, nil, fmt.Errorf("decode type: %w", err)
	}
	if reqType != "response.create" {
		return nil, nil, fmt.Errorf("unsupported websocket request type %q", reqType)
	}

	extraHeaders, err := extractResponsesWebsocketCompatibilityHeaders(doc["client_metadata"])
	if err != nil {
		return nil, nil, err
	}

	// Strip only websocket envelope metadata that must become headers. Continuity
	// fields such as previous_response_id and the actual input history must remain
	// in the upstream JSON body or Codex turn state can break across requests.
	delete(doc, "type")
	delete(doc, "client_metadata")
	delete(doc, "generate")

	body, err := json.Marshal(doc)
	if err != nil {
		return nil, nil, err
	}
	return body, extraHeaders, nil
}

func extractResponsesWebsocketCompatibilityHeaders(raw json.RawMessage) (http.Header, error) {
	headers := make(http.Header)
	if len(raw) == 0 {
		return headers, nil
	}

	var metadata map[string]string
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return nil, fmt.Errorf("decode client_metadata: %w", err)
	}

	for key, value := range metadata {
		if value == "" {
			continue
		}
		switch strings.ToLower(key) {
		case strings.ToLower(codexTurnMetadataHeader):
			headers.Add(codexTurnMetadataHeader, value)
		case "ws_request_header_traceparent":
			headers.Add("Traceparent", value)
		case "ws_request_header_tracestate":
			headers.Add("Tracestate", value)
		}
	}

	return headers, nil
}

var errResponsesWebsocketFallback = errors.New("fallback to http responses bridge")

func (p *Proxy) relayResponsesWebsocketUpstream(clientConn *websocket.Conn, r *http.Request, state *responsesWebsocketBridgeState, selection targetSelection, payload []byte, alerts []Alert) error {
	upstreamConn, err := p.ensureResponsesWebsocketUpstream(r, state, selection)
	if err != nil {
		return errResponsesWebsocketFallback
	}

	if err := upstreamConn.WriteMessage(websocket.TextMessage, payload); err != nil {
		state.resetUpstreamLocked()
		return err
	}

	alertState := newResponsesAlertStreamState(alerts)
	for {
		msgType, message, err := upstreamConn.ReadMessage()
		if err != nil {
			state.resetUpstreamLocked()
			return err
		}
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}
		eventType := responsesWebsocketEventType(message)
		if rewritten, err := rewriteResponsesEventPayload(message, eventType, alertState); err == nil {
			message = rewritten
		}
		if err := clientConn.WriteMessage(msgType, message); err != nil {
			return err
		}
		if eventType == "response.completed" || eventType == "error" {
			return nil
		}
	}
}

func (p *Proxy) ensureResponsesWebsocketUpstream(r *http.Request, state *responsesWebsocketBridgeState, selection targetSelection) (*websocket.Conn, error) {
	if state.upstream != nil && state.upstreamBaseURL == selection.Target.BaseURL {
		return state.upstream, nil
	}
	if state.upstream != nil {
		state.resetUpstreamLocked()
	}

	upstreamURL, err := responsesWebsocketURL(selection.Target.BaseURL)
	if err != nil {
		return nil, err
	}

	headers := make(http.Header)
	forwardOpenAICompatibilityHeaders(headers, r.Header)
	if state.sessionKey != "" {
		if headers.Get("session_id") == "" {
			headers.Set("session_id", state.sessionKey)
		}
		if headers.Get("X-Client-Request-Id") == "" {
			headers.Set("X-Client-Request-Id", state.sessionKey)
		}
	}
	if selection.Capabilities.Provider == "openai" && !responsesWebsocketBetaHeaderPresent(headers) {
		headers.Add("OpenAI-Beta", openAIResponsesWebsocketBetaHeaderValue)
	}
	if state.turnState != "" {
		headers.Set(codexTurnStateHeader, state.turnState)
	}
	if selection.Target.APIKey != "" {
		headers.Set("Authorization", "Bearer "+selection.Target.APIKey)
	} else if auth := r.Header.Get("Authorization"); auth != "" {
		headers.Set("Authorization", auth)
		forwardClientAuthHeaders(headers, r.Header)
	}

	dialer := *websocket.DefaultDialer
	dialer.EnableCompression = true
	upstreamConn, resp, err := dialer.Dial(upstreamURL, headers)
	if err != nil {
		return nil, err
	}
	if resp != nil {
		if turnState := resp.Header.Get(codexTurnStateHeader); turnState != "" {
			state.turnState = turnState
		}
	}

	state.upstream = upstreamConn
	state.upstreamBaseURL = selection.Target.BaseURL
	return upstreamConn, nil
}

func responsesWebsocketURL(baseURL string) (string, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return "", err
	}

	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported upstream scheme %q", u.Scheme)
	}

	u.Path = strings.TrimRight(u.Path, "/") + "/v1/responses"
	return u.String(), nil
}

func responsesWebsocketEventType(payload []byte) string {
	var event struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return ""
	}
	return event.Type
}

func responsesWebsocketBetaHeaderPresent(headers http.Header) bool {
	for _, value := range headers.Values("OpenAI-Beta") {
		for _, part := range strings.Split(value, ",") {
			if strings.TrimSpace(part) == openAIResponsesWebsocketBetaHeaderValue {
				return true
			}
		}
	}
	return false
}

func relayResponsesSSEToWebsocket(conn *websocket.Conn, body io.Reader, alerts []Alert) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 256*1024), 2*1024*1024)
	alertState := newResponsesAlertStreamState(alerts)
	currentEvent := ""

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			if line == "" {
				currentEvent = ""
			}
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "" || payload == "[DONE]" {
			continue
		}
		rewrittenPayload := []byte(payload)
		if rewritten, err := rewriteResponsesEventPayload([]byte(payload), currentEvent, alertState); err == nil {
			rewrittenPayload = rewritten
		}
		if err := conn.WriteMessage(websocket.TextMessage, rewrittenPayload); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

func writeResponsesWebsocketError(conn *websocket.Conn, status int, code, message string) error {
	payload := map[string]any{
		"type":   "error",
		"status": status,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return err
	}

	return fmt.Errorf("%s", message)
}

func decodeUpstreamErrorMessage(status int, body []byte) string {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Error.Message != "" {
		return payload.Error.Message
	}

	text := strings.TrimSpace(string(body))
	if text != "" {
		return text
	}

	return http.StatusText(status)
}
