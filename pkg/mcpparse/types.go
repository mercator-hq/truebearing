package mcpparse

import "encoding/json"

// MCPRequest is the parsed representation of an MCP JSON-RPC 2.0 request.
// ID and Params are kept as json.RawMessage so callers can decode them into
// concrete types without re-marshalling.
type MCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// IsTool reports whether the request is a tools/call invocation.
// Only tools/call requests are intercepted by the evaluation pipeline;
// all other methods (tools/list, initialize, ping, etc.) are forwarded directly.
func IsTool(r *MCPRequest) bool {
	return r.Method == "tools/call"
}

// ToolCallParams holds the name and raw arguments of a tools/call request.
// Arguments is kept as json.RawMessage so the escalation evaluator can
// extract values via JSONPath without a full unmarshal.
type ToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}
