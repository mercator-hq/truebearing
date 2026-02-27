package proxy

import (
	"encoding/json"
	"net/http"
	"os"
)

// proxyVersion is the current proxy binary version, embedded in /health responses
// so SDK subprocess management can verify it is talking to a compatible proxy.
const proxyVersion = "0.1.0"

// healthResponse is the JSON body returned by GET /health.
type healthResponse struct {
	Status            string `json:"status"`
	PolicyFingerprint string `json:"policy_fingerprint,omitempty"`
	PolicyFile        string `json:"policy_file,omitempty"`
	ProxyVersion      string `json:"proxy_version,omitempty"`
	DBPath            string `json:"db_path,omitempty"`
	Reason            string `json:"reason,omitempty"`
}

// handleHealth handles GET /health. This endpoint is registered on the mux
// before the auth middleware, so it requires no JWT.
//
// A 200 response signals the proxy is fully operational. The Python and Node
// SDKs poll this endpoint after spawning the proxy subprocess to determine
// when it is safe to start forwarding MCP requests.
//
// A 503 response signals a degraded state; the Reason field names the unhealthy
// component.
func (p *Proxy) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Check that the policy source file is still accessible on disk. The proxy
	// loaded and parsed it at startup; if it has since become unreadable, report
	// degraded so operators know the running policy may diverge from disk.
	// SourcePath is empty when policy was loaded from bytes (e.g. in tests).
	if p.pol.SourcePath != "" {
		if _, err := os.Stat(p.pol.SourcePath); err != nil {
			writeHealthDegraded(w, "policy file unreadable")
			return
		}
	}

	// Verify the database connection is still alive.
	if err := p.st.Ping(); err != nil {
		writeHealthDegraded(w, "database unreachable")
		return
	}

	resp := healthResponse{
		Status:            "ok",
		PolicyFingerprint: p.pol.ShortFingerprint(),
		PolicyFile:        p.pol.SourcePath,
		ProxyVersion:      proxyVersion,
		DBPath:            p.dbPath,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// writeHealthDegraded writes a 503 JSON response with a Reason field describing
// which component is unhealthy.
func writeHealthDegraded(w http.ResponseWriter, reason string) {
	resp := healthResponse{
		Status: "degraded",
		Reason: reason,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(resp)
}
