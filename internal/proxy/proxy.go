package proxy

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"

	"github.com/mercator-hq/truebearing/internal/engine"
	"github.com/mercator-hq/truebearing/internal/escalation"
	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/session"
	"github.com/mercator-hq/truebearing/internal/store"
	"github.com/mercator-hq/truebearing/pkg/mcpparse"
)

// costPerToolCall is the flat MVP cost estimate applied to every allowed tool
// call. Post-MVP: replace with actual token counts parsed from upstream MCP
// server responses. See mvp-plan.md §8.3.
const costPerToolCall = 0.001

// Proxy is the TrueBearing HTTP reverse proxy. It chains JWT authentication,
// session validation, and the evaluation pipeline before forwarding allowed
// MCP requests to the upstream server.
type Proxy struct {
	upstream *url.URL
	st       *store.Store
	pol      *policy.Policy
	rp       *httputil.ReverseProxy
	pipeline *engine.Pipeline
	// dbPath is stored for display in GET /health responses. It is the path
	// that was passed to store.Open() and is not used for any DB operation
	// inside the proxy itself.
	dbPath string
}

// New creates a Proxy that forwards traffic to upstream, uses st for agent
// authentication and session persistence, pol for policy evaluation, and
// records dbPath for health responses. The full evaluation pipeline
// (MayUse → Budget → Taint → Sequence → Escalation) is wired here so that
// cmd/serve.go can validate the policy at startup before binding a port.
//
// Design: pol is accepted here rather than loaded inside the proxy so that
// cmd/serve.go can validate the policy file at startup and fail fast before
// binding a port. Accepting a parsed *policy.Policy keeps the constructor
// pure and testable without requiring filesystem access.
func New(upstream *url.URL, st *store.Store, pol *policy.Policy, dbPath string) *Proxy {
	rp := httputil.NewSingleHostReverseProxy(upstream)
	pipeline := engine.New(
		&engine.MayUseEvaluator{},
		&engine.BudgetEvaluator{},
		&engine.TaintEvaluator{},
		&engine.SequenceEvaluator{Store: st},
		&engine.EscalationEvaluator{Store: st},
	)
	return &Proxy{
		upstream: upstream,
		st:       st,
		pol:      pol,
		rp:       rp,
		pipeline: pipeline,
		dbPath:   dbPath,
	}
}

// Handler returns the top-level HTTP handler. GET /health is registered before
// the auth middleware so SDK subprocess management can poll it without a JWT.
// All other requests are routed through: auth middleware → session middleware →
// MCP router.
//
// Design: a ServeMux is used so /health can bypass auth via explicit route
// registration rather than conditional logic inside a middleware. This makes
// the no-auth contract visible at a glance and prevents accidental auth bypass
// on other routes.
func (p *Proxy) Handler() http.Handler {
	mux := http.NewServeMux()
	// Health check bypasses auth — no JWT required. Registered first so the
	// pattern match is unambiguous. SDK subprocess readiness polls this route.
	mux.HandleFunc("/health", p.handleHealth)
	// All other routes go through JWT auth then session enforcement.
	mux.Handle("/", AuthMiddleware(p.st)(SessionMiddleware()(http.HandlerFunc(p.handleMCP))))
	return mux
}

// Policy returns the parsed policy loaded at proxy startup. Downstream handlers
// such as the health endpoint use this to access the policy fingerprint.
func (p *Proxy) Policy() *policy.Policy {
	return p.pol
}

// handleMCP is the inner handler reached after auth and session middleware pass.
// It detects tool calls, runs them through the evaluation pipeline, and either
// forwards allowed calls upstream or returns a synthetic JSON-RPC response for
// denied and escalated calls.
//
// Per pipeline invariant 1 (CLAUDE.md §6): exactly one session event is written
// per tool call, regardless of the decision outcome.
func (p *Proxy) handleMCP(w http.ResponseWriter, r *http.Request) {
	// SessionMiddleware has already read and restored the body once. We read it
	// again here to determine the MCP method, then restore it for the reverse
	// proxy which needs to forward the body bytes upstream.
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

	if !isToolCall(body) {
		// Non-tool MCP methods (tools/list, initialize, ping, etc.) bypass
		// evaluation entirely and are forwarded to the upstream unchanged.
		p.rp.ServeHTTP(w, r)
		return
	}

	// Parse the request to extract the JSON-RPC ID (needed to construct
	// JSON-RPC error responses) and tool call parameters (tool name, arguments).
	mcpReq, err := mcpparse.ParseRequest(body)
	if err != nil {
		writeBadRequest(w, "malformed MCP request")
		return
	}
	params, err := mcpparse.ParseToolCallParams(mcpReq.Params)
	if err != nil {
		writeBadRequest(w, "malformed tool call params")
		return
	}

	// check_escalation_status is a TrueBearing-injected virtual tool (mvp-plan.md §7.4).
	// It is never forwarded to the upstream MCP server — TrueBearing handles it entirely
	// by querying the escalations table and returning a synthetic JSON-RPC response.
	// This interception happens before session load and pipeline evaluation so that an
	// agent can poll escalation status even when the session is otherwise blocked.
	if params.Name == "check_escalation_status" {
		escID := gjson.GetBytes(params.Arguments, "escalation_id").String()
		if escID == "" {
			writeJSONRPCError(w, mcpReq.ID, "check_escalation_status requires an escalation_id argument", "virtual_tool_error")
			return
		}
		status, err := escalation.GetStatus(escID, p.st)
		if err != nil {
			log.Printf("proxy: check_escalation_status %q: %v", escID, err)
			writeJSONRPCError(w, mcpReq.ID, "escalation not found", "escalation_not_found")
			return
		}
		writeJSONRPCEscalationStatus(w, mcpReq.ID, escID, status)
		return
	}

	// SessionMiddleware guarantees the session ID is in context for tool calls.
	sessionID, _ := SessionIDFromContext(r.Context())
	// AuthMiddleware guarantees claims are in context for all authenticated requests.
	claims, _ := AgentClaimsFromContext(r.Context())

	// Load or create the session. Per mvp-plan.md §7.1a, session creation is
	// implicit on the first tools/call for a given session ID — no explicit
	// "start session" call is required from the agent.
	sess, err := p.st.GetSession(sessionID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("proxy: looking up session %q: %v", sessionID, err)
			writeBadRequest(w, "session lookup failed")
			return
		}
		// First call for this session ID: bind the session to the current
		// policy fingerprint (Fix 3, mvp-plan.md §14).
		if createErr := p.st.CreateSession(sessionID, claims.AgentName, p.pol.Fingerprint); createErr != nil {
			log.Printf("proxy: creating session %q: %v", sessionID, createErr)
			writeBadRequest(w, "session creation failed")
			return
		}
		sess = &session.Session{
			ID:                sessionID,
			AgentName:         claims.AgentName,
			PolicyFingerprint: p.pol.Fingerprint,
		}
	}

	// Per mvp-plan.md §14 Fix 3: reject calls whose session was created under
	// a different policy fingerprint. Agents must start a new session after a
	// policy update. Silent re-evaluation under a changed policy creates audit gaps.
	if sess.PolicyFingerprint != p.pol.Fingerprint {
		writeConflict(w, sess.PolicyFingerprint, p.pol.Fingerprint)
		return
	}

	// Terminated sessions reject all subsequent tool calls with 410 Gone.
	if sess.Terminated {
		writeGone(w, sessionID)
		return
	}

	// Build the engine's ToolCall representation. Arguments is kept as raw
	// JSON so evaluators can use gjson to extract specific paths without a
	// full unmarshal on every call.
	call := &engine.ToolCall{
		SessionID:   sessionID,
		AgentName:   claims.AgentName,
		ToolName:    params.Name,
		Arguments:   params.Arguments,
		RequestedAt: time.Now(),
	}

	// Capture taint state before the pipeline so we can detect mutations.
	// The pipeline applies taint mutations to sess in memory; we persist them
	// to the database after the decision, per pipeline invariant 2.
	taintBefore := sess.Tainted

	// Run the evaluation pipeline. Errors inside evaluators are converted to
	// Deny by the pipeline (fail closed). This call never returns an error.
	decision := p.pipeline.Evaluate(r.Context(), call, sess, p.pol)

	// Append exactly one session event per tool call, per pipeline invariant 1.
	// This must succeed before any response is written so the session history
	// is consistent even on subsequent calls.
	event := &store.SessionEvent{
		SessionID:     sessionID,
		ToolName:      params.Name,
		ArgumentsJSON: string(params.Arguments),
		Decision:      string(decision.Action),
		PolicyRule:    decision.RuleID,
	}
	if appendErr := p.st.AppendEvent(event); appendErr != nil {
		log.Printf("proxy: appending session event for session %q tool %q: %v", sessionID, params.Name, appendErr)
		writeBadRequest(w, "session recording failed")
		return
	}

	// Persist taint state mutation if the pipeline changed sess.Tainted.
	// Fail closed: if we cannot persist the new taint state, the session would
	// be in an inconsistent state on the next call.
	if sess.Tainted != taintBefore {
		if taintErr := p.st.UpdateSessionTaint(sessionID, sess.Tainted); taintErr != nil {
			log.Printf("proxy: updating session taint for %q: %v", sessionID, taintErr)
			writeBadRequest(w, "session state update failed")
			return
		}
	}

	switch decision.Action {
	case engine.Allow, engine.ShadowDeny:
		// Both Allow and ShadowDeny forward the call to the upstream. ShadowDeny
		// records a policy violation in the audit log but permits the call through
		// because the effective enforcement mode for this tool is shadow.
		if counterErr := p.st.IncrementSessionCounters(sessionID, costPerToolCall); counterErr != nil {
			// Counter failures are advisory: the call has already been evaluated
			// and the session event written. Log and proceed so a bookkeeping
			// failure does not block the agent's work.
			log.Printf("proxy: incrementing counters for session %q: %v", sessionID, counterErr)
		}
		p.rp.ServeHTTP(w, r)

	case engine.Deny:
		writeJSONRPCError(w, mcpReq.ID, decision.Reason, decision.RuleID)

	case engine.Escalate:
		// Create a pending escalation record. The agent polls
		// check_escalation_status with the returned ID to learn when an
		// operator resolves the escalation via "truebearing escalation approve".
		escID := uuid.New().String()
		esc := &store.Escalation{
			ID:            escID,
			SessionID:     sessionID,
			Seq:           event.Seq,
			ToolName:      params.Name,
			ArgumentsJSON: string(params.Arguments),
			Status:        "pending",
		}
		if escErr := p.st.CreateEscalation(esc); escErr != nil {
			log.Printf("proxy: creating escalation for session %q tool %q: %v", sessionID, params.Name, escErr)
			// Fail closed: if we cannot record the escalation, deny the call
			// rather than returning an escalated response with no backing record.
			writeJSONRPCError(w, mcpReq.ID, "escalation could not be recorded", "internal_error")
			return
		}
		// Fire the notification after the escalation is persisted. Delivery
		// failure is logged inside Notify and does not block the response.
		var notifyCfg *escalation.NotifyConfig
		if p.pol.Escalation != nil {
			notifyCfg = &escalation.NotifyConfig{WebhookURL: p.pol.Escalation.WebhookURL}
		}
		escalation.Notify(esc, decision.Reason, notifyCfg)
		writeJSONRPCEscalated(w, mcpReq.ID, escID)
	}
}

// writeJSONRPCError writes a JSON-RPC 2.0 error response for a policy denial.
// The HTTP status is 200 OK per the JSON-RPC 2.0 specification; the error is
// encoded in the response body. The message is the human-readable policy
// violation reason; per CLAUDE.md §8 it must not contain internal error detail.
func writeJSONRPCError(w http.ResponseWriter, id json.RawMessage, message, ruleID string) {
	type dataObj struct {
		RuleID   string `json:"rule_id"`
		Decision string `json:"decision"`
	}
	type errorObj struct {
		// -32603 is the JSON-RPC Internal Error code; we repurpose it for
		// policy denials because the spec does not define a "policy denial" code.
		Code    int     `json:"code"`
		Message string  `json:"message"`
		Data    dataObj `json:"data"`
	}
	type response struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   errorObj        `json:"error"`
	}
	resp := response{
		JSONRPC: "2.0",
		ID:      id,
		Error: errorObj{
			Code:    -32603,
			Message: message,
			Data:    dataObj{RuleID: ruleID, Decision: "deny"},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	body, _ := json.Marshal(resp)
	_, _ = w.Write(body)
}

// writeJSONRPCEscalated writes the synthetic JSON-RPC 2.0 success response
// returned when a tool call triggers an escalate_when rule. The caller is
// expected to poll check_escalation_status with the returned escalation ID.
// Per mvp-plan.md §7.4, the HTTP connection is not held open while awaiting
// human review — the agent uses the virtual tool to poll for a decision.
func writeJSONRPCEscalated(w http.ResponseWriter, id json.RawMessage, escalationID string) {
	// The text payload is JSON-encoded so the LLM can parse it as structured
	// data and extract the escalation ID for subsequent polling calls.
	textPayload, _ := json.Marshal(map[string]string{
		"status":        "escalated",
		"escalation_id": escalationID,
		"message":       "This action requires human approval. Call check_escalation_status with this ID to poll for a decision.",
	})
	type contentItem struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type result struct {
		Content []contentItem `json:"content"`
	}
	type response struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  result          `json:"result"`
	}
	resp := response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result{Content: []contentItem{{Type: "text", Text: string(textPayload)}}},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	body, _ := json.Marshal(resp)
	_, _ = w.Write(body)
}

// writeJSONRPCEscalationStatus writes the synthetic JSON-RPC 2.0 success response
// for a check_escalation_status virtual tool call. The payload contains the
// escalation ID and its current status so the LLM can parse it as structured data.
func writeJSONRPCEscalationStatus(w http.ResponseWriter, id json.RawMessage, escalationID, status string) {
	textPayload, _ := json.Marshal(map[string]string{
		"escalation_id": escalationID,
		"status":        status,
	})
	type contentItem struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type result struct {
		Content []contentItem `json:"content"`
	}
	type response struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  result          `json:"result"`
	}
	resp := response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result{Content: []contentItem{{Type: "text", Text: string(textPayload)}}},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	body, _ := json.Marshal(resp)
	_, _ = w.Write(body)
}

// writeConflict writes the 409 Conflict response for tool calls that arrive on
// a session created under a different policy fingerprint. Per mvp-plan.md §14
// Fix 3, the agent must start a new session after a policy change.
func writeConflict(w http.ResponseWriter, sessionFingerprint, currentFingerprint string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	body, _ := json.Marshal(struct {
		Error                    string `json:"error"`
		Message                  string `json:"message"`
		SessionPolicyFingerprint string `json:"session_policy_fingerprint"`
		CurrentPolicyFingerprint string `json:"current_policy_fingerprint"`
	}{
		Error:                    "policy_changed",
		Message:                  "Policy was updated after this session was created. Start a new session to continue under the updated policy.",
		SessionPolicyFingerprint: sessionFingerprint,
		CurrentPolicyFingerprint: currentFingerprint,
	})
	_, _ = w.Write(body)
}

// writeGone writes a 410 Gone response for tool calls on terminated sessions.
// Terminated sessions are hard-closed by the operator via "truebearing session
// terminate" and reject all subsequent tool calls.
func writeGone(w http.ResponseWriter, sessionID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusGone)
	body, _ := json.Marshal(struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}{
		Error:   "session_terminated",
		Message: "Session " + sessionID + " has been terminated. All further tool calls are rejected.",
	})
	_, _ = w.Write(body)
}
