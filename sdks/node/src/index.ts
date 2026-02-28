/**
 * TrueBearing Node.js SDK.
 *
 * Exposes PolicyProxy — the single entry point for wrapping an LLM client to
 * route all MCP tool calls through the TrueBearing transparent proxy.
 *
 * @example
 * import { PolicyProxy } from '@mercator/truebearing';
 * import Anthropic from '@anthropic-ai/sdk';
 *
 * const proxy = await PolicyProxy.create(new Anthropic(), { policy: './policy.yaml' });
 * // proxy.client is the Anthropic client configured to route through TrueBearing.
 * proxy.close();
 */
export { PolicyProxy } from "./proxy";
export type { PolicyProxyOptions } from "./proxy";
