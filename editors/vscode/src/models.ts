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
  problem?: string;
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
