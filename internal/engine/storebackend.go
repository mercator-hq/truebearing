//go:build !wasip1 && !(js && wasm)

package engine

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mercator-hq/truebearing/internal/store"
)

// StoreBackend adapts *store.Store to the QueryBackend interface. It is
// excluded from WASM builds via the build tag so that internal/engine has
// no transitive dependency on modernc.org/sqlite when compiled to WASM.
// cmd/wasm uses MemBackend instead.
//
// The proxy creates one StoreBackend per evaluator at construction time
// (see internal/proxy/proxy.go) and passes it as the Store field.
type StoreBackend struct {
	Store *store.Store
}

// GetSessionEvents fetches all events for the session, converting the store's
// SessionEvent type to the engine's SessionEventEntry projection.
func (b *StoreBackend) GetSessionEvents(sessionID string) ([]SessionEventEntry, error) {
	rows, err := b.Store.GetSessionEvents(sessionID)
	if err != nil {
		return nil, fmt.Errorf("StoreBackend.GetSessionEvents for session %q: %w", sessionID, err)
	}
	entries := make([]SessionEventEntry, len(rows))
	for i, r := range rows {
		entries[i] = SessionEventEntry{
			ToolName:   r.ToolName,
			Decision:   r.Decision,
			RecordedAt: r.RecordedAt,
		}
	}
	return entries, nil
}

// CountSessionEventsSince delegates directly to the store's SQL-backed
// implementation which filters by time in the WHERE clause.
func (b *StoreBackend) CountSessionEventsSince(sessionID, toolName string, since time.Time) (int, error) {
	return b.Store.CountSessionEventsSince(sessionID, toolName, since)
}

// HasApprovedEscalation delegates directly to the store's SQL-backed lookup.
func (b *StoreBackend) HasApprovedEscalation(sessionID, toolName, argsHash string) (bool, error) {
	return b.Store.HasApprovedEscalation(sessionID, toolName, argsHash)
}

// GetAgentAllowedTools loads the parent agent from the store and decodes its
// allowed tool list. It maps sql.ErrNoRows to ErrParentAgentNotFound so that
// DelegationEvaluator does not need to import database/sql.
func (b *StoreBackend) GetAgentAllowedTools(agentName string) ([]string, error) {
	agent, err := b.Store.GetAgent(agentName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrParentAgentNotFound
		}
		return nil, fmt.Errorf("StoreBackend.GetAgentAllowedTools for agent %q: %w", agentName, err)
	}
	tools, err := agent.AllowedTools()
	if err != nil {
		return nil, fmt.Errorf("StoreBackend.GetAgentAllowedTools decoding tools for %q: %w", agentName, err)
	}
	return tools, nil
}
