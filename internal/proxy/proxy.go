package proxy

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/mercator-hq/truebearing/internal/audit"
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
	upstream    *url.URL
	st          *store.Store
	rp          *httputil.ReverseProxy
	pipeline    *engine.Pipeline
	signingKey  ed25519.PrivateKey // nil means audit records are not signed or persisted
	traceWriter *TraceWriter       // nil means trace capture is disabled
	tracer      trace.Tracer       // no-op by default; replaced by SetTracer when OTel is configured
	// logger is the structured slog logger used for all proxy log output.
	// The default discards all output until SetLogger is called by cmd/serve.go
	// after the --log-level flag is parsed.
	logger *slog.Logger
	// dbPath is stored for display in GET /health responses. It is the path
	// that was passed to store.Open() and is not used for any DB operation
	// inside the proxy itself.
	dbPath string

	// polMu protects livePol and polByFingerprint. Policy hot-reload via SIGHUP
	// atomically replaces livePol while retaining previous policy versions in
	// polByFingerprint so existing sessions can continue evaluating against the
	// policy version they were bound to at creation time.
	polMu            sync.RWMutex
	livePol          *policy.Policy
	polByFingerprint map[string]*policy.Policy
}

// New creates a Proxy that forwards traffic to upstream, uses st for agent
// authentication and session persistence, pol for policy evaluation, and
// records dbPath for health responses. The full evaluation pipeline
// (Env → MayUse → Budget → Taint → Sequence → Content → Escalation) is
// wired here so that cmd/serve.go can validate the policy at startup before
// binding a port.
//
// signingKey is the Ed25519 private key used to sign audit records. Pass nil
// if the key is unavailable — audit records will be logged but not persisted.
// See CLAUDE.md §8 security invariant 3: key files must be 0600.
//
// Design: pol is accepted here rather than loaded inside the proxy so that
// cmd/serve.go can validate the policy file at startup and fail fast before
// binding a port. Accepting a parsed *policy.Policy keeps the constructor
// pure and testable without requiring filesystem access.
func New(upstream *url.URL, st *store.Store, pol *policy.Policy, dbPath string, signingKey ed25519.PrivateKey) *Proxy {
	rp := httputil.NewSingleHostReverseProxy(upstream)
	pipeline := engine.New(
		// EnvEvaluator runs first: a wrong-environment agent has no business
		// executing any tool in this session regardless of which tool is called.
		&engine.EnvEvaluator{},
		&engine.MayUseEvaluator{},
		// DelegationEvaluator runs after MayUse: a child agent must be further
		// constrained to its parent's allowed tool set. If a tool passes MayUse
		// but is not in the parent's scope, delegation blocks it here before any
		// budget or sequence checks run.
		&engine.DelegationEvaluator{Store: &engine.StoreBackend{Store: st}},
		&engine.BudgetEvaluator{},
		&engine.TaintEvaluator{},
		&engine.SequenceEvaluator{Store: &engine.StoreBackend{Store: st}},
		&engine.ContentEvaluator{},
		&engine.RateLimitEvaluator{Store: &engine.StoreBackend{Store: st}},
		&engine.EscalationEvaluator{Store: &engine.StoreBackend{Store: st}},
	)
	return &Proxy{
		upstream:   upstream,
		st:         st,
		rp:         rp,
		pipeline:   pipeline,
		signingKey: signingKey,
		dbPath:     dbPath,
		// Default to a no-op tracer so emitDecisionSpan is always safe to call.
		// SetTracer replaces this when OTel is configured via --otel-endpoint.
		tracer: noop.NewTracerProvider().Tracer("truebearing"),
		// Default to a discard logger until SetLogger is called by cmd/serve.go.
		// This prevents unexpected log output in tests and library callers that
		// do not configure a log level.
		logger:           slog.New(slog.DiscardHandler),
		livePol:          pol,
		polByFingerprint: map[string]*policy.Policy{pol.Fingerprint: pol},
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

// Policy returns the currently live policy. After a SIGHUP hot-reload this
// returns the reloaded policy. It is safe to call concurrently.
func (p *Proxy) Policy() *policy.Policy {
	return p.currentPolicy()
}

// currentPolicy returns the live policy under a read lock.
func (p *Proxy) currentPolicy() *policy.Policy {
	p.polMu.RLock()
	defer p.polMu.RUnlock()
	return p.livePol
}

// policyForFingerprint looks up a previously-loaded policy by its fingerprint.
// Returns false if no policy with that fingerprint is held in memory, which
// indicates the proxy was restarted or a very old policy version was evicted.
func (p *Proxy) policyForFingerprint(fp string) (*policy.Policy, bool) {
	p.polMu.RLock()
	defer p.polMu.RUnlock()
	pol, ok := p.polByFingerprint[fp]
	return pol, ok
}

// ReloadPolicy re-parses the policy file at its original SourcePath and
// atomically replaces the live policy if the new file passes all ERROR-level
// lint rules. If parsing or linting fails the previous policy remains active
// and the error is returned to the caller for logging.
//
// Existing sessions are not disrupted: polByFingerprint retains all previously
// loaded policy versions so that sessions bound to an older fingerprint
// continue to evaluate correctly without receiving a 409 Conflict.
func (p *Proxy) ReloadPolicy() error {
	p.polMu.RLock()
	sourcePath := p.livePol.SourcePath
	p.polMu.RUnlock()

	if sourcePath == "" {
		return fmt.Errorf("policy was not loaded from a file; hot-reload is not supported")
	}

	newPol, err := policy.ParseFile(sourcePath)
	if err != nil {
		return fmt.Errorf("reloading policy from %s: %w", sourcePath, err)
	}

	// Reject the reload if any ERROR-level lint rule fires. The operator must
	// fix the policy file and send SIGHUP again. WARNING and INFO results are
	// logged but do not prevent the reload.
	results := policy.Lint(newPol)
	for _, r := range results {
		if r.Severity == policy.SeverityError {
			return fmt.Errorf("reload rejected — lint error in %s: %s %s", sourcePath, r.Code, r.Message)
		}
	}

	p.polMu.Lock()
	defer p.polMu.Unlock()
	p.livePol = newPol
	// Retain the previous policy in the registry so existing sessions remain
	// valid under their original fingerprint. Design: we keep all versions in
	// memory for the lifetime of the proxy. In practice the number of hot-reloads
	// per proxy lifetime is small (GitOps push cadence), so unbounded growth is
	// not a concern for the MVP.
	p.polByFingerprint[newPol.Fingerprint] = newPol
	return nil
}

// SetTraceWriter configures a TraceWriter for this proxy. When set, every
// incoming MCP tool call is appended to the trace file before the evaluation
// pipeline runs, so both allowed and denied calls are captured. Pass nil to
// disable trace capture (the default after New).
func (p *Proxy) SetTraceWriter(tw *TraceWriter) {
	p.traceWriter = tw
}

// SetTracer installs an OTel tracer for span emission. Call this from
// cmd/serve.go after InitTracer succeeds. The default tracer is a no-op, so
// omitting this call leaves the proxy fully functional without OTel.
func (p *Proxy) SetTracer(t trace.Tracer) {
	p.tracer = t
}

// SetLogger installs a structured slog logger for proxy and pipeline log output.
// Call this from cmd/serve.go after the --log-level flag is parsed and the
// slog.Handler is initialized. The same logger is wired into the engine pipeline
// so that debug-level evaluator chain logs appear in the same output stream as
// the proxy's info-level decision logs.
//
// Per CLAUDE.md §8 security invariant 4: argument values must never appear in
// log output. The decision log emits arguments_sha256 only.
func (p *Proxy) SetLogger(l *slog.Logger) {
	p.logger = l
	p.pipeline.SetLogger(l)
}

// emitDecisionSpan records a single OTel span for one policy decision. The
// span covers the period from request arrival to the moment the decision is
// reached, and carries attributes that match the AuditRecord fields exactly so
// the two can be correlated by session_id + seq without any additional join.
//
// Span errors are silently dropped — OTel is advisory and must never block
// a tool call or affect the decision outcome (fail open on observability).
func (p *Proxy) emitDecisionSpan(
	ctx context.Context,
	start time.Time,
	sessionID, agentName, toolName string,
	decision engine.Decision,
	policyFingerprint, clientTraceID string,
) {
	_, span := p.tracer.Start(
		ctx,
		"truebearing.tool_call",
		trace.WithTimestamp(start),
	)
	span.SetAttributes(
		attribute.String("truebearing.session_id", sessionID),
		attribute.String("truebearing.agent_name", agentName),
		attribute.String("truebearing.tool_name", toolName),
		attribute.String("truebearing.decision", string(decision.Action)),
		attribute.String("truebearing.rule_id", decision.RuleID),
		attribute.String("truebearing.policy_fingerprint", policyFingerprint),
		attribute.String("truebearing.client_trace_id", clientTraceID),
	)
	// Mark the span as an error for denied calls so observability dashboards
	// can filter on span status without parsing the decision attribute.
	if decision.Action == engine.Deny || decision.Action == engine.ShadowDeny {
		span.SetStatus(codes.Error, decision.Reason)
	}
	span.End()
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
			p.logger.ErrorContext(r.Context(), "check_escalation_status lookup failed",
				"escalation_id", escID,
				"error", err,
			)
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

	// Record arrival time before any DB operations so the timestamp in both
	// the trace file and the ToolCall struct reflects the actual request time.
	requestedAt := time.Now()

	// Capture the tool call to the trace file before the pipeline decision so
	// that both allowed and denied calls appear in the trace. The virtual
	// check_escalation_status tool is excluded — it is handled above and is
	// not replayed by truebearing simulate.
	p.writeTraceEntry(TraceEntry{
		SessionID:   sessionID,
		AgentName:   claims.AgentName,
		ToolName:    params.Name,
		Arguments:   params.Arguments,
		RequestedAt: requestedAt.Format(time.RFC3339),
	})

	// Load or create the session. Per mvp-plan.md §7.1a, session creation is
	// implicit on the first tools/call for a given session ID — no explicit
	// "start session" call is required from the agent.
	sess, err := p.st.GetSession(sessionID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			p.logger.ErrorContext(r.Context(), "session lookup failed",
				"session_id", sessionID,
				"error", err,
			)
			writeBadRequest(w, "session lookup failed")
			return
		}
		// First call for this session ID: bind the session to the fingerprint of
		// the currently live policy (Fix 3, mvp-plan.md §14). Reading the live
		// policy once here ensures consistency if a SIGHUP reload races with
		// session creation.
		livePol := p.currentPolicy()
		if createErr := p.st.CreateSession(sessionID, claims.AgentName, livePol.Fingerprint); createErr != nil {
			p.logger.ErrorContext(r.Context(), "session creation failed",
				"session_id", sessionID,
				"error", createErr,
			)
			writeBadRequest(w, "session creation failed")
			return
		}
		sess = &session.Session{
			ID:                sessionID,
			AgentName:         claims.AgentName,
			PolicyFingerprint: livePol.Fingerprint,
		}
	}

	// Resolve the policy version this session was bound to at creation. After
	// a SIGHUP hot-reload the live policy fingerprint may differ from the
	// session's fingerprint, but we continue evaluating with the session-bound
	// version so existing sessions are not disrupted. If the proxy was
	// restarted and the old policy version is no longer in memory, return 409
	// so the agent knows to start a fresh session.
	sessionPol, ok := p.policyForFingerprint(sess.PolicyFingerprint)
	if !ok {
		writeConflict(w, sess.PolicyFingerprint, p.currentPolicy().Fingerprint)
		return
	}

	// Terminated sessions reject all subsequent tool calls with 410 Gone.
	if sess.Terminated {
		writeGone(w, sessionID)
		return
	}

	// Build the engine's ToolCall representation. Arguments is kept as raw
	// JSON so evaluators can use gjson to extract specific paths without a
	// full unmarshal on every call. requestedAt was captured before the DB
	// lookups above so the timestamp is consistent with the trace entry.
	// AgentEnv is populated from the JWT "env" claim so the EnvEvaluator can
	// compare it against the policy's require_env without a DB lookup.
	// ParentAgent is populated from the JWT "parent_agent" claim so the
	// DelegationEvaluator can load the parent's allowed tools from the store.
	call := &engine.ToolCall{
		SessionID:   sessionID,
		AgentName:   claims.AgentName,
		ToolName:    params.Name,
		Arguments:   params.Arguments,
		AgentEnv:    claims.Env,
		ParentAgent: claims.ParentAgent,
		RequestedAt: requestedAt,
	}

	// Capture taint state before the pipeline so we can detect mutations.
	// The pipeline applies taint mutations to sess in memory; we persist them
	// to the database after the decision, per pipeline invariant 2.
	taintBefore := sess.Tainted

	// Run the evaluation pipeline against the session-bound policy. Errors
	// inside evaluators are converted to Deny by the pipeline (fail closed).
	// This call never returns an error.
	decision := p.pipeline.Evaluate(r.Context(), call, sess, sessionPol)

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
		p.logger.ErrorContext(r.Context(), "session event append failed",
			"session_id", sessionID,
			"tool", params.Name,
			"error", appendErr,
		)
		writeBadRequest(w, "session recording failed")
		return
	}

	// Write the signed audit record immediately after AppendEvent so that
	// event.Seq is available and the record is persisted regardless of which
	// decision branch runs below. Per pipeline invariant 1 (CLAUDE.md §6):
	// exactly one AuditRecord is written per tool call, regardless of outcome.
	p.writeAuditRecord(r, sessionID, claims.AgentName, claims.ParentAgent, params.Name, params.Arguments, event.Seq, decision, sessionPol.Fingerprint)

	// Emit an OTel span for this decision. The span attributes mirror the
	// AuditRecord fields so the two can be correlated without a join.
	// emitDecisionSpan is always safe to call — the tracer defaults to no-op
	// when OTel is not configured. Span errors are silently dropped.
	p.emitDecisionSpan(
		r.Context(),
		requestedAt,
		sessionID,
		claims.AgentName,
		params.Name,
		decision,
		sessionPol.Fingerprint,
		ExtractClientTraceID(r.Header),
	)

	// Log the policy decision at info level. This is the primary structured log
	// entry for each tool call and is the single source of truth for what
	// happened and why. The required fields — session_id, agent, tool, decision,
	// rule_id, trace_id — are included so log aggregators (Datadog, Splunk, etc.)
	// can filter and correlate without additional joins.
	//
	// Per CLAUDE.md §8 security invariant 4: argument values are never logged.
	// Only the sha256 of the raw arguments JSON is included for context.
	argSum := sha256.Sum256(params.Arguments)
	p.logger.InfoContext(r.Context(), "tool call evaluated",
		"session_id", sessionID,
		"agent", claims.AgentName,
		"tool", params.Name,
		"decision", string(decision.Action),
		"rule_id", decision.RuleID,
		"trace_id", ExtractClientTraceID(r.Header),
		"arguments_sha256", hex.EncodeToString(argSum[:]),
	)

	// Persist taint state mutation if the pipeline changed sess.Tainted.
	// Fail closed: if we cannot persist the new taint state, the session would
	// be in an inconsistent state on the next call.
	if sess.Tainted != taintBefore {
		if taintErr := p.st.UpdateSessionTaint(sessionID, sess.Tainted); taintErr != nil {
			p.logger.ErrorContext(r.Context(), "session taint update failed",
				"session_id", sessionID,
				"error", taintErr,
			)
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
			p.logger.WarnContext(r.Context(), "session counter increment failed",
				"session_id", sessionID,
				"error", counterErr,
			)
		}
		p.rp.ServeHTTP(w, r)

	case engine.Deny:
		writeJSONRPCDeny(w, mcpReq.ID, params.Name, decision)

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
			p.logger.ErrorContext(r.Context(), "escalation creation failed",
				"session_id", sessionID,
				"tool", params.Name,
				"error", escErr,
			)
			// Fail closed: if we cannot record the escalation, deny the call
			// rather than returning an escalated response with no backing record.
			writeJSONRPCError(w, mcpReq.ID, "escalation could not be recorded", "internal_error")
			return
		}
		// Fire the notification after the escalation is persisted. Delivery
		// failure is logged inside Notify and does not block the response.
		var notifyCfg *escalation.NotifyConfig
		if sessionPol.Escalation != nil {
			notifyCfg = &escalation.NotifyConfig{WebhookURL: sessionPol.Escalation.WebhookURL}
		}
		escalation.Notify(esc, decision.Reason, notifyCfg)
		writeJSONRPCEscalated(w, mcpReq.ID, escID)
	}
}

// writeAuditRecord builds a signed audit.AuditRecord from the pipeline decision
// and persists it via audit.Write. It is called once per tool call, immediately
// after AppendEvent, so event.Seq is available.
//
// If no signing key is loaded (p.signingKey == nil), the method logs a warning
// and returns without writing. This allows the proxy to operate without keys
// during local development, as described in mvp-plan.md §15. Per CLAUDE.md §8,
// the proxy never blocks a request because of an audit failure.
func (p *Proxy) writeAuditRecord(
	r *http.Request,
	sessionID, agentName, parentAgent, toolName string,
	arguments []byte,
	seq uint64,
	decision engine.Decision,
	policyFingerprint string,
) {
	if p.signingKey == nil {
		p.logger.WarnContext(r.Context(), "audit record not persisted: no signing key loaded",
			"session_id", sessionID,
			"seq", seq,
		)
		return
	}

	// sha256 of raw arguments JSON per CLAUDE.md §8 security invariant 4.
	argSum := sha256.Sum256(arguments)

	// sha256 of the raw Bearer token — AuthMiddleware has already validated it,
	// so bearerToken is guaranteed to succeed here.
	rawJWT, _ := bearerToken(r)
	jwtSum := sha256.Sum256([]byte(rawJWT))

	rec := &audit.AuditRecord{
		ID:                uuid.New().String(),
		SessionID:         sessionID,
		Seq:               seq,
		AgentName:         agentName,
		ToolName:          toolName,
		ArgumentsSHA256:   hex.EncodeToString(argSum[:]),
		Decision:          string(decision.Action),
		DecisionReason:    decision.Reason,
		PolicyFingerprint: policyFingerprint,
		AgentJWTSHA256:    hex.EncodeToString(jwtSum[:]),
		ClientTraceID:     ExtractClientTraceID(r.Header),
		DelegationChain:   buildDelegationChain(parentAgent, agentName),
		RecordedAt:        time.Now().UnixNano(),
	}

	if signErr := audit.Sign(rec, p.signingKey); signErr != nil {
		p.logger.ErrorContext(r.Context(), "signing audit record failed",
			"session_id", sessionID,
			"seq", seq,
			"error", signErr,
		)
		return
	}
	if writeErr := audit.Write(rec, p.st); writeErr != nil {
		p.logger.ErrorContext(r.Context(), "writing audit record failed",
			"session_id", sessionID,
			"seq", seq,
			"error", writeErr,
		)
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

// writeJSONRPCDeny writes a JSON-RPC 2.0 error response for a policy denial.
// It extends the standard error with a structured data object so LLM agents
// can parse the denial reason and self-correct without human intervention.
//
// The data object contains:
//   - blocked_tool: the tool name that was denied
//   - reason_code:  stable machine-readable string from the evaluator's DenyFeedback
//   - unsatisfied_prerequisites: tool names the agent must call first (may be empty)
//   - suggestion:   plain-English instruction the agent can use to guide a retry
//
// When decision.Feedback is nil (pipeline-level error → deny), only blocked_tool
// is populated in data; reason_code and suggestion are empty strings.
func writeJSONRPCDeny(w http.ResponseWriter, id json.RawMessage, blockedTool string, decision engine.Decision) {
	type dataObj struct {
		BlockedTool              string   `json:"blocked_tool"`
		ReasonCode               string   `json:"reason_code"`
		UnsatisfiedPrerequisites []string `json:"unsatisfied_prerequisites,omitempty"`
		Suggestion               string   `json:"suggestion"`
	}
	type errorObj struct {
		// -32000 is in the JSON-RPC server-error range (-32000 to -32099).
		// It is the conventional code for application-defined server errors.
		Code    int     `json:"code"`
		Message string  `json:"message"`
		Data    dataObj `json:"data"`
	}
	type response struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   errorObj        `json:"error"`
	}
	data := dataObj{BlockedTool: blockedTool}
	if decision.Feedback != nil {
		data.ReasonCode = decision.Feedback.ReasonCode
		data.UnsatisfiedPrerequisites = decision.Feedback.UnsatisfiedPrerequisites
		data.Suggestion = decision.Feedback.Suggestion
	}
	resp := response{
		JSONRPC: "2.0",
		ID:      id,
		Error: errorObj{
			Code:    -32000,
			Message: decision.Reason,
			Data:    data,
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

// writeTraceEntry captures a tool call to the trace file if a TraceWriter is
// configured. Errors are logged but do not block request processing — trace
// capture is advisory and must not affect the proxy's correctness or latency.
func (p *Proxy) writeTraceEntry(e TraceEntry) {
	if p.traceWriter == nil {
		return
	}
	if err := p.traceWriter.WriteEntry(e); err != nil {
		p.logger.Warn("trace capture failed",
			"tool", e.ToolName,
			"error", err,
		)
	}
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

// buildDelegationChain returns the delegation path string for the audit record.
// For root agents (parentAgent == ""), it returns "" so the field is omitted
// from the signed JSON payload via omitempty. For child agents it returns
// "parent → child", giving auditors the full delegation context at a glance.
func buildDelegationChain(parentAgent, agentName string) string {
	if parentAgent == "" {
		return ""
	}
	return parentAgent + " → " + agentName
}
