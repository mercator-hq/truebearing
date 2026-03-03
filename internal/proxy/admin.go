package proxy

// Package proxy — admin.go owns the localhost-only administrative HTTP API.
// It exposes escalation list/approve/reject endpoints so human approvers who are
// not on the same machine as the operator can resolve escalations via HTTP rather
// than requiring CLI access. No JWT authentication is required; localhost-only
// binding is the sole access control for this API.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/mercator-hq/truebearing/internal/escalation"
	"github.com/mercator-hq/truebearing/internal/store"
)

// AdminHandler returns the HTTP handler for the localhost-only admin API.
// It exposes three endpoints:
//
//	GET  /admin/escalations?status=<filter>
//	POST /admin/escalations/{id}/approve  body: {"note":"..."}
//	POST /admin/escalations/{id}/reject   body: {"reason":"..."}
//
// Design: a separate AdminHandler (and separate port, default 7774) keeps the
// admin surface completely distinct from the MCP proxy surface. There is no code
// path where an MCP client accidentally reaches admin endpoints, and the operator
// can bind the admin port to a management VLAN or firewall it independently.
// The Go 1.22+ method-prefixed pattern syntax (e.g. "GET /path") is used so
// the mux enforces HTTP method constraints without extra middleware.
func (p *Proxy) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/escalations", p.handleAdminListEscalations)
	mux.HandleFunc("POST /admin/escalations/{id}/approve", p.handleAdminApproveEscalation)
	mux.HandleFunc("POST /admin/escalations/{id}/reject", p.handleAdminRejectEscalation)
	return mux
}

// adminEscalation is the JSON representation of a store.Escalation returned by
// the admin API. It uses snake_case field names for REST API consistency. The
// store.Escalation struct has no JSON tags (it is an internal DB type), so we
// project into this DTO rather than encoding the internal type directly.
type adminEscalation struct {
	ID            string `json:"id"`
	SessionID     string `json:"session_id"`
	Seq           uint64 `json:"seq"`
	ToolName      string `json:"tool_name"`
	ArgumentsJSON string `json:"arguments_json,omitempty"`
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
	CreatedAt     int64  `json:"created_at"`
	ResolvedAt    int64  `json:"resolved_at,omitempty"`
}

// toAdminEscalation converts an internal store.Escalation to the API DTO.
func toAdminEscalation(e store.Escalation) adminEscalation {
	return adminEscalation{
		ID:            e.ID,
		SessionID:     e.SessionID,
		Seq:           e.Seq,
		ToolName:      e.ToolName,
		ArgumentsJSON: e.ArgumentsJSON,
		Status:        e.Status,
		Reason:        e.Reason,
		CreatedAt:     e.CreatedAt,
		ResolvedAt:    e.ResolvedAt,
	}
}

// handleAdminListEscalations handles GET /admin/escalations.
// The optional ?status= query parameter filters by escalation lifecycle state
// (pending, approved, rejected). If absent, all escalations are returned ordered
// by created_at DESC.
func (p *Proxy) handleAdminListEscalations(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && status != "pending" && status != "approved" && status != "rejected" {
		writeAdminError(w, http.StatusBadRequest, "status must be pending, approved, or rejected")
		return
	}

	escs, err := p.st.ListEscalations(status)
	if err != nil {
		p.logger.ErrorContext(r.Context(), "admin: list escalations failed", "error", err)
		writeAdminError(w, http.StatusInternalServerError, "could not list escalations")
		return
	}

	out := make([]adminEscalation, len(escs))
	for i, e := range escs {
		out[i] = toAdminEscalation(e)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(out)
}

// handleAdminApproveEscalation handles POST /admin/escalations/{id}/approve.
// The optional JSON body may contain a "note" field recorded with the approval.
// After a successful approve, the agent's next check_escalation_status call will
// return "approved" and the pending tool call can be retried.
func (p *Proxy) handleAdminApproveEscalation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req struct {
		Note string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		writeAdminError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if err := escalation.Approve(id, req.Note, p.st); err != nil {
		p.writeAdminEscalationError(w, r, "approve", id, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(struct {
		EscalationID string `json:"escalation_id"`
		Status       string `json:"status"`
	}{EscalationID: id, Status: "approved"})
}

// handleAdminRejectEscalation handles POST /admin/escalations/{id}/reject.
// The optional JSON body may contain a "reason" field recorded with the rejection.
// After a successful reject, the agent's next check_escalation_status call will
// return "rejected".
func (p *Proxy) handleAdminRejectEscalation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		writeAdminError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if err := escalation.Reject(id, req.Reason, p.st); err != nil {
		p.writeAdminEscalationError(w, r, "reject", id, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(struct {
		EscalationID string `json:"escalation_id"`
		Status       string `json:"status"`
	}{EscalationID: id, Status: "rejected"})
}

// writeAdminEscalationError maps escalation operation errors to appropriate HTTP
// status codes and writes a JSON error response.
//
// Error mapping:
//   - sql.ErrNoRows (wrapped) → 404 Not Found
//   - "cannot be resolved" in message → 409 Conflict (already approved or rejected)
//   - anything else → 500 Internal Server Error
func (p *Proxy) writeAdminEscalationError(w http.ResponseWriter, r *http.Request, action, id string, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeAdminError(w, http.StatusNotFound, "escalation not found")
		return
	}
	if strings.Contains(err.Error(), "cannot be resolved") {
		writeAdminError(w, http.StatusConflict, err.Error())
		return
	}
	p.logger.ErrorContext(r.Context(), "admin: escalation operation failed",
		"action", action,
		"escalation_id", id,
		"error", err,
	)
	writeAdminError(w, http.StatusInternalServerError, "could not "+action+" escalation")
}

// writeAdminError writes a JSON error response body for admin API endpoints.
// The HTTP status is set before the body is written per http.ResponseWriter contract.
func writeAdminError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}{
		Error:   http.StatusText(code),
		Message: message,
	})
}
