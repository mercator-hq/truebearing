package engine

import (
	"fmt"
	"time"
)

// MemBackend is an in-memory implementation of QueryBackend. It is populated
// from pre-loaded state and is the backend used by the WASM entry point
// (cmd/wasm) and optionally in tests that do not require a real SQLite store.
//
// All methods ignore the sessionID parameter: a MemBackend instance is scoped
// to a single evaluation call, so all events belong to the same session by
// construction.
//
// Invariant: MemBackend is read-only after construction. NewMemBackend copies
// the slices it receives, so the caller may safely mutate its own slices after
// calling NewMemBackend.
type MemBackend struct {
	events              []SessionEventEntry
	approvedEscalations []ApprovedEscalationEntry
	parentTools         []string
}

// NewMemBackend constructs a MemBackend from pre-loaded state. It copies the
// input slices so the caller's memory is not aliased by the backend.
func NewMemBackend(
	events []SessionEventEntry,
	approved []ApprovedEscalationEntry,
	parentTools []string,
) *MemBackend {
	e := make([]SessionEventEntry, len(events))
	copy(e, events)
	a := make([]ApprovedEscalationEntry, len(approved))
	copy(a, approved)
	p := make([]string, len(parentTools))
	copy(p, parentTools)
	return &MemBackend{
		events:              e,
		approvedEscalations: a,
		parentTools:         p,
	}
}

// GetSessionEvents returns all pre-loaded events. The sessionID argument is
// ignored because a MemBackend is scoped to a single session by design.
func (m *MemBackend) GetSessionEvents(_ string) ([]SessionEventEntry, error) {
	return m.events, nil
}

// CountSessionEventsSince counts events matching toolName with an allowed
// decision (allow or shadow_deny) whose RecordedAt timestamp is at or after
// the since time. The sessionID argument is ignored (see MemBackend comment).
//
// Design: RecordedAt is stored as unix nanoseconds so the comparison is a
// straightforward integer comparison after converting since to nanoseconds.
func (m *MemBackend) CountSessionEventsSince(_ string, toolName string, since time.Time) (int, error) {
	sinceNano := since.UnixNano()
	count := 0
	for _, ev := range m.events {
		if ev.ToolName != toolName {
			continue
		}
		if ev.Decision != string(Allow) && ev.Decision != string(ShadowDeny) {
			continue
		}
		if ev.RecordedAt >= sinceNano {
			count++
		}
	}
	return count, nil
}

// HasApprovedEscalation reports whether any entry in the pre-loaded
// ApprovedEscalations slice matches the given toolName and argsHash. The
// sessionID argument is ignored (see MemBackend comment).
func (m *MemBackend) HasApprovedEscalation(_ string, toolName, argsHash string) (bool, error) {
	for _, e := range m.approvedEscalations {
		if e.ToolName == toolName && e.ArgsHash == argsHash {
			return true, nil
		}
	}
	return false, nil
}

// GetAgentAllowedTools returns the pre-loaded parent tool list. The agentName
// argument is ignored because a MemBackend carries exactly one parent's tool
// set — the caller is responsible for passing the correct set at construction
// time. Returns ErrParentAgentNotFound when no parent tools were provided,
// which causes DelegationEvaluator to deny the call with a clear error.
func (m *MemBackend) GetAgentAllowedTools(_ string) ([]string, error) {
	if len(m.parentTools) == 0 {
		return nil, fmt.Errorf("%w", ErrParentAgentNotFound)
	}
	return m.parentTools, nil
}
