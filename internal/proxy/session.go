package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/mercator-hq/truebearing/pkg/mcpparse"
)

// sessionIDKey is the context key for the X-TrueBearing-Session-ID header value
// stored by SessionMiddleware. Its numeric value (1) is chosen to not collide with
// claimsKey (0) which is defined in auth.go using iota.
const sessionIDKey contextKey = 1

// sessionIDHeader is the required header name for session correlation on tool calls.
const sessionIDHeader = "X-TrueBearing-Session-ID"

// SessionIDFromContext retrieves the session ID stored by SessionMiddleware in the
// request context. Returns (id, true) if present, ("", false) if absent.
func SessionIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(sessionIDKey).(string)
	return id, ok
}

// SessionMiddleware returns an HTTP middleware that enforces the presence of the
// X-TrueBearing-Session-ID header on tools/call requests, and stores the session ID
// in the request context for downstream handlers.
//
// Design: the session ID header is enforced only on tools/call requests, not on all
// MCP methods. Non-tool methods (tools/list, initialize, ping, etc.) are stateless
// operations that do not require sequence tracking — the evaluation pipeline never
// runs for them. Requiring the session header on every method would force SDK authors
// to manage session IDs during the MCP initialization handshake, adding friction without
// any security benefit. Session context is meaningless without a tool call to evaluate.
//
// Per mvp-plan.md §7.1a: if a tools/call arrives without this header the proxy returns
// immediately with a 400 error. No evaluation runs. No audit record is written.
func SessionMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Read the full body so we can inspect the MCP method, then restore it
			// so downstream handlers (the proxy, the engine) can read it again.
			var body []byte
			if r.Body != nil {
				var err error
				body, err = io.ReadAll(r.Body)
				if err != nil {
					writeBadRequest(w, "could not read request body")
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
			}

			// Only enforce the session header for tools/call requests.
			// For any other MCP method — or for non-parseable bodies — pass through.
			if isToolCall(body) {
				sessionID := r.Header.Get(sessionIDHeader)
				if sessionID == "" {
					writeMissingSessionID(w)
					return
				}
				ctx := context.WithValue(r.Context(), sessionIDKey, sessionID)
				r = r.WithContext(ctx)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// isToolCall returns true if the body is a valid JSON-RPC 2.0 tools/call request.
// Any body that fails to parse, or that carries a different method, returns false;
// those requests are forwarded without session enforcement.
func isToolCall(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	req, err := mcpparse.ParseRequest(body)
	if err != nil {
		return false
	}
	return mcpparse.IsTool(req)
}

// writeMissingSessionID writes the 400 JSON response prescribed by mvp-plan.md §7.1a
// when a tools/call request arrives without the X-TrueBearing-Session-ID header.
func writeMissingSessionID(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	body, _ := json.Marshal(struct {
		Error   string `json:"error"`
		Message string `json:"message"`
		Code    int    `json:"code"`
	}{
		Error:   "missing_session_id",
		Message: "X-TrueBearing-Session-ID header is required on all tools/call requests.",
		Code:    400,
	})
	_, _ = w.Write(body)
}

// writeBadRequest writes a generic 400 JSON error for unexpected body-read failures.
// Per CLAUDE.md §8: fail closed — any condition that prevents proper evaluation returns
// an error response rather than defaulting to allow.
func writeBadRequest(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	body, _ := json.Marshal(struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}{
		Error:   "bad_request",
		Message: message,
	})
	_, _ = w.Write(body)
}
