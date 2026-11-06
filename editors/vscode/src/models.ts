import type { Effort, ModelCapabilities } from './protocol';

export const DEFAULT_MODEL_CATALOG_URL = 'https://athenai.mihoyo.com/v1/models';

export type ModelConnectionMode = 'single' | 'gateway';

export interface ModelSettings {
  mode: ModelConnectionMode;
  provider: 'openai' | 'anthropic';
  baseUrl: string;
  singleModelId: string;
  catalogUrl: string;
  gatewayAvailable: string[];
  gatewayDefaultModel: string;
  available: string[];
  defaultModel: string;
  capabilities: Record<string, ModelCapabilities>;
  capabilitySources: Record<string, 'explicit' | 'gateway'>;
  routes: Record<string, StoredModelRoute>;
  problem?: string;
}

export interface StoredModelCapabilities {
  capabilities: ModelCapabilities;
  source: 'explicit' | 'gateway';
}

export type ModelCredential = 'default' | 'openai' | 'anthropic';

export interface StoredModelRoute {
  provider: 'openai' | 'anthropic';
  baseUrl: string;
  credential: ModelCredential;
}

export const MAX_OUTPUT_TOKEN_LIMIT = 2_147_483_647;

/**
 * Validates the composer's per-conversation output ceiling. Zero is Off.
 * Unknown profiles retain compatibility behavior; an explicit false refuses
 * the control before a session is reopened.
 */
export function outputTokenLimitProblem(
  value: unknown,
  capabilities: ModelCapabilities | undefined,
): string | undefined {
  if (typeof value !== 'number' || !Number.isSafeInteger(value)) {
    return 'Output limit must be Off or a whole number.';
  }
  if (value < 0 || value > MAX_OUTPUT_TOKEN_LIMIT) {
    return `Output limit must be between 1 and ${MAX_OUTPUT_TOKEN_LIMIT}, or Off.`;
  }
  if (value > 0 && capabilities?.max_output_tokens === false) {
    return 'The selected model profile does not support an output-token limit.';
  }
  return undefined;
}

export function normalizeCapabilities(value: unknown): ModelCapabilities | undefined {
  if (!value || typeof value !== 'object') return undefined;
  const raw = value as Record<string, unknown>;
  const protocols = new Set(['none', 'openai_effort', 'anthropic_budget', 'anthropic_adaptive']);
  if (typeof raw.thinking !== 'string' || !protocols.has(raw.thinking)) return undefined;
  if (typeof raw.max_output_tokens !== 'boolean' || typeof raw.vision !== 'boolean') return undefined;
  const rawLevels = raw.effort_levels;
  if (rawLevels !== undefined && !Array.isArray(rawLevels)) return undefined;
  const levels: Effort[] = [];
  for (const level of rawLevels ?? []) {
    if ((level !== 'low' && level !== 'medium' && level !== 'high') || levels.includes(level)) {
      return undefined;
    }
    levels.push(level);
  }
  if (raw.thinking === 'none' && levels.length > 0) return undefined;
  return {
    thinking: raw.thinking as ModelCapabilities['thinking'],
    effort_levels: levels.length > 0 ? levels : undefined,
    max_output_tokens: raw.max_output_tokens,
    vision: raw.vision,
  };
}

export function normalizeModelCapabilities(value: unknown): Record<string, StoredModelCapabilities> {
  if (!value || typeof value !== 'object') return {};
  const out: Record<string, StoredModelCapabilities> = {};
  for (const [id, entry] of Object.entries(value as Record<string, unknown>)) {
    if (!validModelId(id) || !entry || typeof entry !== 'object') continue;
    const raw = entry as Record<string, unknown>;
    const capabilities = normalizeCapabilities(raw.capabilities);
    if (!capabilities) continue;
    out[id] = {
      capabilities,
      source: raw.source === 'explicit' ? 'explicit' : 'gateway',
    };
  }
  return out;
}

export function withGatewayCapabilities(
  current: Record<string, StoredModelCapabilities>,
  id: string,
  capabilities: unknown,
): Record<string, StoredModelCapabilities> {
  const key = id.trim();
  if (!validModelId(key) || current[key]?.source === 'explicit') return { ...current };
  const next = { ...current };
  const normalized = normalizeCapabilities(capabilities);
  if (normalized) next[key] = { capabilities: normalized, source: 'gateway' };
  else delete next[key];
  return next;
}

export function normalizeModelRoutes(value: unknown): Record<string, StoredModelRoute> {
  if (!value || typeof value !== 'object') return {};
  const out: Record<string, StoredModelRoute> = {};
  for (const [rawId, entry] of Object.entries(value as Record<string, unknown>)) {
    const id = rawId.trim();
    if (!validModelId(id) || !entry || typeof entry !== 'object') continue;
    const raw = entry as Record<string, unknown>;
    const provider = raw.provider === 'anthropic' ? 'anthropic' : raw.provider === 'openai' ? 'openai' : undefined;
    const baseUrl = typeof raw.baseUrl === 'string' ? raw.baseUrl.trim().replace(/\/+$/, '') : '';
    const credential =
      raw.credential === 'openai' || raw.credential === 'anthropic' ? raw.credential : 'default';
    if (!provider || baseUrlProblem(baseUrl)) continue;
    out[id] = { provider, baseUrl, credential };
  }
  return out;
}

export function connectionMode(value: unknown): ModelConnectionMode {
  return value === 'single' ? 'single' : 'gateway';
}

/** Stable, trimmed model ids. Invalid values and duplicates are discarded. */
export function normalizeModelIds(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  const seen = new Set<string>();
  const out: string[] = [];
  for (const item of value) {
    if (typeof item !== 'string') continue;
    const id = item.trim();
    if (!validModelId(id) || seen.has(id)) continue;
    seen.add(id);
    out.push(id);
  }
  return out;
}

export function validModelId(id: string): boolean {
  return (
    id.length > 0 &&
    id.length <= 256 &&
    ![...id].some((character) => /\s/.test(character) || character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127)
  );
}

export function baseUrlProblem(value: string): string | undefined {
  const text = value.trim();
  if (text === '') return 'Base URL is required.';
  let url: URL;
  try {
    url = new URL(text);
  } catch {
    return 'Base URL must be an absolute http(s) URL.';
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    return 'Base URL must use http or https.';
  }
  if (url.hash !== '') return 'Base URL cannot contain a fragment.';
  return undefined;
}

export function connectionProblem(
  mode: ModelConnectionMode,
  baseUrl: string,
  singleModelId: string,
): string | undefined {
  const baseProblem = baseUrlProblem(baseUrl);
  if (baseProblem) return baseProblem;
  if (mode === 'single' && !validModelId(singleModelId.trim())) {
    return 'Single API mode requires a valid Model ID without spaces.';
  }
  return undefined;
}

/**
 * Turns an exact OpenAI-compatible `/models` endpoint into the base URL the
 * existing host RPC accepts. Query strings are rejected because appending
 * `/models` after one would silently call a different endpoint.
 */
export function catalogBaseUrl(endpoint: string): string {
  let url: URL;
  try {
    url = new URL(endpoint.trim());
  } catch {
    throw new Error('The catalog URL must be an absolute http(s) URL.');
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    throw new Error('The catalog URL must use http or https.');
  }
  if (url.search !== '' || url.hash !== '') {
    throw new Error('The catalog URL cannot contain a query string or fragment.');
  }
  const path = url.pathname.replace(/\/+$/, '');
  if (!path.endsWith('/models')) {
    throw new Error('The catalog URL must end in /models.');
  }
  url.pathname = path.slice(0, -'/models'.length) || '/';
  return url.toString().replace(/\/$/, '');
}
