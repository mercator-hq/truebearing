package proxy

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/mercator-hq/truebearing/internal/identity"
	"github.com/mercator-hq/truebearing/internal/store"
)

// contextKey is an unexported type for context keys owned by the proxy package.
// Using a typed int prevents key collisions with context values set by other packages.
type contextKey int

const claimsKey contextKey = iota

// AgentClaimsFromContext retrieves the *identity.AgentClaims stored by AuthMiddleware
// in the request context. Returns (claims, true) if the middleware ran successfully,
// (nil, false) if the value is absent (i.e., the request did not pass auth).
func AgentClaimsFromContext(ctx context.Context) (*identity.AgentClaims, bool) {
	claims, ok := ctx.Value(claimsKey).(*identity.AgentClaims)
	return claims, ok
}

// AuthMiddleware returns an HTTP middleware that enforces JWT authentication on every
// request. It reads the Authorization: Bearer token, resolves the agent's public key
// from the store, validates the Ed25519 signature and expiry, and stores the verified
// *identity.AgentClaims in the request context for downstream handlers.
//
// Per CLAUDE.md §8: No JWT = 401, always. There is no bypass flag or override.
func AuthMiddleware(st *store.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenString, err := bearerToken(r)
			if err != nil {
				writeUnauthorized(w, err.Error())
				return
			}

			// Decode the payload without verifying the signature to extract the agent
			// name. We need the name to look up the correct public key before we can
			// verify. See Design comment on unverifiedAgentClaim for why this is safe.
			agentName, err := unverifiedAgentClaim(tokenString)
			if err != nil {
				writeUnauthorized(w, "malformed token")
				return
			}
			if agentName == "" {
				writeUnauthorized(w, "token missing required agent claim")
				return
			}

			agent, err := st.GetAgent(agentName)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeUnauthorized(w, "agent not registered")
				} else {
					writeUnauthorized(w, "agent lookup failed")
				}
				return
			}

			pubKey, err := parsePublicKeyPEM(agent.PublicKeyPEM)
			if err != nil {
				// The stored PEM is corrupt — fail closed rather than defaulting to allow.
				writeUnauthorized(w, "invalid agent credentials")
				return
			}

			claims, err := identity.ValidateAgentJWT(tokenString, pubKey)
			if err != nil {
				writeUnauthorized(w, "invalid or expired token")
				return
			}

			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// bearerToken extracts the raw JWT string from the Authorization: Bearer <token> header.
// Returns an error if the header is absent, uses a non-Bearer scheme, or is empty.
func bearerToken(r *http.Request) (string, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", fmt.Errorf("missing Authorization header")
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return "", fmt.Errorf("Authorization header must use Bearer scheme")
	}
	token := strings.TrimPrefix(auth, prefix)
	if token == "" {
		return "", fmt.Errorf("empty Bearer token")
	}
	return token, nil
}

// unverifiedAgentClaim decodes the JWT payload segment without verifying the signature
// and returns the value of the "agent" claim.
//
// Design: we need the agent name to look up the public key required for signature
// verification. The unverified decode is safe here because full cryptographic verification
// (signature + expiry) immediately follows using the retrieved key. An attacker who
// supplies a fake agent name receives either "agent not registered" or a signature
// verification failure — there is no path that skips the cryptographic check.
func unverifiedAgentClaim(tokenString string) (string, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("JWT must have exactly three dot-separated segments")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decoding JWT payload segment: %w", err)
	}
	var claims struct {
		Agent string `json:"agent"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("parsing JWT payload JSON: %w", err)
	}
	return claims.Agent, nil
}

// parsePublicKeyPEM decodes a PKIX PEM-encoded Ed25519 public key stored in the agents
// table. This is the in-memory equivalent of identity.LoadPublicKey; it operates on a
// string rather than a file path.
func parsePublicKeyPEM(pemData string) (ed25519.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in agent public key")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing PKIX public key: %w", err)
	}
	pubKey, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("agent key is not an Ed25519 public key")
	}
	return pubKey, nil
}

// writeUnauthorized writes a 401 JSON response. The message parameter must be safe to
// expose to callers — it must not contain key material, JWT contents, or internal error
// detail. Surface only what the caller needs to understand the failure category.
func writeUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	body, _ := json.Marshal(struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}{
		Error:   "unauthorized",
		Message: message,
	})
	_, _ = w.Write(body)
}
