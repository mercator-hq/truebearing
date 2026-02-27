package proxy

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mercator-hq/truebearing/internal/identity"
	"github.com/mercator-hq/truebearing/internal/store"
)

// registerTestAgent generates an Ed25519 keypair, registers the agent in st, and
// returns the keypair. It fails the test immediately on any setup error.
func registerTestAgent(t *testing.T, st *store.Store, name string) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating test keypair: %v", err)
	}

	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshalling public key to PKIX: %v", err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))

	if err := st.UpsertAgent(&store.Agent{
		Name:             name,
		PublicKeyPEM:     pubPEM,
		PolicyFile:       "./test.policy.yaml",
		AllowedToolsJSON: `["test_tool"]`,
		RegisteredAt:     0,
		JWTPreview:       "",
	}); err != nil {
		t.Fatalf("upserting test agent %q: %v", name, err)
	}

	return pub, priv
}

// mintTestToken mints a JWT for the given agent name using priv. The expiry controls
// whether the token is valid or already expired.
func mintTestToken(t *testing.T, priv ed25519.PrivateKey, agentName string, expiry time.Duration) string {
	t.Helper()
	token, err := identity.MintAgentJWT(identity.AgentClaims{
		AgentName:    agentName,
		AllowedTools: []string{"test_tool"},
	}, priv, expiry)
	if err != nil {
		t.Fatalf("minting test JWT for %q: %v", agentName, err)
	}
	return token
}

// runMiddleware invokes AuthMiddleware with a handler that records whether it was reached
// and captures the AgentClaims from context if present.
func runMiddleware(t *testing.T, st *store.Store, r *http.Request) (statusCode int, body string, claims *identity.AgentClaims) {
	t.Helper()
	var capturedClaims *identity.AgentClaims
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if c, ok := AgentClaimsFromContext(req.Context()); ok {
			capturedClaims = c
		}
		w.WriteHeader(http.StatusOK)
	})
	rr := httptest.NewRecorder()
	AuthMiddleware(st)(handler).ServeHTTP(rr, r)
	return rr.Code, rr.Body.String(), capturedClaims
}

// --- AuthMiddleware integration tests ---

func TestAuthMiddleware_MissingAuthorizationHeader(t *testing.T) {
	st := store.NewTestDB(t)
	r := httptest.NewRequest(http.MethodPost, "/mcp/v1", nil)

	code, body, _ := runMiddleware(t, st, r)

	if code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d", code)
	}
	if !strings.Contains(body, `"unauthorized"`) {
		t.Errorf("body must contain error=unauthorized, got %q", body)
	}
}

func TestAuthMiddleware_NonBearerScheme(t *testing.T) {
	st := store.NewTestDB(t)
	r := httptest.NewRequest(http.MethodPost, "/mcp/v1", nil)
	r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

	code, _, _ := runMiddleware(t, st, r)

	if code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d", code)
	}
}

func TestAuthMiddleware_EmptyBearerToken(t *testing.T) {
	st := store.NewTestDB(t)
	r := httptest.NewRequest(http.MethodPost, "/mcp/v1", nil)
	r.Header.Set("Authorization", "Bearer ")

	code, _, _ := runMiddleware(t, st, r)

	if code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d", code)
	}
}

func TestAuthMiddleware_MalformedToken(t *testing.T) {
	st := store.NewTestDB(t)
	r := httptest.NewRequest(http.MethodPost, "/mcp/v1", nil)
	r.Header.Set("Authorization", "Bearer notavalidjwt")

	code, _, _ := runMiddleware(t, st, r)

	if code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d", code)
	}
}

func TestAuthMiddleware_AgentNotInDB(t *testing.T) {
	st := store.NewTestDB(t)

	// Generate a keypair but do NOT register the agent in the store.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating keypair: %v", err)
	}
	token := mintTestToken(t, priv, "ghost-agent", time.Hour)

	r := httptest.NewRequest(http.MethodPost, "/mcp/v1", nil)
	r.Header.Set("Authorization", "Bearer "+token)

	code, body, _ := runMiddleware(t, st, r)

	if code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d", code)
	}
	if !strings.Contains(body, "agent not registered") {
		t.Errorf("body must mention agent not registered, got %q", body)
	}
}

func TestAuthMiddleware_InvalidSignature(t *testing.T) {
	st := store.NewTestDB(t)
	_, priv := registerTestAgent(t, st, "test-agent")
	token := mintTestToken(t, priv, "test-agent", time.Hour)

	// Corrupt the signature segment (third dot-separated part).
	parts := strings.Split(token, ".")
	sig := []byte(parts[2])
	if sig[0] == 'A' {
		sig[0] = 'B'
	} else {
		sig[0] = 'A'
	}
	tampered := strings.Join([]string{parts[0], parts[1], string(sig)}, ".")

	r := httptest.NewRequest(http.MethodPost, "/mcp/v1", nil)
	r.Header.Set("Authorization", "Bearer "+tampered)

	code, _, _ := runMiddleware(t, st, r)

	if code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d", code)
	}
}

func TestAuthMiddleware_ExpiredToken(t *testing.T) {
	st := store.NewTestDB(t)
	_, priv := registerTestAgent(t, st, "test-agent")
	// Mint a token that expired 1 second ago.
	token := mintTestToken(t, priv, "test-agent", -time.Second)

	r := httptest.NewRequest(http.MethodPost, "/mcp/v1", nil)
	r.Header.Set("Authorization", "Bearer "+token)

	code, _, _ := runMiddleware(t, st, r)

	if code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d", code)
	}
}

func TestAuthMiddleware_ValidToken_ClaimsInContext(t *testing.T) {
	st := store.NewTestDB(t)
	_, priv := registerTestAgent(t, st, "test-agent")
	token := mintTestToken(t, priv, "test-agent", time.Hour)

	r := httptest.NewRequest(http.MethodPost, "/mcp/v1", nil)
	r.Header.Set("Authorization", "Bearer "+token)

	code, _, claims := runMiddleware(t, st, r)

	if code != http.StatusOK {
		t.Errorf("status: want 200, got %d", code)
	}
	if claims == nil {
		t.Fatal("AgentClaims must be present in context after successful auth")
	}
	if claims.AgentName != "test-agent" {
		t.Errorf("AgentName: want %q, got %q", "test-agent", claims.AgentName)
	}
}

func TestAuthMiddleware_ValidToken_NextHandlerReached(t *testing.T) {
	st := store.NewTestDB(t)
	_, priv := registerTestAgent(t, st, "test-agent")
	token := mintTestToken(t, priv, "test-agent", time.Hour)

	reached := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	r := httptest.NewRequest(http.MethodPost, "/mcp/v1", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	AuthMiddleware(st)(handler).ServeHTTP(rr, r)

	if !reached {
		t.Error("next handler must be called for a valid token")
	}
}

func TestAuthMiddleware_ResponseBody_Format(t *testing.T) {
	st := store.NewTestDB(t)
	r := httptest.NewRequest(http.MethodPost, "/mcp/v1", nil)

	rr := httptest.NewRecorder()
	AuthMiddleware(st)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rr, r)

	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: want application/json, got %q", ct)
	}
	var resp struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parsing 401 response body: %v", err)
	}
	if resp.Error != "unauthorized" {
		t.Errorf("error field: want %q, got %q", "unauthorized", resp.Error)
	}
	if resp.Message == "" {
		t.Error("message field must not be empty in 401 response")
	}
}

// --- Unit tests for unexported helpers ---

func TestBearerToken(t *testing.T) {
	cases := []struct {
		name       string
		authHeader string
		wantToken  string
		wantErr    bool
	}{
		{
			name:       "valid bearer token",
			authHeader: "Bearer mytoken123",
			wantToken:  "mytoken123",
		},
		{
			name:    "missing header",
			wantErr: true,
		},
		{
			name:       "basic auth scheme",
			authHeader: "Basic dXNlcjpwYXNz",
			wantErr:    true,
		},
		{
			name:       "bearer with empty token",
			authHeader: "Bearer ",
			wantErr:    true,
		},
		{
			name:       "lowercase bearer (scheme is case-sensitive per RFC 7235)",
			authHeader: "bearer mytoken",
			wantErr:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.authHeader != "" {
				r.Header.Set("Authorization", tc.authHeader)
			}
			got, err := bearerToken(r)
			if (err != nil) != tc.wantErr {
				t.Errorf("bearerToken() error = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && got != tc.wantToken {
				t.Errorf("bearerToken() = %q, want %q", got, tc.wantToken)
			}
		})
	}
}

func TestUnverifiedAgentClaim(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating keypair: %v", err)
	}
	validToken, err := identity.MintAgentJWT(identity.AgentClaims{AgentName: "my-agent"}, priv, time.Hour)
	if err != nil {
		t.Fatalf("MintAgentJWT: %v", err)
	}

	cases := []struct {
		name      string
		token     string
		wantAgent string
		wantErr   bool
	}{
		{
			name:      "valid token returns agent name",
			token:     validToken,
			wantAgent: "my-agent",
		},
		{
			name:    "not a JWT string",
			token:   "notavalidjwt",
			wantErr: true,
		},
		{
			name:    "only two segments",
			token:   "aaa.bbb",
			wantErr: true,
		},
		{
			name:    "invalid base64 in payload segment",
			token:   "aaa.!!!.ccc",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := unverifiedAgentClaim(tc.token)
			if (err != nil) != tc.wantErr {
				t.Errorf("unverifiedAgentClaim() error = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && got != tc.wantAgent {
				t.Errorf("unverifiedAgentClaim() = %q, want %q", got, tc.wantAgent)
			}
		})
	}
}

func TestParsePublicKeyPEM(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating keypair: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshalling public key: %v", err)
	}
	validPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))

	cases := []struct {
		name    string
		pemData string
		wantErr bool
	}{
		{
			name:    "valid Ed25519 public key PEM",
			pemData: validPEM,
		},
		{
			name:    "empty string",
			pemData: "",
			wantErr: true,
		},
		{
			name:    "garbage data",
			pemData: "not pem data at all",
			wantErr: true,
		},
		{
			name:    "PEM block with invalid DER content",
			pemData: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("notvalidder")})),
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePublicKeyPEM(tc.pemData)
			if (err != nil) != tc.wantErr {
				t.Errorf("parsePublicKeyPEM() error = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && got == nil {
				t.Error("parsePublicKeyPEM() returned nil key without error")
			}
		})
	}
}
