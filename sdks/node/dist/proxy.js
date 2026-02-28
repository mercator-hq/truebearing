"use strict";
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
var __createBinding = (this && this.__createBinding) || (Object.create ? (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    var desc = Object.getOwnPropertyDescriptor(m, k);
    if (!desc || ("get" in desc ? !m.__esModule : desc.writable || desc.configurable)) {
      desc = { enumerable: true, get: function() { return m[k]; } };
    }
    Object.defineProperty(o, k2, desc);
}) : (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    o[k2] = m[k];
}));
var __setModuleDefault = (this && this.__setModuleDefault) || (Object.create ? (function(o, v) {
    Object.defineProperty(o, "default", { enumerable: true, value: v });
}) : function(o, v) {
    o["default"] = v;
});
var __importStar = (this && this.__importStar) || (function () {
    var ownKeys = function(o) {
        ownKeys = Object.getOwnPropertyNames || function (o) {
            var ar = [];
            for (var k in o) if (Object.prototype.hasOwnProperty.call(o, k)) ar[ar.length] = k;
            return ar;
        };
        return ownKeys(o);
    };
    return function (mod) {
        if (mod && mod.__esModule) return mod;
        var result = {};
        if (mod != null) for (var k = ownKeys(mod), i = 0; i < k.length; i++) if (k[i] !== "default") __createBinding(result, mod, k[i]);
        __setModuleDefault(result, mod);
        return result;
    };
})();
Object.defineProperty(exports, "__esModule", { value: true });
exports.PolicyProxy = void 0;
exports.resolveJwt = resolveJwt;
exports.findFreePort = findFreePort;
exports.startSubprocess = startSubprocess;
const childProcess = __importStar(require("child_process"));
const fs = __importStar(require("fs"));
const http = __importStar(require("http"));
const net = __importStar(require("net"));
const os = __importStar(require("os"));
const path = __importStar(require("path"));
const crypto_1 = require("crypto");
/** Maximum milliseconds to wait for the proxy subprocess to become ready. */
const READINESS_TIMEOUT_MS = 30_000;
/** Milliseconds between /health polls while waiting for subprocess readiness. */
const POLL_INTERVAL_MS = 250;
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
class PolicyProxy {
    /** @internal exposed for testing */
    _sessionId;
    /** @internal exposed for testing */
    _proc;
    /** @internal exposed for testing */
    _jwt;
    /** @internal exposed for testing */
    _proxyUrl;
    /** @internal exposed for testing */
    _client;
    constructor(client, proxyUrl, jwt, sessionId, proc) {
        this._sessionId = sessionId;
        this._jwt = jwt;
        this._proxyUrl = proxyUrl;
        this._proc = proc;
        this._client = configureClient(client, proxyUrl, jwt, sessionId);
    }
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
    static async create(client, options = {}) {
        const { policy, proxyUrl, agentJwt, agentName, sessionId, upstream } = options;
        const resolvedSessionId = sessionId ?? (0, crypto_1.randomUUID)();
        const jwt = resolveJwt(agentJwt, agentName);
        if (proxyUrl !== undefined) {
            return new PolicyProxy(client, proxyUrl.replace(/\/$/, ""), jwt, resolvedSessionId, null);
        }
        if (policy === undefined) {
            throw new Error("'policy' is required when not providing an explicit 'proxyUrl'. " +
                "Either pass { policy: './policy.yaml' } (subprocess mode) or " +
                "{ proxyUrl: 'http://localhost:7773' } (explicit proxy mode).");
        }
        const port = await findFreePort();
        const resolvedProxyUrl = `http://localhost:${port}`;
        const proc = startSubprocess(port, policy, upstream);
        const instance = new PolicyProxy(client, resolvedProxyUrl, jwt, resolvedSessionId, proc);
        try {
            await instance._waitForReady();
        }
        catch (err) {
            proc.kill();
            throw err;
        }
        return instance;
    }
    /**
     * Poll /health until the proxy responds 200 or the timeout expires.
     *
     * Extracted as an instance method so tests can spy on it without needing to
     * intercept raw HTTP calls.
     *
     * @internal
     */
    async _waitForReady() {
        const healthUrl = `${this._proxyUrl}/health`;
        const deadline = Date.now() + READINESS_TIMEOUT_MS;
        while (Date.now() < deadline) {
            const ok = await checkHealth(healthUrl);
            if (ok)
                return;
            await sleep(POLL_INTERVAL_MS);
        }
        throw new Error(`TrueBearing proxy did not become ready within ${READINESS_TIMEOUT_MS / 1000}s. ` +
            "Verify that the 'truebearing' binary is on PATH, the policy file is valid, " +
            `and no other process is occupying the port (${this._proxyUrl}).`);
    }
    /** The reconfigured wrapped client, ready to route requests through the proxy. */
    get client() {
        return this._client;
    }
    /** Terminate the proxy subprocess if one was spawned by this instance. */
    close() {
        if (this._proc !== null) {
            this._proc.kill("SIGTERM");
            this._proc = null;
        }
    }
    /**
     * Support `await using proxy = await PolicyProxy.create(...)` via TC39
     * explicit resource management (TypeScript 5.2+).
     */
    [Symbol.asyncDispose]() {
        this.close();
        return Promise.resolve();
    }
}
exports.PolicyProxy = PolicyProxy;
// ---------------------------------------------------------------------------
// Module-level helpers (exported for testability)
// ---------------------------------------------------------------------------
/**
 * Return the agent JWT from the first available source.
 *
 * Priority:
 * 1. agentJwt argument (explicit).
 * 2. TRUEBEARING_AGENT_JWT environment variable.
 * 3. ~/.truebearing/keys/<agentName>.jwt file (when agentName is given).
 */
function resolveJwt(agentJwt, agentName) {
    if (agentJwt)
        return agentJwt;
    const envVal = process.env["TRUEBEARING_AGENT_JWT"];
    if (envVal)
        return envVal;
    if (agentName) {
        const keyPath = path.join(os.homedir(), ".truebearing", "keys", `${agentName}.jwt`);
        return fs.readFileSync(keyPath, "utf8").trim();
    }
    return null;
}
/**
 * Return an available TCP port on localhost.
 *
 * The server is closed immediately after the OS assigns a port. There is a
 * small TOCTOU window before the subprocess binds the port, but it is
 * acceptable for local development use.
 */
function findFreePort() {
    return new Promise((resolve, reject) => {
        const srv = net.createServer();
        srv.on("error", reject);
        srv.listen(0, "127.0.0.1", () => {
            const port = srv.address().port;
            srv.close((err) => {
                if (err)
                    reject(err);
                else
                    resolve(port);
            });
        });
    });
}
/**
 * Spawn `truebearing serve` on the given port and return the process handle.
 *
 * Output is suppressed because operators observe proxy behaviour via
 * `truebearing audit query`, not subprocess stdout/stderr.
 */
function startSubprocess(port, policy, upstream) {
    const upstreamUrl = upstream ?? "http://localhost:8080";
    return childProcess.spawn("truebearing", [
        "serve",
        "--port",
        String(port),
        "--policy",
        policy,
        "--upstream",
        upstreamUrl,
    ], { stdio: "ignore" });
}
/** Issue a GET to url and return true iff the response status is 200. */
function checkHealth(url) {
    return new Promise((resolve) => {
        const req = http.get(url, { timeout: 2000 }, (res) => {
            resolve(res.statusCode === 200);
            // Consume the response body to free the socket.
            res.resume();
        });
        req.on("error", () => resolve(false));
        req.on("timeout", () => {
            req.destroy();
            resolve(false);
        });
    });
}
function sleep(ms) {
    return new Promise((resolve) => setTimeout(resolve, ms));
}
/**
 * Reconfigure client to route through the proxy and inject required headers.
 *
 * Returns a new client instance for known SDK types, or the original client
 * unchanged when the SDK is unrecognised.
 *
 * Design: SDK detection uses duck typing on the withOptions method so the SDK
 * packages remain optional runtime dependencies. PolicyProxy works without them
 * when proxyUrl is supplied explicitly and the caller configures headers
 * themselves. This mirrors the Python SDK's try/import pattern.
 */
function configureClient(client, proxyUrl, jwt, sessionId) {
    const extraHeaders = {};
    if (jwt) {
        extraHeaders["Authorization"] = `Bearer ${jwt}`;
    }
    extraHeaders["X-TrueBearing-Session-ID"] = sessionId;
    // Anthropic SDK (and any compatible SDK) exposes withOptions() to return a
    // new client instance with overridden options. Use duck typing so the package
    // stays free of runtime dependencies.
    if (client !== null &&
        typeof client === "object" &&
        "withOptions" in client &&
        typeof client.withOptions === "function") {
        return client.withOptions({ baseURL: proxyUrl, defaultHeaders: extraHeaders });
    }
    // Unrecognised client: return unchanged. The caller is responsible for
    // header injection in this case (e.g. via a custom fetch wrapper).
    return client;
}
//# sourceMappingURL=proxy.js.map