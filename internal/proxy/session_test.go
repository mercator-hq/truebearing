package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// toolsCallBody is a minimal valid JSON-RPC 2.0 tools/call request used across tests.
const toolsCallBody = `{"jsonrpc":"2.0","id":"1","method":"tools/call","params":{"name":"some_tool","arguments":{}}}`

// toolsListBody is a minimal valid JSON-RPC 2.0 tools/list request (non-tool method).
const toolsListBody = `{"jsonrpc":"2.0","id":"2","method":"tools/list","params":{}}`

// runSessionMiddleware invokes SessionMiddleware with a handler that records whether
// it was reached and captures the session ID from context if present.
func runSessionMiddleware(t *testing.T, r *http.Request) (statusCode int, body string, sessionID string) {
	t.Helper()
	var capturedID string
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if id, ok := SessionIDFromContext(req.Context()); ok {
			capturedID = id
		}
		w.WriteHeader(http.StatusOK)
	})
	rr := httptest.NewRecorder()
	SessionMiddleware()(handler).ServeHTTP(rr, r)
	return rr.Code, rr.Body.String(), capturedID
}

func TestSessionMiddleware_ToolsCall_MissingHeader_Returns400(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/mcp/v1", strings.NewReader(toolsCallBody))
	r.Header.Set("Content-Type", "application/json")

	code, body, _ := runSessionMiddleware(t, r)

	if code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", code)
	}
	if !strings.Contains(body, `"missing_session_id"`) {
		t.Errorf("body must contain error=missing_session_id, got %q", body)
	}
}

func TestSessionMiddleware_ToolsList_MissingHeader_Forwarded(t *testing.T) {
	// Non-tool methods must pass through even without the session header.
	r := httptest.NewRequest(http.MethodPost, "/mcp/v1", strings.NewReader(toolsListBody))
	r.Header.Set("Content-Type", "application/json")

	code, _, _ := runSessionMiddleware(t, r)

	if code != http.StatusOK {
		t.Errorf("status: want 200 (non-tool forwarded), got %d", code)
	}
}

func TestSessionMiddleware_ToolsCall_WithHeader_SessionIDInContext(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/mcp/v1", strings.NewReader(toolsCallBody))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(sessionIDHeader, "sess-abc-123")

	code, _, id := runSessionMiddleware(t, r)

	if code != http.StatusOK {
		t.Errorf("status: want 200, got %d", code)
	}
	if id != "sess-abc-123" {
		t.Errorf("session ID: want %q, got %q", "sess-abc-123", id)
	}
}

func TestSessionMiddleware_NonJSONBody_Forwarded(t *testing.T) {
	// Non-parseable bodies cannot be tool calls; they must pass through unchanged.
	r := httptest.NewRequest(http.MethodPost, "/mcp/v1", strings.NewReader("not json at all"))

	code, _, _ := runSessionMiddleware(t, r)

	if code != http.StatusOK {
		t.Errorf("status: want 200 (non-JSON body forwarded), got %d", code)
	}
}

func TestSessionMiddleware_EmptyBody_Forwarded(t *testing.T) {
	// An empty body is not a tool call; forward it without enforcement.
	r := httptest.NewRequest(http.MethodPost, "/mcp/v1", http.NoBody)

	code, _, _ := runSessionMiddleware(t, r)

	if code != http.StatusOK {
		t.Errorf("status: want 200 (empty body forwarded), got %d", code)
	}
}

func TestSessionMiddleware_BodyRestoredForNextHandler(t *testing.T) {
	// The middleware reads the body to detect the method, then must restore it
	// so that downstream handlers (the reverse proxy, the engine) can read it too.
	r := httptest.NewRequest(http.MethodPost, "/mcp/v1", strings.NewReader(toolsCallBody))
	r.Header.Set(sessionIDHeader, "sess-restore-test")

	var bodyReadByNext string
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		bodyReadByNext = string(b)
		w.WriteHeader(http.StatusOK)
	})

	rr := httptest.NewRecorder()
	SessionMiddleware()(handler).ServeHTTP(rr, r)

	if bodyReadByNext != toolsCallBody {
		t.Errorf("body not restored for next handler: got %q, want %q", bodyReadByNext, toolsCallBody)
	}
}

func TestSessionMiddleware_400ResponseFormat(t *testing.T) {
	// The 400 response must be valid JSON with the correct Content-Type and error fields.
	r := httptest.NewRequest(http.MethodPost, "/mcp/v1", strings.NewReader(toolsCallBody))

	rr := httptest.NewRecorder()
	SessionMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rr, r)

	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: want application/json, got %q", ct)
	}
	var resp struct {
		Error   string `json:"error"`
		Message string `json:"message"`
		Code    int    `json:"code"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parsing 400 response body: %v", err)
	}
	if resp.Error != "missing_session_id" {
		t.Errorf("error field: want %q, got %q", "missing_session_id", resp.Error)
	}
	if resp.Message == "" {
		t.Error("message field must not be empty in 400 response")
	}
	if resp.Code != 400 {
		t.Errorf("code field: want 400, got %d", resp.Code)
	}
}

func TestSessionMiddleware_NoSessionInContextForNonToolMethod(t *testing.T) {
	// Session ID must not appear in context for non-tool methods, even when the header
	// is present — context population is tied to enforcement, not header presence.
	r := httptest.NewRequest(http.MethodPost, "/mcp/v1", strings.NewReader(toolsListBody))
	r.Header.Set(sessionIDHeader, "sess-ignored")

	_, _, id := runSessionMiddleware(t, r)

	// The session ID is stored in context only when we enforce — i.e. on tool calls.
	// For tools/list the header is present but we did not go through the enforcement
	// branch, so SessionIDFromContext returns ("", false) and capturedID remains "".
	if id != "" {
		t.Errorf("session ID should not be in context for non-tool method, got %q", id)
	}
}

// --- Unit tests for unexported helpers ---

func TestIsToolCall(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "tools/call is a tool call",
			body: toolsCallBody,
			want: true,
		},
		{
			name: "tools/list is not a tool call",
			body: toolsListBody,
			want: false,
		},
		{
			name: "initialize is not a tool call",
			body: `{"jsonrpc":"2.0","id":"3","method":"initialize","params":{}}`,
			want: false,
		},
		{
			name: "empty body is not a tool call",
			body: "",
			want: false,
		},
		{
			name: "non-JSON body is not a tool call",
			body: "not json",
			want: false,
		},
		{
			name: "missing jsonrpc field is not a tool call",
			body: `{"id":"1","method":"tools/call"}`,
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isToolCall([]byte(tc.body))
			if got != tc.want {
				t.Errorf("isToolCall(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}
