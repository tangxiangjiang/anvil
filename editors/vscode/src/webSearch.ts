/**
 * The web-search setting and the wire field it becomes.
 *
 * Pure on purpose: what is sent to the host is decided here, without VS Code,
 * so a test can pin the shape — including the fact that it has no key field.
 * The credential follows the model key's discipline: SecretStorage, then the
 * child environment, never this module and never the protocol.
 */

import { WebSearchParams } from './protocol';

export interface WebSearchSettings {
  enabled: boolean;
  /** `http` or `mcp`; anything else is treated as the default, http. */
  provider: string;
  baseUrl: string;
  /** 0 means the SDK default. */
  maxResults: number;
}

/**
 * The session field to send, or undefined when the tools are off.
 *
 * An enabled http provider with no endpoint is still sent: the host refuses it
 * with a message naming `web_search.base_url`, which is a better answer than a
 * session that quietly has no tools. `maxResults` is omitted at zero so the
 * request says nothing it does not mean.
 */
export function webSearchParams(settings: WebSearchSettings): WebSearchParams | undefined {
  if (!settings.enabled) return undefined;
  const provider = settings.provider.trim().toLowerCase() === 'mcp' ? 'mcp' : 'http';
  const params: WebSearchParams = { provider };
  // An endpoint is the http provider's business; the mcp one would only be
  // carrying a field nothing reads.
  const baseUrl = settings.baseUrl.trim();
  if (provider === 'http' && baseUrl !== '') params.base_url = baseUrl;
  if (settings.maxResults > 0) params.max_results = settings.maxResults;
  return params;
}

/** Whether this configuration expects a key. The mcp provider searches through a server instead. */
export function webSearchNeedsKey(settings: WebSearchSettings): boolean {
  return settings.enabled && settings.provider.trim().toLowerCase() !== 'mcp';
}
