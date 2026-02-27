package store

import (
	"encoding/json"
	"fmt"
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
// Re-registering an agent (key rotation, policy update) is a clean overwrite with no error.
func (s *Store) UpsertAgent(a *Agent) error {
	const query = `
		INSERT OR REPLACE INTO agents
			(name, public_key_pem, policy_file, allowed_tools_json, registered_at, jwt_preview)
		VALUES (?, ?, ?, ?, ?, ?)`
	if _, err := s.db.Exec(query,
		a.Name, a.PublicKeyPEM, a.PolicyFile,
		a.AllowedToolsJSON, a.RegisteredAt, a.JWTPreview,
	); err != nil {
		return fmt.Errorf("upserting agent %q: %w", a.Name, err)
	}
	return nil
}

// GetAgent returns the agent row with the given name.
// Returns a wrapped sql.ErrNoRows if no agent with that name is registered.
func (s *Store) GetAgent(name string) (*Agent, error) {
	const query = `
		SELECT name, public_key_pem, policy_file, allowed_tools_json, registered_at, jwt_preview
		FROM agents
		WHERE name = ?`
	row := s.db.QueryRow(query, name)
	a := new(Agent)
	if err := row.Scan(
		&a.Name, &a.PublicKeyPEM, &a.PolicyFile,
		&a.AllowedToolsJSON, &a.RegisteredAt, &a.JWTPreview,
	); err != nil {
		return nil, fmt.Errorf("looking up agent %q: %w", name, err)
	}
	return a, nil
}

// ListAgents returns all agent rows ordered by registration time ascending.
func (s *Store) ListAgents() ([]*Agent, error) {
	const query = `
		SELECT name, public_key_pem, policy_file, allowed_tools_json, registered_at, jwt_preview
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
		if err := rows.Scan(
			&a.Name, &a.PublicKeyPEM, &a.PolicyFile,
			&a.AllowedToolsJSON, &a.RegisteredAt, &a.JWTPreview,
		); err != nil {
			return nil, fmt.Errorf("scanning agent row: %w", err)
		}
		agents = append(agents, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating agent rows: %w", err)
	}
	return agents, nil
}
