package identity_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/mercator-hq/truebearing/internal/identity"
)

// generateTestKeypair generates a fresh Ed25519 keypair for use in tests.
// It fails the test immediately on any error.
func generateTestKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating test keypair: %v", err)
	}
	return pub, priv
}

// baseClaims returns a fully populated AgentClaims suitable for most test cases.
func baseClaims() identity.AgentClaims {
	return identity.AgentClaims{
		AgentName:     "test-agent",
		PolicyFile:    "./policy.yaml",
		AllowedTools:  []string{"read_data", "write_data"},
		IssuedByProxy: "proxy-test-instance",
	}
}

// TestMintAndValidate_RoundTrip verifies that a token minted with MintAgentJWT can be
// parsed back by ValidateAgentJWT with all claim fields intact.
func TestMintAndValidate_RoundTrip(t *testing.T) {
	pub, priv := generateTestKeypair(t)

	claims := baseClaims()
	claims.AllowedTools = []string{"tool_a", "tool_b"}
	claims.ParentAgent = "parent-agent"
	claims.ParentAllowed = []string{"tool_a", "tool_b", "tool_c"}

	token, err := identity.MintAgentJWT(claims, priv, time.Hour)
	if err != nil {
		t.Fatalf("MintAgentJWT: %v", err)
	}

	got, err := identity.ValidateAgentJWT(token, pub)
	if err != nil {
		t.Fatalf("ValidateAgentJWT: %v", err)
	}

	if got.AgentName != claims.AgentName {
		t.Errorf("AgentName: got %q, want %q", got.AgentName, claims.AgentName)
	}
	if got.PolicyFile != claims.PolicyFile {
		t.Errorf("PolicyFile: got %q, want %q", got.PolicyFile, claims.PolicyFile)
	}
	if got.IssuedByProxy != claims.IssuedByProxy {
		t.Errorf("IssuedByProxy: got %q, want %q", got.IssuedByProxy, claims.IssuedByProxy)
	}
	if got.ParentAgent != claims.ParentAgent {
		t.Errorf("ParentAgent: got %q, want %q", got.ParentAgent, claims.ParentAgent)
	}
	if len(got.AllowedTools) != len(claims.AllowedTools) {
		t.Errorf("AllowedTools length: got %d, want %d", len(got.AllowedTools), len(claims.AllowedTools))
	}
	if len(got.ParentAllowed) != len(claims.ParentAllowed) {
		t.Errorf("ParentAllowed length: got %d, want %d", len(got.ParentAllowed), len(claims.ParentAllowed))
	}

	// Registered claims must be populated by MintAgentJWT.
	if got.IssuedAt == nil || got.IssuedAt.IsZero() {
		t.Error("IssuedAt is nil or zero — MintAgentJWT must set registered claims")
	}
	if got.ExpiresAt == nil || got.ExpiresAt.IsZero() {
		t.Error("ExpiresAt is nil or zero — MintAgentJWT must set registered claims")
	}
}

// TestValidate_ExpiredToken confirms that a token with a past expiry is rejected.
func TestValidate_ExpiredToken(t *testing.T) {
	pub, priv := generateTestKeypair(t)

	// Mint a token that expired 1 second ago.
	token, err := identity.MintAgentJWT(baseClaims(), priv, -time.Second)
	if err != nil {
		t.Fatalf("MintAgentJWT: %v", err)
	}

	_, err = identity.ValidateAgentJWT(token, pub)
	if err == nil {
		t.Error("expected error for expired token, got nil")
	}
}

// TestValidate_TamperedSignature confirms that flipping one byte in the signature
// segment causes ValidateAgentJWT to return an error. This guards against signature
// bypass bugs in the JWT library's integration.
func TestValidate_TamperedSignature(t *testing.T) {
	pub, priv := generateTestKeypair(t)

	token, err := identity.MintAgentJWT(baseClaims(), priv, time.Hour)
	if err != nil {
		t.Fatalf("MintAgentJWT: %v", err)
	}

	// JWT is header.payload.signature — the last segment is the base64url signature.
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("unexpected JWT structure: %d parts", len(parts))
	}

	// Flip the first character of the signature segment to corrupt it.
	// We change the first byte to a different character in the base64url alphabet.
	sig := []byte(parts[2])
	if sig[0] == 'A' {
		sig[0] = 'B'
	} else {
		sig[0] = 'A'
	}
	tampered := strings.Join([]string{parts[0], parts[1], string(sig)}, ".")

	_, err = identity.ValidateAgentJWT(tampered, pub)
	if err == nil {
		t.Error("expected error for tampered-signature token, got nil")
	}
}

// TestValidate_WrongKey confirms that a token signed by one private key is rejected
// when validated against a different public key.
func TestValidate_WrongKey(t *testing.T) {
	_, priv1 := generateTestKeypair(t)
	pub2, _ := generateTestKeypair(t)

	token, err := identity.MintAgentJWT(baseClaims(), priv1, time.Hour)
	if err != nil {
		t.Fatalf("MintAgentJWT: %v", err)
	}

	_, err = identity.ValidateAgentJWT(token, pub2)
	if err == nil {
		t.Error("expected error validating token against wrong public key, got nil")
	}
}

// TestValidate_MissingAgentClaim confirms that a token with an empty AgentName is
// rejected with ErrMissingAgentClaim. The "agent" claim is required for the proxy
// to identify which agent made the request.
func TestValidate_MissingAgentClaim(t *testing.T) {
	pub, priv := generateTestKeypair(t)

	claims := baseClaims()
	claims.AgentName = "" // strip the required field

	token, err := identity.MintAgentJWT(claims, priv, time.Hour)
	if err != nil {
		t.Fatalf("MintAgentJWT: %v", err)
	}

	_, err = identity.ValidateAgentJWT(token, pub)
	if err == nil {
		t.Error("expected error for token missing agent claim, got nil")
	}
}

// TestMintAgentJWT_SetsExpiry confirms that the ExpiresAt claim reflects the
// requested expiry duration relative to the current time.
//
// JWT NumericDate is second-precision (RFC 7519 §2), so bounds are truncated to the
// second before comparison to avoid spurious failures from sub-second timing.
func TestMintAgentJWT_SetsExpiry(t *testing.T) {
	pub, priv := generateTestKeypair(t)

	// Truncate to second because jwt.NewNumericDate truncates to second precision.
	before := time.Now().Truncate(time.Second)
	token, err := identity.MintAgentJWT(baseClaims(), priv, 2*time.Hour)
	if err != nil {
		t.Fatalf("MintAgentJWT: %v", err)
	}
	after := time.Now().Truncate(time.Second)

	got, err := identity.ValidateAgentJWT(token, pub)
	if err != nil {
		t.Fatalf("ValidateAgentJWT: %v", err)
	}

	expiry := got.ExpiresAt.Time
	lowerBound := before.Add(2 * time.Hour)
	upperBound := after.Add(2 * time.Hour)

	if expiry.Before(lowerBound) || expiry.After(upperBound) {
		t.Errorf("ExpiresAt %v is outside expected range [%v, %v]", expiry, lowerBound, upperBound)
	}
}
