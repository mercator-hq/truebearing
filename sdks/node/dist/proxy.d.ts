/**
 * PolicyProxy — wraps an LLM client to route tool calls through TrueBearing.
 *
 * This module owns the lifecycle of the TrueBearing proxy subprocess (when running
 * in subprocess mode) and the header injection that associates every outbound
 * request with a session ID and a signed agent JWT.
 *
 * Callers interact primarily with PolicyProxy.create(). resolveJwt, findFreePort,
 * startSubprocess, and waitForReady are exported for testability.
 */
import * as childProcess from "child_process";
/** Options for constructing a PolicyProxy. */
export interface PolicyProxyOptions {
    /**
     * Path to a TrueBearing policy YAML file. Required in subprocess mode
     * (when proxyUrl is not provided).
     */
    policy?: string;
    /**
     * URL of a running TrueBearing proxy. When given, no subprocess is spawned.
     * This is the sidecar deployment model.
     */
    proxyUrl?: string;
    /**
     * The agent JWT string. Takes precedence over the TRUEBEARING_AGENT_JWT
     * environment variable and the key file.
     */
    agentJwt?: string;
    /**
     * Agent name whose JWT is read from ~/.truebearing/keys/<agentName>.jwt.
     * Used only when agentJwt is not provided and the env var is absent.
     */
    agentName?: string;
    /**
     * Explicit session ID to continue a prior session. Defaults to a fresh
     * UUID v4 when not provided.
     */
    sessionId?: string;
    /**
     * Upstream MCP server URL passed to truebearing serve in subprocess mode.
     * Defaults to http://localhost:8080.
     */
    upstream?: string;
}
/**
 * Wraps an LLM client to route all MCP tool calls through the TrueBearing proxy.
 *
 * Two operating modes:
 *
 * **Subprocess mode** (use PolicyProxy.create() with a policy path): spawns
 * `truebearing serve` on a random local port, waits for the /health endpoint to
 * return 200, then reconfigures the wrapped client to use the proxy URL. The
 * subprocess is terminated when close() is called.
 *
 * **Explicit proxy mode** (pass proxyUrl): no subprocess is spawned. The proxy
 * must already be running (e.g. as a sidecar container).
 *
 * Header injection: every outbound request carries:
 *   - `Authorization: Bearer <jwt>` — the agent JWT issued by
 *     `truebearing agent register`.
 *   - `X-TrueBearing-Session-ID: <uuid4>` — the session identifier that
 *     scopes sequence evaluation and budget tracking to this agent run.
 *
 * @example
 * // Subprocess mode:
 * import { PolicyProxy } from '@mercator/truebearing';
 * import Anthropic from '@anthropic-ai/sdk';
 *
 * const proxy = await PolicyProxy.create(new Anthropic(), { policy: './policy.yaml' });
 * try {
 *   const response = await (proxy.client as Anthropic).messages.create({ ... });
 * } finally {
 *   proxy.close();
 * }
 *
 * @example
 * // Explicit proxy mode — proxy already running as a sidecar:
 * const proxy = await PolicyProxy.create(new Anthropic(), {
 *   proxyUrl: 'http://localhost:7773',
 *   agentJwt: process.env.TRUEBEARING_AGENT_JWT,
 * });
 */
export declare class PolicyProxy {
    /** @internal exposed for testing */
    readonly _sessionId: string;
    /** @internal exposed for testing */
    _proc: childProcess.ChildProcess | null;
    /** @internal exposed for testing */
    readonly _jwt: string | null;
    /** @internal exposed for testing */
    readonly _proxyUrl: string;
    /** @internal exposed for testing */
    _client: unknown;
    private constructor();
    /**
     * Create a PolicyProxy, optionally spawning a truebearing serve subprocess.
     *
     * In subprocess mode (policy provided, no proxyUrl), this method:
     * 1. Finds a free local port.
     * 2. Spawns `truebearing serve` on that port.
     * 3. Polls /health until the proxy is ready.
     * 4. Returns a PolicyProxy whose .client is reconfigured to route through the proxy.
     *
     * In explicit proxy mode (proxyUrl provided), no subprocess is spawned and
     * no async work is required. The method still returns a Promise for API
     * consistency.
     *
     * Throws if policy is absent in subprocess mode, or if the proxy does not
     * become ready within 30 seconds.
     */
    static create(client: unknown, options?: PolicyProxyOptions): Promise<PolicyProxy>;
    /**
     * Poll /health until the proxy responds 200 or the timeout expires.
     *
     * Extracted as an instance method so tests can spy on it without needing to
     * intercept raw HTTP calls.
     *
     * @internal
     */
    _waitForReady(): Promise<void>;
    /** The reconfigured wrapped client, ready to route requests through the proxy. */
    get client(): unknown;
    /** Terminate the proxy subprocess if one was spawned by this instance. */
    close(): void;
    /**
     * Support `await using proxy = await PolicyProxy.create(...)` via TC39
     * explicit resource management (TypeScript 5.2+).
     */
    [Symbol.asyncDispose](): Promise<void>;
}
/**
 * Return the agent JWT from the first available source.
 *
 * Priority:
 * 1. agentJwt argument (explicit).
 * 2. TRUEBEARING_AGENT_JWT environment variable.
 * 3. ~/.truebearing/keys/<agentName>.jwt file (when agentName is given).
 */
export declare function resolveJwt(agentJwt: string | undefined, agentName: string | undefined): string | null;
/**
 * Return an available TCP port on localhost.
 *
 * The server is closed immediately after the OS assigns a port. There is a
 * small TOCTOU window before the subprocess binds the port, but it is
 * acceptable for local development use.
 */
export declare function findFreePort(): Promise<number>;
/**
 * Spawn `truebearing serve` on the given port and return the process handle.
 *
 * Output is suppressed because operators observe proxy behaviour via
 * `truebearing audit query`, not subprocess stdout/stderr.
 */
export declare function startSubprocess(port: number, policy: string, upstream: string | undefined): childProcess.ChildProcess;
//# sourceMappingURL=proxy.d.ts.map