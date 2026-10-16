/**
 * Paths the extension derives from input, without a window.
 *
 * A mention is a workspace-relative, forward-slashed path and nothing else. The
 * host is the one that reads files, decides how much of each to include, and
 * refuses anything that leaves the workspace, so the client's only job here is
 * to name a path the same way twice. The same goes for the paths a tool call's
 * arguments name: the call is the model's, the sandbox is the SDK's, and what
 * this module answers is only "which files would that be".
 *
 * No `vscode` import: this is the part worth testing directly, and paths are
 * where the platform differences live.
 */

/** What separates path segments on this platform. */
const SEP = process.platform === 'win32' ? '\\' : '/';

/**
 * Normalizes `fsPath` to a workspace-relative path, or returns undefined when
 * it lies outside `root`.
 *
 * Undefined is not a refusal to send — the host refuses escapes itself, and it
 * has to, because it is the one that would do the reading. It is so the UI can
 * say "that file is not in this folder" rather than surfacing the host's error
 * for a pick the user could not have made deliberately.
 */
export function relativeMention(root: string, fsPath: string): string | undefined {
  const base = trimTrailingSeparators(root);
  if (base === '') return undefined;
  const path = trimTrailingSeparators(fsPath);

  const fold = (value: string): string => (process.platform === 'win32' ? value.toLowerCase() : value);
  if (fold(path) === fold(base)) return undefined;
  if (!fold(path).startsWith(fold(base + SEP))) return undefined;

  const relative = path.slice(base.length + 1).split(SEP).join('/');
  // A path that normalizes away to nothing, or that climbs, is not a mention.
  if (relative === '' || relative.split('/').includes('..')) return undefined;
  return relative;
}

function trimTrailingSeparators(path: string): string {
  let end = path.length;
  while (end > 1 && (path[end - 1] === '/' || path[end - 1] === '\\')) end--;
  return path.slice(0, end);
}

/**
 * The label a mention chip shows: the last segment, with the folder it is in
 * when two picks would otherwise look identical.
 */
export function mentionLabel(mention: string): string {
  const parts = mention.split('/');
  return parts[parts.length - 1] || mention;
}

/** Drops duplicates while keeping the order the user picked in. */
export function dedupeMentions(mentions: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const mention of mentions) {
    if (mention === '' || seen.has(mention)) continue;
    seen.add(mention);
    out.push(mention);
  }
  return out;
}

/** Tools whose `path` argument names the one file they are about to change. */
const SINGLE_PATH_TOOLS = new Set(['write_file', 'edit_file']);

/**
 * The headers `apply_patch` uses to name each file it touches. This is the
 * tool's own grammar and the host's patch parser is the authority on it; what
 * is needed here is narrow recognition of the header lines, not a second patch
 * implementation. Like that parser, the match is anchored at the start of the
 * line and nothing is trimmed first, so a hunk body line that merely mentions a
 * header — prefixed by a space, a `+`, or a `-` — is not one.
 */
const PATCH_HEADER = /^\*\*\*[ \t]+(?:Update|Add|Delete) File:[ \t]*(\S.*)$/;

/**
 * The workspace-relative paths a tool call is about to write, as far as its
 * arguments say.
 *
 * Under-reporting is the failure that matters: a change the list omits is a
 * change the user is not told about. Over-reporting is not, because the list
 * re-reads every path after the turn and drops the ones that did not move. That
 * asymmetry is why a blind scan of the patch text is the right call.
 */
export function toolCallPaths(tool: string | undefined, args: unknown): string[] {
  if (!tool || !args || typeof args !== 'object') return [];
  const fields = args as Record<string, unknown>;
  if (SINGLE_PATH_TOOLS.has(tool)) {
    return typeof fields.path === 'string' && fields.path !== '' ? [forward(fields.path)] : [];
  }
  if (tool === 'apply_patch' && typeof fields.patch === 'string') {
    const out: string[] = [];
    for (const line of fields.patch.split('\n')) {
      const match = PATCH_HEADER.exec(line);
      if (match) out.push(forward(match[1].trim()));
    }
    return out;
  }
  return [];
}

function forward(path: string): string {
  return path.split('\\').join('/');
}
