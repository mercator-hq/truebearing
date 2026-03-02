//go:build wasip1

package main

import (
	"bufio"
	"encoding/json"
	"os"
)

// main runs the WASM engine as a WASI stdin/stdout JSON processor. Each line
// read from stdin is a JSON object with policy, session, and call fields. The
// corresponding decision is written as a JSON object to stdout, followed by a
// newline.
//
// This entry point satisfies the wasip1 build target for compilation
// verification (GOOS=wasip1 GOARCH=wasm go build ./cmd/wasm/). It also enables
// server-side WASM runtimes (Wasmtime, WasmEdge) to drive evaluation via
// standard I/O. For Node.js integration, the js/wasm target (main_js.go) is
// preferred as it provides a synchronous function-call API without pipe overhead.
//
// Request format (one JSON object per line):
//
//	{"policy": <policyJSON>, "session": <sessionJSON>, "call": <callJSON>}
//
// Response format (one JSON object per line):
//
//	{"action": "allow|deny|escalate|shadow_deny", "reason": "...", "rule_id": "..."}
func main() {
	type request struct {
		Policy  json.RawMessage `json:"policy"`
		Session json.RawMessage `json:"session"`
		Call    json.RawMessage `json:"call"`
	}

	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)

	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			// Write an error decision so the caller always receives a response.
			_ = enc.Encode(json.RawMessage(
				`{"action":"deny","reason":"malformed request JSON: ` + err.Error() + `","rule_id":"wasm_input_error"}`,
			))
			continue
		}
		result := evaluateCore(req.Policy, req.Session, req.Call)
		_ = enc.Encode(json.RawMessage(result))
	}
}
