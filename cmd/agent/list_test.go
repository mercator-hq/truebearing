package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/mercator-hq/truebearing/internal/identity"
)

func TestJWTExpiry_Valid(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}

	const expiry = 24 * time.Hour
	token, err := identity.MintAgentJWT(
		identity.AgentClaims{AgentName: "list-test-agent"},
		priv,
		expiry,
	)
	if err != nil {
		t.Fatalf("minting JWT: %v", err)
	}

	got, ok := jwtExpiry(token)
	if !ok {
		t.Fatal("jwtExpiry returned false for a valid token")
	}

	// Expiry should be now + 24h within 5 seconds of timing tolerance.
	want := time.Now().Add(expiry)
	diff := got.Sub(want)
	if diff < -5*time.Second || diff > 5*time.Second {
		t.Errorf("jwtExpiry: got %v, want ~%v (diff %v)", got, want, diff)
	}
}

func TestJWTExpiry_Invalid(t *testing.T) {
	// A JSON payload with no exp field, base64url-encoded without padding.
	noExpPayload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"test"}`))

	cases := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"two segments only", "header.payload"},
		{"five segments", "a.b.c.d.e"},
		{"bad base64 in payload", "header.!!!.sig"},
		{"non-JSON payload", "header." + base64.RawURLEncoding.EncodeToString([]byte(`{not json}`)) + ".sig"},
		{"no exp claim", "header." + noExpPayload + ".sig"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := jwtExpiry(tc.input)
			if ok {
				t.Errorf("jwtExpiry(%q): expected false, got true", tc.input)
			}
		})
	}
}
