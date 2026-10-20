/**
 * Where a tool result's document lives, and what it is called.
 *
 * Kept apart from the provider so the naming can be tested without a window:
 * the suffix is derived from the tool call id so the same call always names the
 * same tab, and the title is made safe for a URI path. The document's identity
 * also carries the id in its query, so a hash collision can only repeat a
 * display name, never merge two results.
 */

export const TOOL_OUTPUT_SCHEME = 'anvil-tool-output';

/** Longest tool name taken into the tab title, in characters. */
const TITLE_LIMIT = 40;

/**
 * A short name for a call, stable across clicks and safe in a path.
 *
 * FNV-1a over the tool call id, in base 36.
 */
export function outputSuffix(id: string): string {
  let hash = 0x811c9dc5;
  for (let i = 0; i < id.length; i += 1) {
    hash ^= id.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return hash.toString(36).padStart(6, '0').slice(-6);
}

/**
 * The path a result is addressed by; the tab shows its basename.
 *
 * What a URI cannot carry in a path, and what would end the title early by
 * being read as the query or the fragment, becomes a hyphen.
 */
export function outputPath(tool: string, id: string): string {
  const safe = tool
    // The control range is deliberate: a tool name is the host's to choose, and
    // a tab that cannot be drawn is worse than one with a hyphen.
    // eslint-disable-next-line no-control-regex
    .replace(/[\u0000-\u001F<>"{}|\\^`?#%/]/g, '-')
    .replace(/\s+/g, ' ')
    .trim();
  const base = safe === '' ? 'Tool' : safe.slice(0, TITLE_LIMIT);
  return `/${base} output (${outputSuffix(id)})`;
}
