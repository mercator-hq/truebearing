/**
 * wasm_engine.ts — in-process TrueBearing policy engine via WASM.
 *
 * Loads the truebearing.wasm binary (built with GOOS=js GOARCH=wasm) and
 * exposes a synchronous evaluate() API. Unlike PolicyProxy, WasmEngine
 * performs enforcement entirely in-process with no sidecar, no HTTP round-
 * trip, and no subprocess to manage.
 *
 * The trade-off: the caller must maintain session state (events, taint,
 * counters) and pass the complete state on each call. WasmEngine is
 * stateless — it has no memory between evaluate() calls.
 *
 * @example
 * import { WasmEngine } from '@mercator/truebearing';
 *
 * const engine = await WasmEngine.load('./truebearing.wasm');
 * const decision = engine.evaluate(policyJSON, sessionState, callJSON);
 * // decision.action is 'allow' | 'deny' | 'escalate' | 'shadow_deny'
 */

import { readFileSync } from "fs";
import { execSync } from "child_process";
import * as path from "path";

/** Decision returned by the WASM engine for a single tool call evaluation. */
export interface WasmDecision {
  /** Outcome of the policy evaluation. */
  action: "allow" | "deny" | "escalate" | "shadow_deny";
  /** Human-readable explanation of the decision, empty for Allow. */
  reason: string;
  /** Identifies the policy rule that triggered a non-allow decision. */
  rule_id: string;
}

/** Per-session event entry passed to the WASM engine for sequence/rate-limit checks. */
export interface WasmSessionEvent {
  tool_name: string;
  /** Decision recorded for this event: "allow", "deny", "escalate", or "shadow_deny". */
  decision: string;
  /** Unix nanoseconds timestamp when the event was recorded. */
  recorded_at: number;
}

/** A previously approved escalation for a session + tool + argument hash. */
export interface WasmApprovedEscalation {
  tool_name: string;
  /** Hex-encoded SHA-256 of the raw argument JSON bytes that were approved. */
  args_hash: string;
}

/**
 * Complete per-session state passed to each evaluate() call.
 * The caller is responsible for keeping this up-to-date between calls.
 */
export interface WasmSessionState {
  session: {
    id: string;
    agent_name: string;
    policy_fingerprint: string;
    tainted: boolean;
    tool_call_count: number;
    estimated_cost_usd: number;
    terminated: boolean;
  };
  /** Full event history for the session. Sequence and rate-limit evaluators read this. */
  events: WasmSessionEvent[];
  /** Human-approved escalations. Escalation evaluator checks this before re-escalating. */
  approved_escalations?: WasmApprovedEscalation[];
  /**
   * Tool names the calling agent's parent is permitted to call.
   * Only required for child agents (those whose JWT carries a parent_agent claim).
   * Leave undefined or empty for root agents.
   */
  parent_tools?: string[];
}

// Declared in wasm_exec.js — added to globalThis when the file is loaded.
declare global {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const Go: any;
  // Set by WasmEngine.load() before launching the WASM module.
  function __truebearingReady(): void;
  // Registered by the Go main() function in main_js.go.
  function truebearingEvaluate(
    policyJSON: string,
    sessionJSON: string,
    callJSON: string
  ): string;
}

/**
 * WasmEngine loads and drives the TrueBearing policy engine compiled to WASM.
 * A single instance is sufficient for an entire process — the underlying Go
 * runtime is shared across all evaluate() calls.
 *
 * Usage:
 *   const engine = await WasmEngine.load('./truebearing.wasm');
 *   const decision = engine.evaluate(policyJSON, sessionState, callJSON);
 */
export class WasmEngine {
  private _ready = false;

  private constructor() {}

  /**
   * load() initialises the WASM runtime and waits for the Go entry point to
   * register the evaluate function. Call this once per process; subsequent
   * calls return the same instance from the module-level cache.
   *
   * @param wasmPath      - Path to truebearing.wasm (js/wasm build).
   * @param wasmExecPath  - Optional path to wasm_exec.js. Defaults to the
   *                        file from the installed Go toolchain (go env GOROOT).
   */
  static async load(
    wasmPath: string,
    wasmExecPath?: string
  ): Promise<WasmEngine> {
    if (_cachedEngine) return _cachedEngine;

    const execPath = wasmExecPath ?? resolveWasmExecPath();
    // Evaluate wasm_exec.js in the current Node.js context so that it adds the
    // Go class to globalThis. Using require() on an absolute path works for
    // both CommonJS and ESM-interop contexts.
    // eslint-disable-next-line @typescript-eslint/no-var-requires
    require(execPath);

    // Install the readiness callback before launching the WASM module so that
    // Go main() can call it synchronously during startup.
    const readyPromise = new Promise<void>((resolve) => {
      (globalThis as unknown as Record<string, unknown>).__truebearingReady =
        resolve;
    });

    const go = new (globalThis as unknown as { Go: new () => GoInstance }).Go();
    const wasmBuffer = readFileSync(wasmPath);
    // WebAssembly is a global in Node.js but its types are not included in the
    // ES2022 lib. Access it through globalThis to avoid the TS2708 error.
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const wasm = (globalThis as unknown as { WebAssembly: any }).WebAssembly;
    const { instance } = await wasm.instantiate(wasmBuffer, go.importObject);

    // Fire-and-forget: go.run() returns a Promise that resolves only when the
    // Go program exits. Since main_js.go blocks with select{}, the promise
    // never resolves. We await readyPromise instead to know when the evaluate
    // function has been registered.
    go.run(instance);
    await readyPromise;

    const engine = new WasmEngine();
    engine._ready = true;
    _cachedEngine = engine;
    return engine;
  }

  /**
   * evaluate runs the full policy pipeline against a single tool call and
   * returns the decision synchronously.
   *
   * @param policyJSON   - JSON string of the policy.Policy struct.
   * @param sessionState - Current session state including event history.
   * @param callJSON     - JSON string of the tool call
   *                       ({ session_id, agent_name, tool_name, arguments,
   *                          agent_env?, parent_agent?, requested_at }).
   */
  evaluate(
    policyJSON: string,
    sessionState: WasmSessionState,
    callJSON: string
  ): WasmDecision {
    return this.evaluateSerialized(
      policyJSON,
      JSON.stringify(sessionState),
      callJSON
    );
  }

  /**
   * evaluateSerialized is a high-performance variant of evaluate() for callers
   * that maintain a pre-serialized session state string and update it only when
   * the session mutates. Avoiding JSON.stringify on every call is the primary
   * optimisation for tight evaluation loops with large event histories.
   *
   * @param policyJSON   - JSON string of the policy.Policy struct.
   * @param sessionJSON  - JSON string of WasmSessionState (pre-serialized).
   * @param callJSON     - JSON string of the tool call.
   */
  evaluateSerialized(
    policyJSON: string,
    sessionJSON: string,
    callJSON: string
  ): WasmDecision {
    if (!this._ready) {
      throw new Error("WasmEngine not ready — call WasmEngine.load() first");
    }
    const result = (
      globalThis as unknown as {
        truebearingEvaluate: (a: string, b: string, c: string) => string;
      }
    ).truebearingEvaluate(policyJSON, sessionJSON, callJSON);
    return JSON.parse(result) as WasmDecision;
  }
}

// Module-level cache: one WASM runtime instance per process.
let _cachedEngine: WasmEngine | null = null;

// Minimal type for the Go WASM runtime instance.
// Uses any for the WASM-specific types because WebAssembly.Imports and
// WebAssembly.Instance are not available in the ES2022 lib (they are DOM-only).
interface GoInstance {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  importObject: any;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  run(instance: any): Promise<void>;
}

/**
 * resolveWasmExecPath finds wasm_exec.js from the installed Go toolchain.
 * Go 1.24+ ships it at $GOROOT/lib/wasm/wasm_exec.js.
 *
 * @throws if the Go toolchain is not installed or GOROOT cannot be determined.
 */
function resolveWasmExecPath(): string {
  // Prefer GOROOT env var for CI environments where go is not on PATH.
  const goroot =
    process.env.GOROOT ??
    execSync("go env GOROOT", { encoding: "utf8" }).trim();
  // Go 1.24+ location; fall back to the pre-1.24 location if not found.
  const candidates = [
    path.join(goroot, "lib", "wasm", "wasm_exec.js"),
    path.join(goroot, "misc", "wasm", "wasm_exec.js"),
  ];
  for (const p of candidates) {
    try {
      readFileSync(p); // throws if not found
      return p;
    } catch {
      // try next candidate
    }
  }
  throw new Error(
    `wasm_exec.js not found in ${goroot}/lib/wasm/ or ${goroot}/misc/wasm/. ` +
      `Install Go 1.24+ or pass the wasmExecPath argument to WasmEngine.load().`
  );
}
