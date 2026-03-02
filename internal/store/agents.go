package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Agent represents a registered agent entry in the agents table.
type Agent struct {
	// Name is the agent's unique identifier and primary key.
	Name string

	// PublicKeyPEM is the PKIX PEM-encoded Ed25519 public key. The auth middleware
	// uses this to verify the agent's JWT signature on every proxy request.
	PublicKeyPEM string

	// PolicyFile is the path to the policy YAML that was active at registration time.
	// It is informational; the proxy loads the policy independently on startup.
	PolicyFile string

	// AllowedToolsJSON is a JSON array of tool names extracted from the policy's
	// may_use at registration time. Use AllowedTools() to decode into a slice.
	AllowedToolsJSON string

	// RegisteredAt is the registration timestamp in unix nanoseconds.
	RegisteredAt int64

	// JWTPreview holds the full issued JWT text.
	//
	// Design: the schema comment ("first 32 chars for display") is intentionally
	// overridden here. Storing the full JWT allows agent list to decode and display
	// the token's expiry without adding a separate schema column. JWTs are not
	// secrets — they are intended to be shared as Authorization: Bearer tokens.
	// The agents table column accepts arbitrary TEXT with no length constraint.
	JWTPreview string

	// RevokedAt is nil while the agent is active. When an operator runs
	// `truebearing agent revoke`, it is set to the revocation timestamp (unix
	// nanoseconds). The auth middleware checks this field on every request: a
	// non-nil value causes immediate 401 rejection, even for JWTs that are
	// cryptographically valid. Re-registering the agent (UpsertAgent) clears the
	// revocation by writing NULL.
	RevokedAt *int64
}

// IsRevoked returns true when the agent has been revoked.
func (a *Agent) IsRevoked() bool {
	return a.RevokedAt != nil
}

// AllowedTools decodes AllowedToolsJSON into a string slice.
func (a *Agent) AllowedTools() ([]string, error) {
	var tools []string
	if err := json.Unmarshal([]byte(a.AllowedToolsJSON), &tools); err != nil {
		return nil, fmt.Errorf("decoding allowed_tools_json for agent %q: %w", a.Name, err)
	}
	return tools, nil
}

// UpsertAgent inserts a new agent row or replaces an existing one with the same name.
// Re-registering an agent (key rotation, policy update) is a clean overwrite that also
// clears any prior revocation — the new credentials are considered fresh and active.
func (s *Store) UpsertAgent(a *Agent) error {
	const query = `
		INSERT OR REPLACE INTO agents
			(name, public_key_pem, policy_file, allowed_tools_json, registered_at, jwt_preview, revoked_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL)`
	if _, err := s.db.Exec(query,
		a.Name, a.PublicKeyPEM, a.PolicyFile,
		a.AllowedToolsJSON, a.RegisteredAt, a.JWTPreview,
	); err != nil {
		return fmt.Errorf("upserting agent %q: %w", a.Name, err)
	}
	return nil
}

// RevokeAgent sets revoked_at to the current time for the named agent, permanently
// blocking its JWT from authenticating at the proxy. Returns an error if no agent
// with that name exists.
func (s *Store) RevokeAgent(name string) error {
	result, err := s.db.Exec(
		"UPDATE agents SET revoked_at = ? WHERE name = ?",
		time.Now().UnixNano(), name,
	)
	if err != nil {
		return fmt.Errorf("revoking agent %q: %w", name, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected for revoke of %q: %w", name, err)
	}
	if n == 0 {
		return fmt.Errorf("agent %q not found", name)
	}
	return nil
}

// GetAgent returns the agent row with the given name.
// Returns a wrapped sql.ErrNoRows if no agent with that name is registered.
func (s *Store) GetAgent(name string) (*Agent, error) {
	const query = `
		SELECT name, public_key_pem, policy_file, allowed_tools_json, registered_at, jwt_preview, revoked_at
		FROM agents
		WHERE name = ?`
	row := s.db.QueryRow(query, name)
	a := new(Agent)
	var revokedAt sql.NullInt64
	if err := row.Scan(
		&a.Name, &a.PublicKeyPEM, &a.PolicyFile,
		&a.AllowedToolsJSON, &a.RegisteredAt, &a.JWTPreview, &revokedAt,
	); err != nil {
		return nil, fmt.Errorf("looking up agent %q: %w", name, err)
	}
	if revokedAt.Valid {
		a.RevokedAt = &revokedAt.Int64
	}
	return a, nil
}

// ListAgents returns all agent rows ordered by registration time ascending.
func (s *Store) ListAgents() ([]*Agent, error) {
	const query = `
		SELECT name, public_key_pem, policy_file, allowed_tools_json, registered_at, jwt_preview, revoked_at
		FROM agents
		ORDER BY registered_at ASC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("querying agents table: %w", err)
	}
	defer rows.Close()

	var agents []*Agent
	for rows.Next() {
		a := new(Agent)
		var revokedAt sql.NullInt64
		if err := rows.Scan(
			&a.Name, &a.PublicKeyPEM, &a.PolicyFile,
			&a.AllowedToolsJSON, &a.RegisteredAt, &a.JWTPreview, &revokedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning agent row: %w", err)
		}
		if revokedAt.Valid {
			a.RevokedAt = &revokedAt.Int64
		}
		agents = append(agents, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating agent rows: %w", err)
	}
	return agents, nil
}
