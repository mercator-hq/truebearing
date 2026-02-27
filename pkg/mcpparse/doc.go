// Package mcpparse owns parsing of MCP JSON-RPC 2.0 wire format messages.
//
// It has zero imports from internal/ — it is a pure protocol parser with no business logic.
//
// Invariant: ParseRequest must never panic on any input, including malformed or empty bytes.
// All error conditions are returned as errors, never as panics.
package mcpparse
