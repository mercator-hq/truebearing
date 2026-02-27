// Package proxy owns the HTTP listener, JWT authentication middleware, session ID middleware,
// and the reverse proxy that forwards allowed tool calls to the upstream MCP server.
//
// It does not own evaluation decisions (see package engine) or MCP wire format parsing
// (see package mcpparse).
//
// Invariant: no request reaches the engine pipeline without first passing JWT validation.
// A missing or invalid JWT always returns 401 before any other processing.
package proxy
