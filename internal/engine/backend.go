package engine

import (
	"errors"
	"time"
)

// SessionEventEntry is a minimal projection of a session event used by
// the sequence and rate-limit evaluators. It avoids a direct dependency on
// internal/store in this package, which would prevent the engine from
// compiling to WASM (modernc.org/sqlite is not WASM-compatible).
type SessionEventEntry struct {
	ToolName   string `json:"tool_name"`
	Decision   string `json:"decision"`
	RecordedAt int64  `json:"recorded_at"` // unix nanoseconds; used by rate-limit rolling-window checks
}

// ApprovedEscalationEntry records a prior human approval for a specific
// tool + argument-hash combination. The EscalationEvaluator uses it to
// skip re-escalation when a human has already approved an identical call.
type ApprovedEscalationEntry struct {
	ToolName string `json:"tool_name"`
	ArgsHash string `json:"args_hash"`
}

// ErrParentAgentNotFound is returned by QueryBackend.GetAgentAllowedTools
// when the named parent agent does not exist in the backing store.
var ErrParentAgentNotFound = errors.New("parent agent not found")

// QueryBackend is the minimal read-only data interface that the evaluation
// pipeline needs from the backing store. It is satisfied by:
//
//   - StoreBackend (wraps *store.Store; excluded from WASM builds via build tag)
//   - MemBackend (in-memory implementation; used by the WASM entry point and tests)
//
// Callers must treat all QueryBackend implementations as read-only: the
// pipeline invariants require that evaluators never write state.
type QueryBackend interface {
	// GetSessionEvents returns all events recorded for the session, ordered
	// by sequence number ascending. Only events with decision "allow" or
	// "shadow_deny" count toward sequence predicates; the caller is
	// responsible for filtering.
	GetSessionEvents(sessionID string) ([]SessionEventEntry, error)

	// CountSessionEventsSince returns the count of allowed events for the
	// named tool in the session that occurred at or after the given time.
	// "Allowed" means decision is "allow" or "shadow_deny"; denied calls
	// were blocked and must not consume rate-limit quota.
	CountSessionEventsSince(sessionID, toolName string, since time.Time) (int, error)

	// HasApprovedEscalation reports whether a human has previously approved
	// a call to toolName in the given session with the exact argsHash. The
	// hash is the hex-encoded SHA-256 of the raw argument JSON bytes.
	HasApprovedEscalation(sessionID, toolName, argsHash string) (bool, error)

	// GetAgentAllowedTools returns the list of tool names that the named
	// agent is permitted to call. Returns ErrParentAgentNotFound when the
	// agent does not exist. Used exclusively by DelegationEvaluator.
	GetAgentAllowedTools(agentName string) ([]string, error)
}
