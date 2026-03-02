//go:build js && wasm

package main

import "syscall/js"

// main registers the truebearingEvaluate global function in the JavaScript
// runtime and signals readiness via __truebearingReady (if the host set it),
// then blocks indefinitely to keep the Go runtime alive.
//
// JS callers receive a synchronous API:
//
//	const result = truebearingEvaluate(policyJSON, sessionJSON, callJSON)
//
// All three arguments are JSON strings. The return value is a JSON string
// containing { action, reason, rule_id }.
//
// Readiness: the host must provide __truebearingReady as a callable function
// before loading the WASM module. The WasmEngine class in wasm_engine.ts
// installs this signal automatically.
func main() {
	// Register the evaluate function in the global JS scope before signalling
	// readiness. The js.FuncOf callback is synchronous — it runs on the same
	// goroutine as the Go WASM runtime and returns a value directly to JS.
	js.Global().Set("truebearingEvaluate", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 3 {
			return `{"action":"deny","reason":"truebearingEvaluate requires exactly 3 arguments (policyJSON, sessionJSON, callJSON)","rule_id":"wasm_input_error"}`
		}
		policyJSON := []byte(args[0].String())
		sessionJSON := []byte(args[1].String())
		callJSON := []byte(args[2].String())
		return string(evaluateCore(policyJSON, sessionJSON, callJSON))
	}))

	// Notify the host that the Go runtime is initialised and the evaluate
	// function is ready to call. The WasmEngine TypeScript class awaits this
	// signal before resolving its load() promise.
	if ready := js.Global().Get("__truebearingReady"); ready.Type() == js.TypeFunction {
		ready.Invoke()
	}

	// Block forever to keep the Go runtime and the registered JS function alive.
	// The channel is never written to; the goroutine parks indefinitely.
	select {}
}
