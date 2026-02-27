package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/store"
)

// Proxy is the TrueBearing HTTP reverse proxy. It chains JWT authentication,
// session validation, and the evaluation pipeline before forwarding allowed
// MCP requests to the upstream server.
//
// The evaluation pipeline in Task 3.5 is a stub that always allows tool calls.
// The real pipeline is wired in Task 4.8 once all evaluators are implemented.
type Proxy struct {
	upstream *url.URL
	st       *store.Store
	pol      *policy.Policy
	rp       *httputil.ReverseProxy
	// dbPath is stored for display in GET /health responses. It is the path
	// that was passed to store.Open() and is not used for any DB operation
	// inside the proxy itself.
	dbPath string
}

// New creates a Proxy that forwards traffic to upstream, uses st for agent
// authentication, pol for policy evaluation, and records dbPath for health
// responses.
//
// Design: pol is accepted here rather than loaded inside the proxy so that
// cmd/serve.go can validate the policy file at startup and fail fast before
// binding a port. Accepting a parsed *policy.Policy keeps the constructor
// pure and testable without requiring filesystem access.
func New(upstream *url.URL, st *store.Store, pol *policy.Policy, dbPath string) *Proxy {
	rp := httputil.NewSingleHostReverseProxy(upstream)
	return &Proxy{
		upstream: upstream,
		st:       st,
		pol:      pol,
		rp:       rp,
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
// such as the health endpoint and the engine pipeline (Task 4.8) use this to
// access the policy fingerprint and rule configuration.
func (p *Proxy) Policy() *policy.Policy {
	return p.pol
}

// handleMCP is the inner handler reached after auth and session middleware pass.
// It detects tool calls and routes them through the evaluation pipeline, then
// forwards allowed calls upstream. Non-tool MCP methods are forwarded directly.
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

	// Tool call path: run the evaluation pipeline, then forward on allow.
	// TODO(task-4.1): replace this stub with engine.Pipeline.Evaluate using
	// the real evaluator chain (MayUse → Budget → Taint → Sequence → Escalation).
	// The stub currently allows all tool calls that pass auth and session checks.
	p.rp.ServeHTTP(w, r)
}
