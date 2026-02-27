package mcpparse_test

import (
	"encoding/json"
	"testing"

	"github.com/mercator-hq/truebearing/pkg/mcpparse"
)

func TestParseRequest(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		wantErr    bool
		wantMethod string
	}{
		{
			name: "valid tool call",
			input: `{
				"jsonrpc": "2.0",
				"id": "req_001",
				"method": "tools/call",
				"params": {"name": "some_tool", "arguments": {}}
			}`,
			wantErr:    false,
			wantMethod: "tools/call",
		},
		{
			name: "valid non-tool call",
			input: `{
				"jsonrpc": "2.0",
				"id": 1,
				"method": "tools/list",
				"params": {}
			}`,
			wantErr:    false,
			wantMethod: "tools/list",
		},
		{
			name: "valid initialize",
			input: `{
				"jsonrpc": "2.0",
				"id": null,
				"method": "initialize",
				"params": {}
			}`,
			wantErr:    false,
			wantMethod: "initialize",
		},
		{
			name:    "malformed JSON",
			input:   `{not valid json`,
			wantErr: true,
		},
		{
			name:    "empty input",
			input:   ``,
			wantErr: true,
		},
		{
			name:    "wrong jsonrpc version",
			input:   `{"jsonrpc": "1.0", "method": "tools/call"}`,
			wantErr: true,
		},
		{
			name:    "missing jsonrpc field",
			input:   `{"method": "tools/call", "id": "req_001"}`,
			wantErr: true,
		},
		{
			name:    "jsonrpc is null",
			input:   `{"jsonrpc": null, "method": "tools/call"}`,
			wantErr: true,
		},
		{
			name: "missing method is allowed — method is empty string",
			// method field missing: Go unmarshals to zero value "". Callers check IsTool.
			input:      `{"jsonrpc": "2.0", "id": "r1"}`,
			wantErr:    false,
			wantMethod: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := mcpparse.ParseRequest([]byte(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if r.Method != tc.wantMethod {
				t.Errorf("method: got %q, want %q", r.Method, tc.wantMethod)
			}
		})
	}
}

func TestIsTool(t *testing.T) {
	cases := []struct {
		name   string
		method string
		want   bool
	}{
		{"tools/call is a tool", "tools/call", true},
		{"tools/list is not a tool", "tools/list", false},
		{"initialize is not a tool", "initialize", false},
		{"empty method is not a tool", "", false},
		{"ping is not a tool", "ping", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &mcpparse.MCPRequest{Method: tc.method}
			got := mcpparse.IsTool(r)
			if got != tc.want {
				t.Errorf("IsTool(%q) = %v, want %v", tc.method, got, tc.want)
			}
		})
	}
}

func TestParseToolCallParams(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantErr  bool
		wantName string
		wantArgs string // expected arguments as JSON string
	}{
		{
			name:     "valid tool call params with arguments",
			raw:      `{"name": "execute_payment", "arguments": {"amount_usd": 1500.00}}`,
			wantErr:  false,
			wantName: "execute_payment",
			wantArgs: `{"amount_usd": 1500.00}`,
		},
		{
			name:     "valid tool call params with empty arguments",
			raw:      `{"name": "read_invoice", "arguments": {}}`,
			wantErr:  false,
			wantName: "read_invoice",
			wantArgs: `{}`,
		},
		{
			name:     "valid tool call params with null arguments",
			raw:      `{"name": "ping_tool", "arguments": null}`,
			wantErr:  false,
			wantName: "ping_tool",
		},
		{
			name:    "missing name field",
			raw:     `{"arguments": {"amount": 100}}`,
			wantErr: true,
		},
		{
			name:    "empty name field",
			raw:     `{"name": "", "arguments": {}}`,
			wantErr: true,
		},
		{
			name:    "malformed JSON",
			raw:     `not json at all`,
			wantErr: true,
		},
		{
			name:    "empty input",
			raw:     ``,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := mcpparse.ParseToolCallParams(json.RawMessage(tc.raw))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.Name != tc.wantName {
				t.Errorf("Name: got %q, want %q", p.Name, tc.wantName)
			}
		})
	}
}

// TestParseRequest_IDPreservation verifies that numeric, string, and null IDs
// are all preserved verbatim as raw JSON without normalisation.
func TestParseRequest_IDPreservation(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		wantID string
	}{
		{"string id", `{"jsonrpc":"2.0","id":"req_001","method":"tools/call"}`, `"req_001"`},
		{"numeric id", `{"jsonrpc":"2.0","id":42,"method":"tools/call"}`, `42`},
		{"null id", `{"jsonrpc":"2.0","id":null,"method":"tools/call"}`, `null`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := mcpparse.ParseRequest([]byte(tc.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(r.ID) != tc.wantID {
				t.Errorf("ID: got %s, want %s", r.ID, tc.wantID)
			}
		})
	}
}
