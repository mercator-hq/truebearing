package store_test

import (
	"testing"
	"time"

	"github.com/mercator-hq/truebearing/internal/store"
)

func TestUpsertAgent_Insert(t *testing.T) {
	db := store.NewTestDB(t)

	a := &store.Agent{
		Name:             "test-agent",
		PublicKeyPEM:     "-----BEGIN PUBLIC KEY-----\nMFIwEwYHKoZIzj0CAQYIKoZIzj0DAQcDOwAE\n-----END PUBLIC KEY-----\n",
		PolicyFile:       "./test.policy.yaml",
		AllowedToolsJSON: `["tool_a","tool_b"]`,
		RegisteredAt:     time.Now().UnixNano(),
		JWTPreview:       "eyJhbGciOiJFZERTQSJ9.eyJhZ2VudCI6InRlc3QifQ.sig",
	}

	if err := db.UpsertAgent(a); err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}

	agents, err := db.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("got %d agents, want 1", len(agents))
	}
	if agents[0].Name != a.Name {
		t.Errorf("Name: got %q, want %q", agents[0].Name, a.Name)
	}
	if agents[0].PolicyFile != a.PolicyFile {
		t.Errorf("PolicyFile: got %q, want %q", agents[0].PolicyFile, a.PolicyFile)
	}
	if agents[0].AllowedToolsJSON != a.AllowedToolsJSON {
		t.Errorf("AllowedToolsJSON: got %q, want %q", agents[0].AllowedToolsJSON, a.AllowedToolsJSON)
	}
	if agents[0].JWTPreview != a.JWTPreview {
		t.Errorf("JWTPreview: got %q, want %q", agents[0].JWTPreview, a.JWTPreview)
	}
}

func TestUpsertAgent_Overwrite(t *testing.T) {
	db := store.NewTestDB(t)

	a := &store.Agent{
		Name:             "agent-alpha",
		PublicKeyPEM:     "key1",
		PolicyFile:       "./old.policy.yaml",
		AllowedToolsJSON: `["tool_a"]`,
		RegisteredAt:     time.Now().UnixNano(),
		JWTPreview:       "jwt1",
	}
	if err := db.UpsertAgent(a); err != nil {
		t.Fatalf("initial UpsertAgent: %v", err)
	}

	// Re-register with updated fields — must overwrite cleanly without error.
	a.PolicyFile = "./new.policy.yaml"
	a.JWTPreview = "jwt2"
	if err := db.UpsertAgent(a); err != nil {
		t.Fatalf("re-register UpsertAgent: %v", err)
	}

	agents, err := db.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents after re-register: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("got %d agents after re-register, want 1", len(agents))
	}
	if agents[0].PolicyFile != "./new.policy.yaml" {
		t.Errorf("PolicyFile after re-register: got %q, want %q", agents[0].PolicyFile, "./new.policy.yaml")
	}
	if agents[0].JWTPreview != "jwt2" {
		t.Errorf("JWTPreview after re-register: got %q, want %q", agents[0].JWTPreview, "jwt2")
	}
}

func TestListAgents_Empty(t *testing.T) {
	db := store.NewTestDB(t)

	agents, err := db.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents on empty DB: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("got %d agents on empty DB, want 0", len(agents))
	}
}

func TestListAgents_OrderByRegisteredAt(t *testing.T) {
	db := store.NewTestDB(t)

	base := time.Now().UnixNano()
	// Insert in reverse order to confirm the query sorts correctly.
	for _, a := range []*store.Agent{
		{Name: "gamma", AllowedToolsJSON: "[]", RegisteredAt: base + 2000},
		{Name: "alpha", AllowedToolsJSON: "[]", RegisteredAt: base},
		{Name: "beta", AllowedToolsJSON: "[]", RegisteredAt: base + 1000},
	} {
		if err := db.UpsertAgent(a); err != nil {
			t.Fatalf("UpsertAgent %q: %v", a.Name, err)
		}
	}

	got, err := db.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d agents, want 3", len(got))
	}
	wantOrder := []string{"alpha", "beta", "gamma"}
	for i, name := range wantOrder {
		if got[i].Name != name {
			t.Errorf("agents[%d].Name: got %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestAgent_AllowedTools(t *testing.T) {
	cases := []struct {
		name      string
		json      string
		wantLen   int
		wantFirst string
		isErr     bool
	}{
		{"two tools", `["tool_x","tool_y"]`, 2, "tool_x", false},
		{"empty list", `[]`, 0, "", false},
		{"malformed JSON", `not-json`, 0, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &store.Agent{Name: "a", AllowedToolsJSON: tc.json}
			tools, err := a.AllowedTools()
			if tc.isErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(tools) != tc.wantLen {
				t.Errorf("len(tools): got %d, want %d", len(tools), tc.wantLen)
			}
			if tc.wantFirst != "" && (len(tools) == 0 || tools[0] != tc.wantFirst) {
				t.Errorf("tools[0]: got %q, want %q", tools[0], tc.wantFirst)
			}
		})
	}
}
