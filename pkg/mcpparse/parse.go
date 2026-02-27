package mcpparse

import (
	"encoding/json"
	"fmt"
)

// ParseRequest unmarshals raw bytes into an MCPRequest and validates that the
// jsonrpc field is exactly "2.0". Any other value — including a missing field —
// is rejected so the proxy never processes non-JSON-RPC-2.0 traffic silently.
//
// This function never panics; all error conditions are returned as errors.
func ParseRequest(body []byte) (*MCPRequest, error) {
	var r MCPRequest
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parsing MCP request: %w", err)
	}
	if r.JSONRPC != "2.0" {
		return nil, fmt.Errorf("parsing MCP request: jsonrpc field must be \"2.0\", got %q", r.JSONRPC)
	}
	return &r, nil
}

// ParseToolCallParams decodes the params field of a tools/call MCPRequest into
// a ToolCallParams struct. Callers should only call this when IsTool returns true.
//
// Returns an error if the params JSON is malformed or the name field is missing.
func ParseToolCallParams(raw json.RawMessage) (*ToolCallParams, error) {
	var p ToolCallParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parsing tool call params: %w", err)
	}
	if p.Name == "" {
		return nil, fmt.Errorf("parsing tool call params: name field is required")
	}
	return &p, nil
}
