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
	"strings"

	"github.com/gorilla/websocket"
)

const responsesWebsocketReadLimit = 8 << 20

var responsesWebsocketUpgrader = websocket.Upgrader{
	ReadBufferSize:  32 * 1024,
	WriteBufferSize: 32 * 1024,
	CheckOrigin: func(_ *http.Request) bool {
		return true
	},
}

type responsesWebsocketBridgeState struct {
	turnState string
}

func (p *Proxy) handleResponsesWebsocket(w http.ResponseWriter, r *http.Request) {
	conn, err := responsesWebsocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Debug("proxy websocket upgrade failed", "error", err)
		return
	}
	defer func() { _ = conn.Close() }()

	conn.SetReadLimit(responsesWebsocketReadLimit)
	state := &responsesWebsocketBridgeState{
		turnState: r.Header.Get(codexTurnStateHeader),
	}

	for {
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}
		if err := p.handleResponsesWebsocketMessage(conn, r, state, payload); err != nil {
			slog.Debug("proxy websocket request failed", "error", err)
			return
		}
	}
}

func (p *Proxy) handleResponsesWebsocketMessage(conn *websocket.Conn, r *http.Request, state *responsesWebsocketBridgeState, payload []byte) error {
	rawBody, err := sanitizeResponsesWebsocketRequest(payload)
	if err != nil {
		return writeResponsesWebsocketError(conn, http.StatusBadRequest, "invalid_request_error", "invalid websocket request: "+err.Error())
	}

	if p.cfg.DryRun {
		return writeResponsesWebsocketError(conn, http.StatusBadRequest, "invalid_request_error", "websocket mode is not supported in dry-run")
	}

	runtime := p.runtimeForSession(requestSessionKey(r, rawBody))

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

	var pipeResult *PipelineResult
	if runtime.pipeline != nil {
		adapter := SelectFilterAdapter(selection.Capabilities, filteredMsgs)
		var pipeErr error
		filteredMsgs, pipeResult, pipeErr = runtime.pipeline.Process(filteredMsgs, adapter)
		if pipeErr != nil {
			if runtime.audit != nil && pipeResult != nil {
				runtime.audit.Record(AuditEntry{
					Timestamp:    timeNow(),
					Model:        req.Model,
					BytesBefore:  pipeResult.BytesBefore,
					BytesAfter:   0,
					BytesRemoved: pipeResult.BytesBefore,
					SecretsFound: pipeResult.SecretsFound,
					SecretPolicy: string(pipeResult.SecretPolicy),
					Blocked:      true,
				})
			}
			if runtime.dnd != nil {
				runtime.dnd.RecordError()
			}
			return writeResponsesWebsocketError(conn, http.StatusForbidden, "invalid_request_error", "request blocked: "+pipeErr.Error())
		}

		if runtime.audit != nil {
			runtime.audit.Record(AuditEntry{
				Timestamp:    timeNow(),
				Model:        req.Model,
				BytesBefore:  pipeResult.BytesBefore,
				BytesAfter:   pipeResult.BytesAfter,
				BytesRemoved: pipeResult.BytesBefore - pipeResult.BytesAfter,
				FiltersRun:   pipeResult.FiltersRun,
				SecretsFound: pipeResult.SecretsFound,
				SecretPolicy: string(pipeResult.SecretPolicy),
			})
		}

		if p.cfg.OnRequest != nil {
			p.cfg.OnRequest(RequestEvent{
				Model:        req.Model,
				BytesBefore:  pipeResult.BytesBefore,
				BytesAfter:   pipeResult.BytesAfter,
				FiltersRun:   pipeResult.FiltersRun,
				SecretsFound: pipeResult.SecretsFound,
			})
		}
	}

	if selection.Capabilities.WireAPI != WireAPIResponses {
		return writeResponsesWebsocketError(conn, http.StatusBadRequest, "invalid_request_error", "websocket mode requires a Responses-compatible upstream")
	}

	body := rawBody
	if pipeResult != nil {
		body, err = RewriteResponsesRequest(rawBody, originalMsgs, filteredMsgs)
		if err != nil {
			return writeResponsesWebsocketError(conn, http.StatusInternalServerError, "server_error", "rewrite responses request: "+err.Error())
		}
	}

	upstreamURL := strings.TrimRight(selection.Target.BaseURL, "/") + "/v1/responses"
	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstreamURL, bytes.NewReader(body))
	if err != nil {
		return writeResponsesWebsocketError(conn, http.StatusInternalServerError, "server_error", "create upstream request: "+err.Error())
	}
	upReq.Header.Set("Content-Type", "application/json")
	upReq.Header.Set("Accept", "text/event-stream")
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

	return relayResponsesSSEToWebsocket(conn, upResp.Body)
}

func sanitizeResponsesWebsocketRequest(payload []byte) ([]byte, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(payload, &doc); err != nil {
		return nil, err
	}

	rawType, ok := doc["type"]
	if !ok {
		return nil, fmt.Errorf("missing type")
	}

	var reqType string
	if err := json.Unmarshal(rawType, &reqType); err != nil {
		return nil, fmt.Errorf("decode type: %w", err)
	}
	if reqType != "response.create" {
		return nil, fmt.Errorf("unsupported websocket request type %q", reqType)
	}

	delete(doc, "type")
	delete(doc, "client_metadata")
	delete(doc, "generate")

	return json.Marshal(doc)
}

func relayResponsesSSEToWebsocket(conn *websocket.Conn, body io.Reader) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 256*1024), 2*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "" || payload == "[DONE]" {
			continue
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
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
