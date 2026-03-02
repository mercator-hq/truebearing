package identity

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AgentClaims is the JWT payload issued to a registered agent. It embeds the standard
// registered claims (expiry, issued-at, etc.) and adds TrueBearing-specific fields.
//
// The AgentName field ("agent") is required and must be non-empty — ValidateAgentJWT
// rejects any token where this field is absent or empty.
type AgentClaims struct {
	jwt.RegisteredClaims

	// AgentName identifies the agent. Matched against the agents table by the proxy
	// to look up the public key before calling ValidateAgentJWT.
	AgentName string `json:"agent"`

	// PolicyFile is the path to the policy YAML that was active at registration time.
	// It is informational; the proxy enforces the live policy file, not this path.
	PolicyFile string `json:"policy_file"`

	// AllowedTools is the list of tools from the policy's may_use at registration time.
	// Child-agent delegation enforcement is a set intersection: child.AllowedTools ⊆ parent.AllowedTools.
	AllowedTools []string `json:"allowed_tools"`

	// ParentAgent is the name of the agent that spawned this agent, or "" for root agents.
	ParentAgent string `json:"parent_agent"`

	// ParentAllowed is the parent's AllowedTools at the time of delegation. The proxy
	// verifies AllowedTools ⊆ ParentAllowed at request time without a database read.
	ParentAllowed []string `json:"parent_allowed"`

	// IssuedByProxy is the proxy instance ID that minted this token, for traceability.
	IssuedByProxy string `json:"issued_by"`

	// Env is the deployment environment for which this agent was registered
	// (e.g. "production", "staging"). When non-empty, the evaluation pipeline's
	// EnvEvaluator denies calls from this agent to any session whose policy
	// carries a require_env value that does not match. Set via `agent register --env`.
	Env string `json:"env,omitempty"`
}

// ErrMissingAgentClaim is returned by ValidateAgentJWT when the token does not carry
// a non-empty "agent" claim. A JWT without an agent identity cannot be authorised.
var ErrMissingAgentClaim = errors.New("JWT is missing required \"agent\" claim")

// MintAgentJWT creates and signs a JWT for the given AgentClaims using the provided
// Ed25519 private key. The token expires after expiry from the current time.
//
// The signing method is EdDSA (Ed25519), which is the only signing algorithm accepted
// by ValidateAgentJWT. This prevents algorithm confusion attacks.
func MintAgentJWT(claims AgentClaims, privateKey ed25519.PrivateKey, expiry time.Duration) (string, error) {
	now := time.Now()
	claims.RegisteredClaims = jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)

	signed, err := token.SignedString(privateKey)
	if err != nil {
		return "", fmt.Errorf("signing agent JWT for %q: %w", claims.AgentName, err)
	}

	return signed, nil
}

// ValidateAgentJWT parses tokenString, verifies the Ed25519 signature against publicKey,
// checks that the token is not expired, and asserts the "agent" claim is present.
//
// Design: this function takes an explicit publicKey rather than a keystore reference.
// Key lookup from the agents table is the caller's responsibility. Keeping validation
// pure makes it independently testable with no database dependency.
//
// The parser is locked to jwt.SigningMethodEdDSA to prevent algorithm-substitution
// attacks (e.g., an attacker supplying "alg":"none" or an HMAC variant).
func ValidateAgentJWT(tokenString string, publicKey ed25519.PublicKey) (*AgentClaims, error) {
	var claims AgentClaims

	token, err := jwt.ParseWithClaims(
		tokenString,
		&claims,
		func(t *jwt.Token) (interface{}, error) {
			// Reject any signing method other than EdDSA to prevent algorithm confusion.
			if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return publicKey, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		// Strict validation: reject tokens missing expiry or issued-at.
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("validating agent JWT: %w", err)
	}

	if !token.Valid {
		return nil, errors.New("agent JWT is not valid")
	}

	if claims.AgentName == "" {
		return nil, ErrMissingAgentClaim
	}

	return &claims, nil
}
