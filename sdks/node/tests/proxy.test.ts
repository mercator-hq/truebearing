/**
 * Tests for @mercator/truebearing PolicyProxy.
 *
 * All tests run without requiring a live TrueBearing binary or a real Anthropic
 * SDK installation. SDK detection is exercised via duck-typed fake clients;
 * subprocess lifecycle is exercised by mocking child_process.spawn.
 *
 * Node.js built-in module properties (spawn, homedir, readFileSync) are
 * non-configurable, so vi.spyOn cannot redefine them. vi.mock() with
 * importOriginal is used to replace those modules with a mocked version whose
 * properties are writable.
 */

// ---------------------------------------------------------------------------
// Module mocks — must be declared before imports (Vitest hoists these).
// ---------------------------------------------------------------------------

import { vi } from "vitest";

vi.mock("child_process", async (importOriginal) => {
  const actual = await importOriginal<typeof import("child_process")>();
  return { ...actual, spawn: vi.fn() };
});

vi.mock("os", async (importOriginal) => {
  const actual = await importOriginal<typeof import("os")>();
  return { ...actual, homedir: vi.fn().mockReturnValue(actual.homedir()) };
});

vi.mock("fs", async (importOriginal) => {
  const actual = await importOriginal<typeof import("fs")>();
  return { ...actual, readFileSync: vi.fn() };
});

// ---------------------------------------------------------------------------
// Imports (receive the mocked modules above)
// ---------------------------------------------------------------------------

import * as childProcess from "child_process";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { PolicyProxy, findFreePort, resolveJwt } from "../src/proxy";

// ---------------------------------------------------------------------------
// Minimal stand-in for an Anthropic-compatible client used across all tests.
// ---------------------------------------------------------------------------

interface FakeConfigured {
  baseURL: string;
  defaultHeaders: Record<string, string>;
}

class FakeClient {
  /** @internal records the last withOptions call for assertions */
  _configured: FakeConfigured | null = null;

  withOptions(opts: {
    baseURL: string;
    defaultHeaders: Record<string, string>;
  }): FakeClient {
    const configured = new FakeClient();
    configured._configured = opts;
    return configured;
  }
}

/** Build a mock ChildProcess with all lifecycle methods spied. */
function makeMockProc(): childProcess.ChildProcess {
  return {
    kill: vi.fn().mockReturnValue(true),
    on: vi.fn(),
    pid: 12345,
  } as unknown as childProcess.ChildProcess;
}

// ---------------------------------------------------------------------------
// findFreePort
// ---------------------------------------------------------------------------

describe("findFreePort", () => {
  it("returns a port number in the valid range", async () => {
    const port = await findFreePort();
    expect(typeof port).toBe("number");
    expect(port).toBeGreaterThanOrEqual(1024);
    expect(port).toBeLessThanOrEqual(65535);
  });
});

// ---------------------------------------------------------------------------
// resolveJwt
// ---------------------------------------------------------------------------

describe("resolveJwt", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.mocked(fs.readFileSync).mockReset();
    vi.mocked(os.homedir).mockReset();
  });

  it("returns agentJwt kwarg when supplied, even if env var is set", () => {
    vi.stubEnv("TRUEBEARING_AGENT_JWT", "env-value");
    expect(resolveJwt("kwarg-value", undefined)).toBe("kwarg-value");
  });

  it("returns the env var when no agentJwt is supplied", () => {
    vi.stubEnv("TRUEBEARING_AGENT_JWT", "env-value");
    expect(resolveJwt(undefined, undefined)).toBe("env-value");
  });

  it("reads from the key file when only agentName is provided", () => {
    // Ensure the env var guard fails by deleting it.
    delete process.env["TRUEBEARING_AGENT_JWT"];
    vi.mocked(os.homedir).mockReturnValue("/fake-home");
    vi.mocked(fs.readFileSync).mockReturnValue("file-jwt-value\n");

    const result = resolveJwt(undefined, "my-agent");

    expect(result).toBe("file-jwt-value");
    expect(fs.readFileSync).toHaveBeenCalledWith(
      path.join("/fake-home", ".truebearing", "keys", "my-agent.jwt"),
      "utf8",
    );
  });

  it("returns null when no jwt source is available", () => {
    delete process.env["TRUEBEARING_AGENT_JWT"];
    expect(resolveJwt(undefined, undefined)).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// Session ID
// ---------------------------------------------------------------------------

describe("session ID", () => {
  it("generates a UUID v4 when sessionId is not provided", async () => {
    const proxy = await PolicyProxy.create(new FakeClient(), {
      proxyUrl: "http://localhost:7773",
    });
    expect(proxy._sessionId).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i,
    );
  });

  it("preserves an explicit sessionId", async () => {
    const proxy = await PolicyProxy.create(new FakeClient(), {
      proxyUrl: "http://localhost:7773",
      sessionId: "explicit-session-id",
    });
    expect(proxy._sessionId).toBe("explicit-session-id");
  });

  it("gives two proxies different session IDs", async () => {
    const p1 = await PolicyProxy.create(new FakeClient(), {
      proxyUrl: "http://localhost:7773",
    });
    const p2 = await PolicyProxy.create(new FakeClient(), {
      proxyUrl: "http://localhost:7773",
    });
    expect(p1._sessionId).not.toBe(p2._sessionId);
  });
});

// ---------------------------------------------------------------------------
// Header injection (withOptions-compatible client)
// ---------------------------------------------------------------------------

describe("header injection", () => {
  it("reconfigures a compatible client with proxy URL and required headers", async () => {
    const client = new FakeClient();
    const proxy = await PolicyProxy.create(client, {
      proxyUrl: "http://localhost:7773",
      agentJwt: "test-jwt",
      sessionId: "fixed-session-id",
    });

    const configured = (proxy._client as FakeClient)._configured!;
    expect(configured.baseURL).toBe("http://localhost:7773");
    expect(configured.defaultHeaders["Authorization"]).toBe("Bearer test-jwt");
    expect(configured.defaultHeaders["X-TrueBearing-Session-ID"]).toBe(
      "fixed-session-id",
    );
  });

  it("X-TrueBearing-Session-ID header matches proxy._sessionId", async () => {
    const proxy = await PolicyProxy.create(new FakeClient(), {
      proxyUrl: "http://localhost:7773",
      agentJwt: "test-jwt",
    });
    const configured = (proxy._client as FakeClient)._configured!;
    expect(configured.defaultHeaders["X-TrueBearing-Session-ID"]).toBe(
      proxy._sessionId,
    );
  });

  it("omits Authorization header when no JWT is resolvable", async () => {
    delete process.env["TRUEBEARING_AGENT_JWT"];
    const proxy = await PolicyProxy.create(new FakeClient(), {
      proxyUrl: "http://localhost:7773",
    });
    const configured = (proxy._client as FakeClient)._configured!;
    expect(configured.defaultHeaders["Authorization"]).toBeUndefined();
  });

  it("strips trailing slash from proxyUrl before injection", async () => {
    const proxy = await PolicyProxy.create(new FakeClient(), {
      proxyUrl: "http://localhost:7773/",
      agentJwt: "jwt",
    });
    const configured = (proxy._client as FakeClient)._configured!;
    expect(configured.baseURL).toBe("http://localhost:7773");
  });
});

// ---------------------------------------------------------------------------
// Client without withOptions (unrecognised SDK)
// ---------------------------------------------------------------------------

describe("unrecognised client", () => {
  it("returns the original client unchanged when no withOptions method exists", async () => {
    const plain = { messages: "messages-object" };
    const proxy = await PolicyProxy.create(plain, {
      proxyUrl: "http://localhost:7773",
    });
    expect(proxy._client).toBe(plain);
  });

  it("exposes .client for access to the wrapped instance", async () => {
    class DuckClient {
      messages = "messages-object";
    }
    const c = new DuckClient();
    const proxy = await PolicyProxy.create(c, {
      proxyUrl: "http://localhost:7773",
    });
    expect((proxy.client as DuckClient).messages).toBe("messages-object");
  });
});

// ---------------------------------------------------------------------------
// Subprocess lifecycle
// ---------------------------------------------------------------------------

describe("subprocess lifecycle", () => {
  let mockProc: childProcess.ChildProcess;

  beforeEach(() => {
    mockProc = makeMockProc();
    vi.mocked(childProcess.spawn).mockReturnValue(mockProc);
    // Suppress the real health check for all subprocess tests.
    vi.spyOn(PolicyProxy.prototype, "_waitForReady" as never).mockResolvedValue(
      undefined as never,
    );
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.mocked(childProcess.spawn).mockReset();
  });

  it("spawns truebearing serve with the expected arguments", async () => {
    await PolicyProxy.create(new FakeClient(), {
      policy: "/tmp/test.policy.yaml",
      agentJwt: "test-jwt",
    });

    expect(childProcess.spawn).toHaveBeenCalledOnce();
    const [cmd, args] = vi.mocked(childProcess.spawn).mock.calls[0] as unknown as [
      string,
      string[],
    ];
    expect(cmd).toBe("truebearing");
    expect(args).toContain("serve");
    expect(args).toContain("--policy");
    expect(args[args.indexOf("--policy") + 1]).toBe("/tmp/test.policy.yaml");
    expect(args).toContain("--port");
    expect(args).toContain("--upstream");
  });

  it("forwards the upstream option to truebearing serve", async () => {
    await PolicyProxy.create(new FakeClient(), {
      policy: "/tmp/p.yaml",
      upstream: "https://mcp.example.com",
    });

    const [, args] = vi.mocked(childProcess.spawn).mock.calls[0] as unknown as [
      string,
      string[],
    ];
    expect(args[args.indexOf("--upstream") + 1]).toBe(
      "https://mcp.example.com",
    );
  });

  it("suppresses subprocess output via stdio: ignore", async () => {
    await PolicyProxy.create(new FakeClient(), { policy: "/tmp/p.yaml" });

    const [, , opts] = vi.mocked(childProcess.spawn).mock.calls[0] as [
      string,
      string[],
      childProcess.SpawnOptions,
    ];
    expect(opts.stdio).toBe("ignore");
  });

  it("does not spawn a subprocess when proxyUrl is supplied", async () => {
    const proxy = await PolicyProxy.create(new FakeClient(), {
      proxyUrl: "http://localhost:7773",
    });
    expect(childProcess.spawn).not.toHaveBeenCalled();
    expect(proxy._proc).toBeNull();
  });

  it("close() sends SIGTERM to the subprocess", async () => {
    const proxy = await PolicyProxy.create(new FakeClient(), {
      policy: "/tmp/p.yaml",
      agentJwt: "jwt",
    });

    proxy.close();

    expect(mockProc.kill).toHaveBeenCalledWith("SIGTERM");
  });

  it("close() is idempotent — does not kill the process twice", async () => {
    const proxy = await PolicyProxy.create(new FakeClient(), {
      policy: "/tmp/p.yaml",
      agentJwt: "jwt",
    });

    proxy.close();
    proxy.close(); // second call must be a no-op

    expect(mockProc.kill).toHaveBeenCalledOnce();
    expect(proxy._proc).toBeNull();
  });

  it("asyncDispose calls close()", async () => {
    const proxy = await PolicyProxy.create(new FakeClient(), {
      policy: "/tmp/p.yaml",
    });
    await proxy[Symbol.asyncDispose]();
    expect(mockProc.kill).toHaveBeenCalledOnce();
  });
});

// ---------------------------------------------------------------------------
// Validation errors
// ---------------------------------------------------------------------------

describe("validation", () => {
  it("throws when neither policy nor proxyUrl is provided", async () => {
    await expect(
      PolicyProxy.create(new FakeClient(), { agentJwt: "jwt" }),
    ).rejects.toThrow("'policy' is required");
  });
});

// ---------------------------------------------------------------------------
// Readiness timeout: subprocess is killed when proxy never becomes ready
// ---------------------------------------------------------------------------

describe("readiness timeout", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.mocked(childProcess.spawn).mockReset();
  });

  it("kills the subprocess and rethrows when proxy does not become ready", async () => {
    const mockProc = makeMockProc();
    vi.mocked(childProcess.spawn).mockReturnValue(mockProc);
    vi.spyOn(PolicyProxy.prototype, "_waitForReady" as never).mockRejectedValue(
      new Error("TrueBearing proxy did not become ready") as never,
    );

    await expect(
      PolicyProxy.create(new FakeClient(), {
        policy: "/tmp/p.yaml",
        agentJwt: "jwt",
      }),
    ).rejects.toThrow("did not become ready");

    expect(mockProc.kill).toHaveBeenCalled();
  });
});
