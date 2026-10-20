/**
 * Where a plan document lives, and what it is called.
 *
 * Kept apart from the provider so the naming rules can be tested without a
 * window: a title is derived from the model's own first line, and the identity
 * of the document is the conversation plus the run that produced it. The URI is
 * deterministic, so opening the same plan twice reveals one tab rather than
 * making a second.
 */

export const PLAN_SCHEME = 'anvil-plan';

/**
 * The one sentence an approved plan sends.
 *
 * Fixed on purpose: the plan is already the last answer in the conversation, so
 * the turn that carries it out needs only to be told that it may. A prompt
 * carrying the plan again would put the same text in the history twice and give
 * the model a second, subtly different copy to follow.
 */
export const PLAN_APPROVED_PROMPT = 'The plan is approved. Carry it out.';

/** Longest tab title taken from a plan, in characters. */
const TITLE_LIMIT = 60;

/**
 * A tab title from the plan's first line, which is usually its heading.
 *
 * Markdown punctuation is dropped rather than escaped: a tab reads as a name,
 * and `## Plan: rename the parser` is not a better name than `Plan: rename the
 * parser`. The characters a path cannot hold are replaced too, because this
 * becomes the document's path.
 */
export function planTitle(text: string): string {
  for (const line of text.split(/\r?\n/)) {
    const cleaned = line
      .replace(/^\s{0,3}#{1,6}\s*/, '')
      .replace(/[*_`>#]/g, '')
      .trim();
    if (cleaned !== '') return clip(cleaned);
  }
  return 'Plan';
}

function clip(value: string): string {
  // What a URI cannot carry in a path, and what would end the title early by
  // being read as the query or the fragment. A colon is legal in a path and is
  // left alone: `Plan: rename` is the heading, not a separator. A slash would
  // make the title a nested path, so the tab would show only the last segment.
  const safe = value
    // The control range is deliberate: a title may contain any byte the model
    // wrote, and a tab that cannot be drawn is worse than one with a hyphen.
    // eslint-disable-next-line no-control-regex
    .replace(/[<>"{}|\\^`?#%\u0000-\u001F/]/g, '-')
    .replace(/\s+/g, ' ')
    .trim();
  const title = safe === '' ? 'Plan' : safe;
  return title.length <= TITLE_LIMIT ? title : `${title.slice(0, TITLE_LIMIT - 1)}…`;
}

/** The path a plan document is addressed by; the tab shows its basename. */
export function planPath(title: string): string {
  return `/${title}.md`;
}

/** The query that names which conversation and run a document belongs to. */
export function planQuery(agentId: string, runId: string): string {
  return new URLSearchParams({ agent: agentId, run: runId }).toString();
}

/** Reads the two ids back. A document with either one missing names nothing. */
export function planTarget(query: string): { agentId: string; runId: string } | undefined {
  const params = new URLSearchParams(query);
  const agentId = params.get('agent') ?? '';
  const runId = params.get('run') ?? '';
  if (agentId === '' || runId === '') return undefined;
  return { agentId, runId };
}
