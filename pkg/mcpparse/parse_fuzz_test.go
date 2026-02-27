package mcpparse_test

import (
	"testing"

	"github.com/mercator-hq/truebearing/pkg/mcpparse"
)

// FuzzParseRequest ensures ParseRequest never panics on arbitrary input.
// Any panic is a bug — the function must always return (nil, err) on bad input.
//
// Run with: go test -fuzz=FuzzParseRequest ./pkg/mcpparse/...
func FuzzParseRequest(f *testing.F) {
	// Seed corpus: known valid and known-invalid inputs to give the fuzzer
	// a useful starting point.
	f.Add([]byte(`{"jsonrpc":"2.0","id":"req_001","method":"tools/call","params":{"name":"t","arguments":{}}}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	f.Add([]byte(`{"jsonrpc":"1.0","method":"tools/call"}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(``))
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`{"jsonrpc":"2.0"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		// The only invariant we assert here is no panic.
		// The return value (nil or a valid *MCPRequest) is irrelevant for fuzz testing.
		//nolint:errcheck
		mcpparse.ParseRequest(data) //nolint:errcheck
	})
}
